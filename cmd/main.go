package main

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
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

func main() {
	flag.Parse()

	if len(os.Args) > 1 && os.Args[1] == "server" {
		runServer()
		return
	}

	runAgentTUI()
}

func runServer() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	// Validate GitHub bot configurationac
	if err := cfg.ValidateGitHubBotConfig(); err != nil {
		logger.Error("GitHub bot configuration error", "error", err)
		logger.Info("See config/config.go for setup instructions")
		os.Exit(1)
	}

	api := slack.New(cfg.SlackToken)

	mcpClient := NewGitHubMCPClient(
		"https://api.githubcopilot.com/mcp/",
		cfg.GitHubToken,
		cfg.GitHubAppID,
		cfg.GitHubInstallID,
		cfg.GitHubPEMKey,
		cfg.GitHubBotName,
		logger,
	)

	agent, err := NewNodeOperatorAgent(logger)
	if err != nil {
		logger.Warn("failed to create AI agent", "error", err)
		agent = nil
	}

	bot := &Bot{
		client:    api,
		config:    cfg,
		logger:    logger,
		mcpClient: mcpClient,
		agent:     agent,
	}
	bot.githubHandler = NewGitHubDeployHandler(bot)
	webhookHandler := NewWebhookHandler(bot)

	http.HandleFunc("/slack/events", bot.handleSlackEvents)
	http.HandleFunc("/slack/command", bot.handleSlashCommand)
	http.HandleFunc("/webhooks/releases", webhookHandler.handleReleasesWebhook)
	http.HandleFunc("/mcp/github", bot.handleGitHubMCP)

	srv := &http.Server{
		Addr:         ":" + cfg.Port,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info("starting server", "port", cfg.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("failed to start server", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	<-stop

	logger.Info("shutting down server")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("error during server shutdown", "error", err)
	}
}

func runAgentTUI() {
	// In TUI mode, redirect logs to a file to avoid interfering with the interface
	var logger *slog.Logger
	logFile, err := os.OpenFile("/tmp/ponos-tui.log", os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0666)
	if err != nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	} else {
		logger = slog.New(slog.NewJSONHandler(logFile, nil))
	}
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		fmt.Fprintf(os.Stderr, "Please check your environment variables.\n")
		logger.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	if err := cfg.ValidateGitHubBotConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "GitHub configuration error: %v\n", err)
		fmt.Fprintf(os.Stderr, "\nRequired environment variables:\n")
		fmt.Fprintf(os.Stderr, "  • Either set GITHUB_TOKEN for personal access token authentication\n")
		fmt.Fprintf(os.Stderr, "  • Or set all three for GitHub App authentication:\n")
		fmt.Fprintf(os.Stderr, "    - GITHUB_APP_ID\n")
		fmt.Fprintf(os.Stderr, "    - GITHUB_INSTALL_ID\n")
		fmt.Fprintf(os.Stderr, "    - GITHUB_PEM_KEY\n")
		fmt.Fprintf(os.Stderr, "\nSee config/config.go for setup instructions.\n")
		logger.Error("GitHub bot configuration error", "error", err)
		logger.Info("See config/config.go for setup instructions")
		os.Exit(1)
	}

	api := slack.New(cfg.SlackToken)

	mcpClient := NewGitHubMCPClient(
		"https://api.githubcopilot.com/mcp/",
		cfg.GitHubToken,
		cfg.GitHubAppID,
		cfg.GitHubInstallID,
		cfg.GitHubPEMKey,
		cfg.GitHubBotName,
		logger,
	)

	agent, err := NewNodeOperatorAgent(logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to instantiate agent: %v\n", err)
		fmt.Fprintf(os.Stderr, "Please check your agent-core service configuration (OpenAI API key, etc.)\n")
		logger.Error("failed to instantiate agent", "error", err)
		os.Exit(1)
	}

	bot := &Bot{
		client:    api,
		config:    cfg,
		logger:    logger,
		mcpClient: mcpClient,
		agent:     agent,
	}
	bot.githubHandler = NewGitHubDeployHandler(bot)

	tui := NewPonosAgentTUI(bot, logger)
	tui.Start()
}

func (b *Bot) handleSlackEvents(w http.ResponseWriter, r *http.Request) {
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

	eventsAPIEvent, err := slackevents.ParseEvent(body)
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
	text := fmt.Sprintf("Hello <@%s>! I received your message: %s", event.User, event.Text)
	if _, _, err := b.client.PostMessage(event.Channel, slack.MsgOptionText(text, false)); err != nil {
		b.logger.Error("error sending app mention response", "error", err, "user", event.User)
	}
}

func (b *Bot) handleMessage(event *slackevents.MessageEvent) {
	if event.BotID != "" {
		errorBlock := slack.NewTextBlockObject("mrkdwn",
			":robot_face: *Bot-to-Bot Communication Not Supported*\n"+
				"Sorry, but I don't talk to other bots yet. Let's wait for AGI! :wink:",
			false, false)
		msgBlock := slack.NewSectionBlock(errorBlock, nil, nil)

		if _, _, err := b.client.PostMessage(
			event.Channel,
			slack.MsgOptionBlocks(msgBlock),
		); err != nil {
			b.logger.Error("error sending bot-to-bot error message",
				"error", err,
				"bot_id", event.BotID,
				"channel", event.Channel)
		}
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
	case "/hello":
		response = &SlashCommandResponse{
			ResponseType: "in_channel",
			Text:         fmt.Sprintf("Hello <@%s>! You said: %s", userID, text),
		}
	case DeployDashboardCmd, DeployAPICmd, DeployProxyCmd:
		response = b.githubHandler.HandleDeploy(command, text, userID, channelID)
	case UpdateNetworkCmd:
		response = b.githubHandler.HandleChainUpdate("network", text, userID)
	default:
		response = &SlashCommandResponse{
			ResponseType: "ephemeral",
			Text:         fmt.Sprintf("Sorry, I don't know how to handle the command %s yet.", command),
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
