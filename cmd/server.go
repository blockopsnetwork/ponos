package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
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

	// Validate GitHub bot configuration
	if err := cfg.ValidateGitHubBotConfig(); err != nil {
		logger.Error("GitHub bot configuration error", "error", err)
		logger.Info("See config/config.go for setup instructions")
		os.Exit(1)
	}

	api := slack.New(cfg.SlackToken)

	mcpClient := NewGitHubMCPClient(
		cfg.GitHubMCPURL,
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
	bot.githubHandler = NewGitHubDeployHandler(logger, cfg, api, agent, mcpClient)
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
