package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/blockops-sh/ponos/config"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)


type Bot struct {
	client        *slack.Client
	config        *config.Config
	logger        *slog.Logger
	mcpClient     *GitHubMCPClient
	agentCoreURL  string
	httpClient    *http.Client
	githubHandler *GitHubDeployHandler
}

func NewBot(cfg *config.Config, logger *slog.Logger, slackClient *slack.Client, enableMCP bool) *Bot {
	if cfg.APIEndpoint == "" {
		fmt.Println("api_endpoint is not configured in ponos.yml")
		os.Exit(1)
	}

	var mcpClient *GitHubMCPClient
	if enableMCP {
		mcpClient = BuildGitHubMCPClient(cfg, logger)
	}

	httpClient := &http.Client{
		Timeout: 300 * time.Second,
	}
	
	if cfg.APIKey != "" {
		httpClient.Transport = &AuthenticatedTransport{
			APIKey:    cfg.APIKey,
			Transport: http.DefaultTransport,
		}
	}

	bot := &Bot{
		client:       slackClient,
		config:       cfg,
		logger:       logger,
		mcpClient:    mcpClient,
		agentCoreURL: cfg.APIEndpoint,
		httpClient:   httpClient,
	}

	if enableMCP {
		bot.githubHandler = NewGitHubDeployHandler(logger, cfg, slackClient, bot, mcpClient)
	}

	if cfg.APIKey != "" {
		go func() {
			if err := bot.syncConfigToAgentCore(); err != nil {
				if strings.Contains(err.Error(), "authentication failed") {
					logger.Error("Failed to sync configuration to agent-core: Authentication failed. Check your API key in ponos.yml", "error", err)
				} else {
					logger.Error("Failed to sync configuration to agent-core", "error", err)
				}
			} else {
				logger.Info("Configuration synced to agent-core successfully")
			}
		}()
	}

	return bot
}


func (b *Bot) handleSlackEvents(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(b.config.Integrations.Slack.SigningKey) == "" {
		b.logger.Error("Slack signing secret is not configured")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	body, ok := b.verifySlack(w, r, 1<<20)
	if !ok {
		return
	}

	var parseOpts []slackevents.Option
	if token := strings.TrimSpace(b.config.Integrations.Slack.VerifyToken); token != "" {
		parseOpts = append(parseOpts, slackevents.OptionVerifyToken(&slackevents.TokenComparator{
			VerificationToken: token,
		}))
	} else {
		parseOpts = append(parseOpts, slackevents.OptionNoVerifyToken())
	}

	eventsAPIEvent, err := slackevents.ParseEvent(body, parseOpts...)
	if err != nil {
		b.logger.Error("failed to parse Slack event", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	switch eventsAPIEvent.Type {
	case slackevents.URLVerification:
		var challenge *slackevents.ChallengeResponse
		err := json.Unmarshal(body, &challenge)
		if err != nil {
			b.logger.Error("failed to unmarshal challenge", "error", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(challenge.Challenge))

	case slackevents.CallbackEvent:
		innerEvent := eventsAPIEvent.InnerEvent
		switch ev := innerEvent.Data.(type) {
		case *slackevents.AppMentionEvent:
			go b.handleAppMention(ev)
		case *slackevents.MessageEvent:
			go b.handleMessage(ev)
		}
		w.WriteHeader(http.StatusOK)

	default:
		b.logger.Info("Unknown Slack event type, ignoring", "type", eventsAPIEvent.Type)
		w.WriteHeader(http.StatusOK)
	}
}

func (b *Bot) handleAppMention(event *slackevents.AppMentionEvent) {
	userMessage := b.normalizeAppMentionText(event.Text)
	if userMessage == "" {
		text := fmt.Sprintf("Hi <@%s>, can you provide a request after mentioning me? For example: `@Ponos AI upgrade ${network} ${chain} ${node type} to the desired ${version}`.", event.User)
		b.postThreadedSlackMessage(event.Channel, event.TimeStamp, text)
		return
	}

	go b.streamAgentResponseToSlack(event, userMessage)
}

func (b *Bot) handleMessage(event *slackevents.MessageEvent) {
	// disable bot-to-bot communication
	if event.BotID != "" {
		b.logger.Info("ignoring bot message", "bot_id", event.BotID, "channel", event.Channel)
		return
	}

	if event.ChannelType == "im" {
		text := fmt.Sprintf("Hi <@%s>! I received your direct message: %s", event.User, event.Text)
		if _, _, err := b.client.PostMessage(event.Channel, slack.MsgOptionText(text, false)); err != nil {
			b.logger.Error("error sending direct message response",
				"error", err,
				"user", event.User,
				"channel", event.Channel)
		}
	}
}

func (b *Bot) handleSlashCommand(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	bodyBytes, ok := b.verifySlack(w, r, 1<<20)
	if !ok {
		return
	}

	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	if err := r.ParseForm(); err != nil {
		b.logger.Error("failed to parse slash command form", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	command := r.PostForm.Get("command")
	text := r.PostForm.Get("text")
	channelID := r.PostForm.Get("channel_id")
	userID := r.PostForm.Get("user_id")

	var response *SlashCommandResponse

	switch command {
	case "/diagnose":
		response = b.handleDiagnosticsCommand(text, userID, channelID)
	default:
		response = &SlashCommandResponse{
			ResponseType: "ephemeral",
			Text:         "Unknown command",
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)

}

func (b *Bot) handleGitHubMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	if b.mcpClient == nil {
		b.logger.Error("GitHub MCP client not available")
		w.WriteHeader(http.StatusServiceUnavailable)
		json.NewEncoder(w).Encode(MCPResponse{
			JSONRPC: "2.0",
			ID:      0,
			Error: &MCPError{
				Code:    -32601,
				Message: "GitHub MCP client not available",
			},
		})
		return
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

	r.Body = io.NopCloser(io.LimitReader(r.Body, 1<<20))
	
	var mcpRequest MCPRequest
	if err := json.NewDecoder(r.Body).Decode(&mcpRequest); err != nil {
		b.logger.Error("failed to decode MCP request", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(MCPResponse{
			JSONRPC: "2.0",
			ID:      mcpRequest.ID,
			Error: &MCPError{
				Code:    -32700,
				Message: "Parse error",
			},
		})
		return
	}

	switch mcpRequest.Method {
	case "tools/call":
		b.handleMCPToolCall(w, mcpRequest)
	default:
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(MCPResponse{
			JSONRPC: "2.0",
			ID:      mcpRequest.ID,
			Error: &MCPError{
				Code:    -32601,
				Message: "Method not found",
			},
		})
	}
}

func (b *Bot) handleMCPToolCall(w http.ResponseWriter, mcpRequest MCPRequest) {
	params, ok := mcpRequest.Params.(map[string]interface{})
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(MCPResponse{
			JSONRPC: "2.0",
			ID:      mcpRequest.ID,
			Error: &MCPError{
				Code:    -32602,
				Message: "Invalid params",
			},
		})
		return
	}

	toolName, ok := params["name"].(string)
	if !ok {
		w.WriteHeader(http.StatusBadRequest)
		json.NewEncoder(w).Encode(MCPResponse{
			JSONRPC: "2.0",
			ID:      mcpRequest.ID,
			Error: &MCPError{
				Code:    -32602,
				Message: "Tool name required",
			},
		})
		return
	}

	arguments, ok := params["arguments"].(map[string]interface{})
	if !ok {
		arguments = make(map[string]interface{})
	}

	result, err := b.mcpClient.CallTool(context.Background(), toolName, arguments)
	if err != nil {
		b.logger.Error("MCP tool call failed", "tool", toolName, "error", err)
		w.WriteHeader(http.StatusOK)
		json.NewEncoder(w).Encode(MCPResponse{
			JSONRPC: "2.0",
			ID:      mcpRequest.ID,
			Error: &MCPError{
				Code:    -32000,
				Message: fmt.Sprintf("Tool execution failed: %v", err),
			},
		})
		return
	}

	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(MCPResponse{
		JSONRPC: "2.0",
		ID:      mcpRequest.ID,
		Result:  result,
	})
}

func (b *Bot) sendReleaseSummaryFromAgent(channel string, payload ReleasesWebhookPayload, summary *AgentSummary, prURL ...string) {
	blocks := buildReleaseNotificationBlocks(payload, summary, prURL...)

	if _, _, err := b.client.PostMessage(channel, slack.MsgOptionBlocks(blocks...)); err != nil {
		b.logger.Error("failed to send release summary to Slack",
			"error", err,
			"channel", channel,
			"summary", summary)
	}
}

func (b *Bot) handleDiagnosticsCommand(text, userID, channelID string) *SlashCommandResponse {
	service := strings.TrimSpace(text)
	if service == "" {
		return &SlashCommandResponse{
			ResponseType: "ephemeral",
			Text:         "Usage: /diagnose <service-name>",
		}
	}

	if b.config.APIEndpoint == "" {
		b.logger.Error("Agent-core URL is not configured")
		return &SlashCommandResponse{
			ResponseType: "ephemeral",
			Text:         "Agent-core URL is not configured. Please set AGENT_CORE_URL.",
		}
	}

	response := &SlashCommandResponse{
		ResponseType: "in_channel",
		Text:         fmt.Sprintf(":mag: Running diagnostics for *%s*...", service),
	}

	go func() {
		if err := b.triggerDiagnostics(service, channelID); err != nil {
			b.logger.Error("Diagnostics failed", "service", service, "error", err)
			b.postThreadedSlackMessage(channelID, "", fmt.Sprintf(":x: Diagnostics failed for *%s*: %v", service, err))
		}
	}()

	return response
}

func (b *Bot) triggerDiagnostics(service, channelID string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	payload := map[string]string{
		"service": service,
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal diagnostics payload: %w", err)
	}

	url := fmt.Sprintf("%s/diagnostics/run", b.config.APIEndpoint)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create diagnostics request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("diagnostics request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("nodeoperator api returned status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var diagResp DiagnosticsResponse

	if err := json.NewDecoder(resp.Body).Decode(&diagResp); err != nil {
		return fmt.Errorf("failed to decode diagnostics response: %w", err)
	}

	if !diagResp.Success {
		return fmt.Errorf("diagnostics service reported failure: %s", diagResp.Error)
	}

	blocks := b.slackDiagnosticMessageBlock(service, diagResp.Result)

	if _, _, err := b.client.PostMessage(
		channelID,
		slack.MsgOptionBlocks(blocks...),
	); err != nil {
		return fmt.Errorf("failed to post diagnostics result to Slack: %w", err)
	}

	return nil
}

func (b *Bot) slackDiagnosticMessageBlock(service string, result DiagnosticsResult) []slack.Block {
	var fields []*slack.TextBlockObject

	if result.Namespace != "" {
		fields = append(fields, mdField("Namespace", "`"+result.Namespace+"`"))
	}
	if result.ResourceType != "" {
		fields = append(fields, mdField("Resource", "`"+result.ResourceType+"`"))
	}
	if result.IssueURL != "" {
		fields = append(fields, mdField("GitHub issue", "<"+result.IssueURL+">"))
	}
	if result.EventsSummary != "" {
		fields = append(fields, mdField("Events", result.EventsSummary))
	}

	blocks := []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf(":white_check_mark: Diagnostics completed for *%s*.", service), false, false),
			nil, nil),
	}

	if len(fields) > 0 {
		blocks = append(blocks, slack.NewSectionBlock(nil, fields, nil))
	}

	if result.Summary != "" {
		blocks = append(blocks, slack.NewSectionBlock(mdField("Summary", result.Summary), nil, nil))
	}

	if result.LogSnippet != "" {
		blocks = append(blocks, slack.NewSectionBlock(mdField("Log snapshot", "```\n"+result.LogSnippet+"\n```"), nil, nil))
	}

	if result.ResourceDescription != "" {
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, "*Resource description was added to the GitHub issue.*", false, false), nil, nil))
	}

	if result.Prompt != "" {
		blocks = append(blocks, slack.NewSectionBlock(mdField("Prompt", "```\n"+result.Prompt+"\n```"), nil, nil))
	}

	return blocks
}

func (b *Bot) streamAgentResponseToSlack(event *slackevents.AppMentionEvent, userMessage string) {
	channel := event.Channel
	threadTS := event.TimeStamp
	userID := event.User

	ack := fmt.Sprintf(":wave: <@%s> working on \"%s\" …", userID, userMessage)
	b.postThreadedSlackMessage(channel, threadTS, ack)

	// Check if agent-core URL is configured
	if b.agentCoreURL == "" {
		b.postThreadedSlackMessage(channel, threadTS, ":x: Agent service is not available. Please check if agent-core is running.")
		return
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	updates := make(chan StreamingUpdate, 10)
	errCh := make(chan error, 1)

	go func() {
		defer close(updates)
		errCh <- b.StreamConversation(ctx, userMessage, nil, updates)
	}()

	var finalResponse string
	var toolSummaries []string
	var toolExecutionCount = make(map[string]int)

	for update := range updates {
		switch update.Type {
		case "assistant":
			if update.Message != "" {
				finalResponse = update.Message
			}
		case "tool_start":
			if update.Tool != "" {
				toolExecutionCount[update.Tool]++

				if toolExecutionCount[update.Tool] == 1 {
					status := fmt.Sprintf(":gear: Running *%s*…", formatToolName(update.Tool))
					b.postThreadedSlackMessage(channel, threadTS, status)
				} else if toolExecutionCount[update.Tool]%3 == 0 {
					status := fmt.Sprintf(":gear: Still working with *%s* (%d attempts)…", formatToolName(update.Tool), toolExecutionCount[update.Tool])
					b.postThreadedSlackMessage(channel, threadTS, status)
				}
			}
		case "tool_result":
			summary := update.Summary
			if summary == "" {
				summary = update.Message
			}
			if summary != "" && len(summary) < 500 {
				icon := ":white_check_mark:"
				if !update.Success {
					icon = ":x:"
				}
				if !strings.Contains(summary, "'success': True, 'command':") {
					toolSummaries = append(toolSummaries, fmt.Sprintf("%s %s", icon, summary))
				}
			}
		case "todo_update":
			if len(update.Todos) > 0 {
				status := fmt.Sprintf(":memo: Updated TODOs (%d items).", len(update.Todos))
				b.postThreadedSlackMessage(channel, threadTS, status)
			}
		}
	}

	if err := <-errCh; err != nil {
		b.logger.Error("agent conversation failed", "error", err)
		b.postThreadedSlackMessage(channel, threadTS, fmt.Sprintf(":x: I hit an error while processing your request: %v", err))
		return
	}

	if finalResponse == "" {
		finalResponse = "Done! Let me know if you need anything else."
	}

	// Only show tool summary if there are meaningful results and not too many
	if len(toolSummaries) > 0 && len(toolSummaries) <= 5 {
		finalResponse = fmt.Sprintf("%s\n\n*Tool summary:*\n%s", finalResponse, strings.Join(toolSummaries, "\n"))
	} else if len(toolSummaries) > 5 {
		finalResponse = fmt.Sprintf("%s\n\n*Executed %d tools successfully.*", finalResponse, len(toolSummaries))
	}

	blocks := []slack.Block{
		slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, finalResponse, false, false),
			nil, nil),
	}

	b.postThreadedSlackBlocks(channel, threadTS, blocks)
}

func (b *Bot) normalizeAppMentionText(text string) string {
	clean := strings.TrimSpace(text)
	if clean == "" {
		return ""
	}

	if strings.HasPrefix(clean, "<@") {
		if idx := strings.Index(clean, ">"); idx != -1 {
			clean = strings.TrimSpace(clean[idx+1:])
		}
	}

	return clean
}

func (b *Bot) postThreadedSlackMessage(channel, threadTS, text string) {
	if text == "" {
		return
	}
	b.post(channel, threadTS, slack.MsgOptionText(text, false))
}

func (b *Bot) postThreadedSlackBlocks(channel, threadTS string, blocks []slack.Block) {
	if len(blocks) == 0 {
		return
	}
	b.post(channel, threadTS, slack.MsgOptionBlocks(blocks...))
}

func formatToolName(name string) string {
	if name == "" {
		return "tool"
	}
	return strings.ReplaceAll(name, "_", " ")
}

func (b *Bot) doJSON(ctx context.Context, method, url string, in any, out any) error {
	var buf io.Reader
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		buf = bytes.NewReader(raw)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, buf)
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if resp.StatusCode == http.StatusUnauthorized {
			return fmt.Errorf("authentication failed: invalid or missing API key for agent-core (HTTP 401)")
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	if out == nil {
		return nil
	}
	return json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(out)
}

func (b *Bot) verifySlack(w http.ResponseWriter, r *http.Request, max int64) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(r.Body, max))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return nil, false
	}

	sv, err := slack.NewSecretsVerifier(r.Header, b.config.Integrations.Slack.SigningKey)
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return nil, false
	}

	if _, err := sv.Write(body); err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return nil, false
	}
	if err := sv.Ensure(); err != nil {
		w.WriteHeader(http.StatusUnauthorized)
		return nil, false
	}

	return body, true
}

func mdField(title, val string) *slack.TextBlockObject {
	return slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("*%s:*\n%s", title, val), false, false)
}

func (b *Bot) post(channel, threadTS string, opts ...slack.MsgOption) {
	if threadTS != "" {
		opts = append(opts, slack.MsgOptionTS(threadTS))
	}
	_, _, _ = b.client.PostMessage(channel, opts...)
}

func (b *Bot) syncConfigToAgentCore() error {
	const maxDuration = 60 * time.Second
	
	startTime := time.Now()
	attempt := 0
	var lastErr error
	
	for time.Since(startTime) < maxDuration {
		attempt++
		
		remainingTime := maxDuration - time.Since(startTime)
		ctxTimeout := 10 * time.Second
		if remainingTime < ctxTimeout {
			ctxTimeout = remainingTime
		}
		
		ctx, cancel := context.WithTimeout(context.Background(), ctxTimeout)
		err := b.trySyncConfig(ctx)
		cancel()
		
		if err == nil {
			b.logger.Info("Config sync successful", "attempt", attempt)
			return nil
		}
		
		lastErr = err
		
		if time.Since(startTime) >= maxDuration {
			break
		}
		
		backoff := time.Duration(attempt) * time.Second
		if backoff > 10*time.Second {
			backoff = 10 * time.Second
		}
		if time.Since(startTime)+backoff >= maxDuration {
			break
		}
		
		b.logger.Warn("Config sync failed, retrying", 
			"attempt", attempt, 
			"error", err.Error(), 
			"backoff", backoff)
		
		time.Sleep(backoff)
	}
	
	if lastErr != nil {
		return fmt.Errorf("sync failed after %v and %d attempts: %w", maxDuration, attempt, lastErr)
	}
	return fmt.Errorf("sync failed after %v and %d attempts", maxDuration, attempt)
}

func (b *Bot) trySyncConfig(ctx context.Context) error {
	ponosConfigPayload := map[string]any{
		"integrations": map[string]any{
			"github": map[string]any{
				"app_id":     b.config.Integrations.GitHub.AppID,
				"install_id": b.config.Integrations.GitHub.InstallID,
				"pem_key":    b.config.Integrations.GitHub.PEMKey,
				"bot_name":   b.config.Integrations.GitHub.BotName,
			},
			"slack": map[string]any{
				"token":        b.config.Integrations.Slack.Token,
				"signing_key":  b.config.Integrations.Slack.SigningKey,
				"verify_token": b.config.Integrations.Slack.VerifyToken,
				"channel":      b.config.Integrations.Slack.Channel,
			},
		},
		"projects": b.config.Projects,
	}

	if b.config.Diagnostics.Enabled {
		ponosConfigPayload["diagnostics"] = map[string]any{
			"enabled": true,
			"github": map[string]any{
				"owner": b.config.Diagnostics.GitHub.Owner,
				"repo":  b.config.Diagnostics.GitHub.Repo,
			},
			"kubernetes": map[string]any{
				"namespace":     b.config.Diagnostics.Kubernetes.Namespace,
				"resource_type": b.config.Diagnostics.Kubernetes.ResourceType,
			},
			"monitoring": map[string]any{
				"service":       b.config.Diagnostics.Monitoring.Service,
				"log_tail":      b.config.Diagnostics.Monitoring.LogTail,
				"eval_interval": b.config.Diagnostics.Monitoring.EvalInterval,
			},
		}
	}

	requestData := map[string]any{
		"message":      "Configuration sync",
		"ponos_config": ponosConfigPayload,
	}

	url := fmt.Sprintf("%s/agent/stream", b.agentCoreURL)
	return b.doJSON(ctx, "POST", url, requestData, nil)
}

func (b *Bot) sendCredentials(ctx context.Context, payload map[string]any) error {
	url := fmt.Sprintf("%s/api/v1/users/credentials", b.agentCoreURL)
	return b.doJSON(ctx, "POST", url, payload, nil)
}

func (b *Bot) sendProjects(ctx context.Context, payload map[string]any) error {
	url := fmt.Sprintf("%s/api/v1/users/projects", b.agentCoreURL)
	return b.doJSON(ctx, "POST", url, payload, nil)
}

func (b *Bot) ProcessReleaseUpdate(ctx context.Context, payload ReleasesWebhookPayload) (*AgentSummary, error) {
	request := map[string]any{
		"repositories": payload.Repositories,
		"releases":     payload.Releases,
		"event_type":   payload.EventType,
		"username":     payload.Username,
	}

	url := fmt.Sprintf("%s/blockchain/analyze-release", b.agentCoreURL)
	var result AgentSummary
	if err := b.doJSON(ctx, "POST", url, request, &result); err != nil {
		return nil, fmt.Errorf("agent-core request failed: %w", err)
	}

	if !result.Success {
		return nil, fmt.Errorf("agent-core analysis failed: %s", result.Error)
	}

	return &result, nil
}

func (b *Bot) ExtractImages(ctx context.Context, yamlContent string) ([]string, error) {
	prompt := fmt.Sprintf(`You are a blockchain infrastructure expert. Analyze this YAML file and identify ONLY the main blockchain node containers.

YAML Content:
%s

Return only a JSON array of image repository names (without tags): ["repo1/image1", "repo2/image2"]
If no blockchain containers found, return: []`, yamlContent)

	request := map[string]any{
		"message": prompt,
		"context": map[string]any{
			"source":    "ponos-yaml-analysis",
			"timestamp": time.Now().Format(time.RFC3339),
			"user_type": "blockchain_operator",
		},
	}

	url := fmt.Sprintf("%s/agent/simple", b.agentCoreURL)
	var response map[string]any
	if err := b.doJSON(ctx, "POST", url, request, &response); err != nil {
		return nil, fmt.Errorf("agent-core request failed: %w", err)
	}

	content, ok := response["content"].(string)
	if !ok {
		return nil, fmt.Errorf("invalid response format from agent-core")
	}

	// Parse JSON array from response
	var repos []string
	if err := b.extractAndUnmarshalJSON(content, &repos); err != nil {
		b.logger.Warn("Failed to parse YAML analysis response", "error", err, "response", content)
		return []string{}, nil
	}
	return repos, nil
}

func (b *Bot) StreamConversation(ctx context.Context, userMessage string, conversationHistory []map[string]string, updates chan<- StreamingUpdate) error {

	request := map[string]any{
		"message": userMessage,
		"context": map[string]any{
			"source":    "ponos-bot",
			"timestamp": time.Now().Format(time.RFC3339),
			"user_type": "blockchain_operator",
			"capabilities": []string{
				"network_upgrades",
				"file_operations",
				"system_commands",
				"blockchain_analysis",
			},
		},
	}

	if len(conversationHistory) > 0 {
		request["conversation_history"] = conversationHistory
		request["session_id"] = nil
	}

	requestBody, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/agent/stream", b.agentCoreURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(requestBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", "Ponos-Bot/1.0")

	resp, err := b.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		if resp.StatusCode == http.StatusUnauthorized {
			return fmt.Errorf("authentication failed: invalid or missing API key for agent-core (HTTP 401)")
		}
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return b.processStreamingResponse(resp.Body, updates)
}

func (b *Bot) processStreamingResponse(body io.Reader, updates chan<- StreamingUpdate) error {
	scanner := bufio.NewScanner(body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	var assistantMessageID string

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		dataStr := strings.TrimPrefix(line, "data: ")
		var streamEvent map[string]any

		if err := json.Unmarshal([]byte(dataStr), &streamEvent); err != nil {
			b.logger.Warn("Failed to parse stream event", "error", err)
			continue
		}

		eventType, ok := streamEvent["type"].(string)
		if !ok {
			continue
		}

		message, _ := streamEvent["message"].(string)

		switch eventType {
		case "thinking":
			updates <- StreamingUpdate{Type: "thinking", Message: message}
		case "tool_start":
			if tool, ok := streamEvent["tool"].(string); ok {
				updates <- StreamingUpdate{
					Type:    "tool_start",
					Message: fmt.Sprintf("Running %s", tool),
					Tool:    tool,
				}
			}
		case "tool_result":
			if tool, ok := streamEvent["tool"].(string); ok {
				success, _ := streamEvent["success"].(bool)
				summary, _ := streamEvent["summary"].(string)
				updates <- StreamingUpdate{
					Type:    "tool_result",
					Message: fmt.Sprintf("%s completed", tool),
					Tool:    tool,
					Success: success,
					Summary: summary,
				}
			}
		case "assistant":
			if assistantMessageID == "" {
				assistantMessageID = generateID("msg")
				updates <- StreamingUpdate{
					Type:        "assistant",
					Message:     message,
					MessageID:   assistantMessageID,
					IsAppending: false,
				}
			} else {
				updates <- StreamingUpdate{
					Type:      "stream_append",
					Message:   message,
					MessageID: assistantMessageID,
				}
			}
		case "status":
			updates <- StreamingUpdate{Type: "status", Message: message}
		case "todo_update":
			var todos []TodoItem
			if todosInterface, ok := streamEvent["todos"].([]any); ok {
				for _, todoInterface := range todosInterface {
					if todoMap, ok := todoInterface.(map[string]any); ok {
						todo := TodoItem{
							Content:    b.getStringFromMap(todoMap, "content"),
							Status:     b.getStringFromMap(todoMap, "status"),
							ActiveForm: b.getStringFromMap(todoMap, "activeForm"),
						}
						todos = append(todos, todo)
					}
				}
			}
			toolName, _ := streamEvent["tool_name"].(string)
			updates <- StreamingUpdate{
				Type:     "todo_update",
				Message:  message,
				Todos:    todos,
				ToolName: toolName,
			}
		case "complete":
			sessionID, _ := streamEvent["session_id"].(string)
			checkpointID, _ := streamEvent["checkpoint_id"].(string)
			updates <- StreamingUpdate{
				Type:         "complete",
				Message:      "Operation completed",
				SessionID:    sessionID,
				CheckpointID: checkpointID,
			}
		case "error":
			return fmt.Errorf("agent-core error: %s", message)
		}
	}

	return scanner.Err()
}

func (b *Bot) extractAndUnmarshalJSON(input string, target any) error {
	input = strings.TrimSpace(input)

	// Handle markdown code blocks
	if strings.HasPrefix(input, "```") {
		lines := strings.Split(input, "\n")
		if len(lines) >= 2 {
			if strings.HasPrefix(lines[0], "```") {
				lines = lines[1:]
			}
			if len(lines) > 0 && strings.HasPrefix(lines[len(lines)-1], "```") {
				lines = lines[:len(lines)-1]
			}
			input = strings.Join(lines, "\n")
		}
	}

	// Find first '{' or '['
	startIdx := strings.IndexAny(input, "{[")
	if startIdx == -1 {
		return fmt.Errorf("no JSON start found")
	}

	// Find last '}' or ']'
	endIdx := strings.LastIndexAny(input, "}]")
	if endIdx == -1 || endIdx <= startIdx {
		return fmt.Errorf("no JSON end found")
	}

	jsonStr := input[startIdx : endIdx+1]
	return json.Unmarshal([]byte(jsonStr), target)
}

func (b *Bot) getStringFromMap(m map[string]any, key string) string {
	if val, ok := m[key]; ok && val != nil {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}
