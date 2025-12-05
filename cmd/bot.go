package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/blockops-sh/ponos/config"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
)

type ReleasesWebhookPayload struct {
	EventType    string                 `json:"event_type"`
	Username     string                 `json:"username"`
	Timestamp    string                 `json:"timestamp"`
	Repositories []Repository           `json:"repositories"`
	Releases     map[string]ReleaseInfo `json:"releases"`
}

type Repository struct {
	Owner       string `json:"owner"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	NetworkKey  string `json:"network_key"`
	NetworkName string `json:"network_name"`
	ReleaseTag  string `json:"release_tag"`
	DockerTag   string `json:"docker_tag"`
	ClientType  string `json:"client_type"`
}

type ReleaseInfo struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Body        string `json:"body"`
	HTMLURL     string `json:"html_url"`
	PublishedAt string `json:"published_at"`
	Prerelease  bool   `json:"prerelease"`
	Draft       bool   `json:"draft"`
}

type Bot struct {
	client        *slack.Client
	config        *config.Config
	logger        *slog.Logger
	mcpClient     *GitHubMCPClient
	agent         *NodeOperatorAgent
	githubHandler *GitHubDeployHandler
}

func NewBot(cfg *config.Config, logger *slog.Logger, slackClient *slack.Client, agent *NodeOperatorAgent) *Bot {
	mcpClient := BuildGitHubMCPClient(cfg, logger)
	bot := &Bot{
		client:    slackClient,
		config:    cfg,
		logger:    logger,
		mcpClient: mcpClient,
		agent:     agent,
	}
	bot.githubHandler = NewGitHubDeployHandler(logger, cfg, slackClient, agent, mcpClient)
	return bot
}

type DiagnosticsResponse struct {
	Success bool              `json:"success"`
	Result  DiagnosticsResult `json:"result"`
	Error   string            `json:"error,omitempty"`
}

type DiagnosticsResult struct {
	Service             string                 `json:"service"`
	Namespace           string                 `json:"namespace"`
	ResourceType        string                 `json:"resource_type"`
	Prompt              string                 `json:"prompt"`
	IssueURL            string                 `json:"issue_url"`
	SlackResult         map[string]interface{} `json:"slack_result"`
	Channel             string                 `json:"slack_channel"`
	IssueNumber         int                    `json:"issue_number"`
	LogSnippet          string                 `json:"log_snippet"`
	Summary             string                 `json:"summary"`
	ResourceDescription string                 `json:"resource_description"`
	EventsSummary       string                 `json:"events_summary"`
}

func (b *Bot) handleSlackEvents(w http.ResponseWriter, r *http.Request) {
	if strings.TrimSpace(b.config.SlackSigningKey) == "" {
		b.logger.Error("Slack signing secret is not configured")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	sv, err := slack.NewSecretsVerifier(r.Header, b.config.SlackSigningKey)
	if err != nil {
		b.logger.Error("failed to create secrets verifier", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		b.logger.Error("failed to read request body", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	_, err = sv.Write(body)
	if err != nil {
		b.logger.Error("failed to write body to verifier", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if err := sv.Ensure(); err != nil {
		b.logger.Error("failed to verify request signature", "error", err)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

	var parseOpts []slackevents.Option
	if token := strings.TrimSpace(b.config.SlackVerifyTok); token != "" {
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
		w.WriteHeader(http.StatusInternalServerError)
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

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		b.logger.Error("failed to read request body", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	r.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	sv, err := slack.NewSecretsVerifier(r.Header, b.config.SlackSigningKey)
	if err != nil {
		b.logger.Error("failed to create secrets verifier for slash command", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	if _, err := sv.Write(bodyBytes); err != nil {
		b.logger.Error("failed to write body to verifier for slash command", "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if err := sv.Ensure(); err != nil {
		b.logger.Error("failed to verify slash command request signature", "error", err)
		w.WriteHeader(http.StatusUnauthorized)
		return
	}

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
	case UpdateNetworkCmd:
		response = b.githubHandler.HandleChainUpdate("network", text, userID)
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

	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

	if r.Method == "OPTIONS" {
		w.WriteHeader(http.StatusOK)
		return
	}

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
	blocks := BuildReleaseNotificationBlocks(payload, summary, prURL...)

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

	if b.config.AgentCoreURL == "" {
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

	url := fmt.Sprintf("%s/diagnostics/run", b.config.AgentCoreURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to create diagnostics request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := http.DefaultClient.Do(req)
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

	blocks := b.buildDiagnosticsBlocks(service, diagResp.Result)

	if _, _, err := b.client.PostMessage(
		channelID,
		slack.MsgOptionBlocks(blocks...),
	); err != nil {
		return fmt.Errorf("failed to post diagnostics result to Slack: %w", err)
	}

	return nil
}

func (b *Bot) buildDiagnosticsBlocks(service string, result DiagnosticsResult) []slack.Block {
	var fields []*slack.TextBlockObject

	if result.Namespace != "" {
		fields = append(fields, slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("*Namespace:*\n`%s`", result.Namespace), false, false))
	}
	if result.ResourceType != "" {
		fields = append(fields, slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("*Resource:*\n`%s`", result.ResourceType), false, false))
	}
	if result.IssueURL != "" {
		fields = append(fields, slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("*GitHub issue:*\n<%s>", result.IssueURL), false, false))
	}
	if result.EventsSummary != "" {
		fields = append(fields, slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("*Events:*\n%s", result.EventsSummary), false, false))
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
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("*Summary:*\n%s", result.Summary), false, false),
			nil, nil))
	}

	if result.LogSnippet != "" {
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("*Log snapshot:*\n```\n%s\n```", result.LogSnippet), false, false),
			nil, nil))
	}

	if result.ResourceDescription != "" {
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, "*Resource description was added to the GitHub issue.*", false, false),
			nil, nil))
	}

	if result.Prompt != "" {
		blocks = append(blocks, slack.NewSectionBlock(
			slack.NewTextBlockObject(slack.MarkdownType, fmt.Sprintf("*Prompt:*\n```\n%s\n```", result.Prompt), false, false),
			nil, nil))
	}

	return blocks
}

func (b *Bot) streamAgentResponseToSlack(event *slackevents.AppMentionEvent, userMessage string) {
	channel := event.Channel
	threadTS := event.TimeStamp
	userID := event.User

	ack := fmt.Sprintf(":wave: <@%s> working on \"%s\" …", userID, userMessage)
	b.postThreadedSlackMessage(channel, threadTS, ack)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	updates := make(chan StreamingUpdate, 10)
	errCh := make(chan error, 1)

	go func() {
		defer close(updates)
		errCh <- b.agent.ProcessConversationWithStreaming(ctx, userMessage, updates)
	}()

	var finalResponse string
	var toolSummaries []string

	for update := range updates {
		switch update.Type {
		case "assistant":
			if update.Message != "" {
				finalResponse = update.Message
			}
		case "tool_start":
			if update.Tool != "" {
				status := fmt.Sprintf(":gear: Running *%s*…", formatToolName(update.Tool))
				b.postThreadedSlackMessage(channel, threadTS, status)
			}
		case "tool_result":
			summary := update.Summary
			if summary == "" {
				summary = update.Message
			}
			if summary != "" {
				icon := ":white_check_mark:"
				if !update.Success {
					icon = ":x:"
				}
				toolSummaries = append(toolSummaries, fmt.Sprintf("%s %s", icon, summary))
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

	if len(toolSummaries) > 0 {
		finalResponse = fmt.Sprintf("%s\n\n*Tool summary:*\n%s", finalResponse, strings.Join(toolSummaries, "\n"))
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

	opts := []slack.MsgOption{slack.MsgOptionText(text, false)}
	if threadTS != "" {
		opts = append(opts, slack.MsgOptionTS(threadTS))
	}

	if _, _, err := b.client.PostMessage(channel, opts...); err != nil {
		b.logger.Error("failed to post Slack message", "error", err, "channel", channel)
	}
}

func (b *Bot) postThreadedSlackBlocks(channel, threadTS string, blocks []slack.Block) {
	if len(blocks) == 0 {
		return
	}
	opts := []slack.MsgOption{slack.MsgOptionBlocks(blocks...)}
	if threadTS != "" {
		opts = append(opts, slack.MsgOptionTS(threadTS))
	}
	if _, _, err := b.client.PostMessage(channel, opts...); err != nil {
		b.logger.Error("failed to post Slack blocks", "error", err, "channel", channel)
	}
}

func formatToolName(name string) string {
	if name == "" {
		return "tool"
	}
	return strings.ReplaceAll(name, "_", " ")
}
