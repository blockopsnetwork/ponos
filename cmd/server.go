package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/blockops-sh/ponos/config"
	"github.com/slack-go/slack"
)

func runServer() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		logger.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	// validate github auth
	if err := cfg.ValidateGitHubBotConfig(); err != nil {
		logger.Error("GitHub auth not configured", "error", err)
		logger.Info("Set either GITHUB_TOKEN (PAT) or GITHUB_APP_ID/GITHUB_INSTALL_ID/GITHUB_PEM_KEY (App). See config/config.go.")
		os.Exit(1)
	}

	if strings.TrimSpace(cfg.SlackToken) == "" || strings.TrimSpace(cfg.SlackSigningKey) == "" {
		logger.Error("Slack configuration missing", "has_token", cfg.SlackToken != "", "has_signing_key", cfg.SlackSigningKey != "")
		os.Exit(1)
	}

	if strings.TrimSpace(cfg.AgentCoreURL) == "" {
		logger.Error("nodeoperator api URL is not configured; set AGENT_CORE_URL")
		os.Exit(1)
	}

	if strings.TrimSpace(cfg.GitHubMCPURL) == "" {
		logger.Warn("GITHUB_MCP_URL is empty; GitHub MCP calls may fail", "github_mcp_url", cfg.GitHubMCPURL)
	}

	api := slack.New(cfg.SlackToken)

	// Server mode needs GitHub MCP client for webhook handling and direct MCP calls
	bot := NewBot(cfg, logger, api, true)

	// TODO: For complete separattion of concerns and to ease the pain of users having to setup ngrok for webhook listeners, the whole server logic should be moved to the api backend
	if cfg.EnableReleaseListener {
		webhookHandler := NewWebhookHandler(bot)
		http.HandleFunc("/webhooks/releases", webhookHandler.handleReleasesWebhook)
		logger.Info("release listener enabled", "path", "/webhooks/releases")
	} else {
		logger.Info("release listener disabled; set ENABLE_RELEASE_LISTENER=true to enable")
	}

	http.HandleFunc("/slack/events", bot.handleSlackEvents)
	http.HandleFunc("/slack/command", bot.handleSlashCommand)
	// TODO: not appropiate here, move to api backend
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
