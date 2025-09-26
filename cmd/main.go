package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/blockops-sh/ponos/config"
	"github.com/go-co-op/gocron/v2"
	_ "github.com/go-sql-driver/mysql"
	"github.com/slack-go/slack"
	"github.com/slack-go/slack/slackevents"
	"github.com/uptrace/bun"
	"github.com/uptrace/bun/dialect/mysqldialect"
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
	db            *sql.DB
	config        *config.Config
	logger        *slog.Logger
	githubHandler *GitHubDeployHandler
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	sqldb, err := sql.Open("mysql", cfg.MySQLDSN)
	if err != nil {
		panic(err)
	}

	db := bun.NewDB(sqldb, mysqldialect.New())

	if err := db.Ping(); err != nil {
		logger.Error("failed to ping database", "error", err)
		os.Exit(1)
	}

	scheduler, err := gocron.NewScheduler()
	if err != nil {
		panic(err.Error())
	}

	api := slack.New(cfg.SlackToken)

	if err := createScheduleJobs(scheduler, db, logger, api); err != nil {
		panic(err)
	}

	scheduler.Start()

	bot := &Bot{
		client: api,
		// db:     db,
		config: cfg,
		logger: logger,
	}
	bot.githubHandler = NewGitHubDeployHandler(bot)
	webhookHandler := NewWebhookHandler(bot)

	http.HandleFunc("/slack/events", bot.handleSlackEvents)
	http.HandleFunc("/slack/command", bot.handleSlashCommand)
	http.HandleFunc("/webhooks/releases", webhookHandler.handleReleasesWebhook)

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
	_ = db.Close()
	_ = scheduler.Shutdown()

	logger.Info("shutting down server")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("error during server shutdown", "error", err)
	}
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
	case UpdatePolkadotToLatestCmd:
		response = b.githubHandler.HandleUpdateChain(text, userID)
	case UpdateNetworkCmd:
		response = b.githubHandler.HandleUpdateNetwork(text, userID)
	default:
		response = &SlashCommandResponse{
			ResponseType: "ephemeral",
			Text:         fmt.Sprintf("Sorry, I don't know how to handle the command %s yet.", command),
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)

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
