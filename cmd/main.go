package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"

	"github.com/blockops-sh/ponos/config"
	"github.com/slack-go/slack"
)

func main() {
	flag.Parse()

	if len(os.Args) > 1 && os.Args[1] == "server" {
		runServer()
		return
	}

	runAgentTUI()
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
	bot.githubHandler = NewGitHubDeployHandler(logger, cfg, api, agent, mcpClient)

	tui := NewPonosAgentTUI(bot, logger)
	tui.Start()
}
