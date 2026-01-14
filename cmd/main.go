package main

import (
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/blockops-sh/ponos/config"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/slack-go/slack"
)

var version = "dev"

func main() {
	flag.Parse()

	if len(os.Args) > 1 && os.Args[1] == "server" {
		runServer()
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "upgrade" {
		if err := runUpgrade(os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "Upgrade failed: %v\n", err)
			os.Exit(1)
		}
		return
	}

	runAgentTUI()
}

func runAgentTUI() {
	logPath := filepath.Join(os.TempDir(), "ponos-tui.log")
	if env := strings.TrimSpace(os.Getenv("PONOS_TUI_LOG_PATH")); env != "" {
		logPath = env
	}
	logDir := filepath.Dir(logPath)
	if logDir != "" && logDir != "." {
		if err := os.MkdirAll(logDir, 0o755); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to create log directory %s: %v\n", logDir, err)
		}
	}

	var logWriter io.Writer
	logFile, err := tea.LogToFile(logPath, "ponos-tui ")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: unable to create TUI log file at %s: %v\n", logPath, err)
		fmt.Fprintf(os.Stderr, "Set PONOS_TUI_LOG_PATH to a writable location. Logs will be discarded otherwise\n")
		logWriter = io.Discard
	} else {
		logWriter = logFile
	}

	logger := slog.New(slog.NewJSONHandler(logWriter, nil))
	slog.SetDefault(logger)

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		fmt.Fprintf(os.Stderr, "Please check your environment variables.\n")
		logger.Error("failed to load configuration", "error", err)
		os.Exit(1)
	}

	if err := cfg.ValidateGitHubBotConfig(); err != nil {
		fmt.Fprintf(os.Stderr, "GitHub auth not configured: %v\n", err)
		fmt.Fprintf(os.Stderr, "Set either github_token or github_app_id/github_install_id/github_pem_key in ponos.yml\n")
		logger.Error("GitHub auth not configured", "error", err)
		os.Exit(1)
	}

	if strings.TrimSpace(cfg.Integrations.Slack.Token) == "" || strings.TrimSpace(cfg.Integrations.Slack.SigningKey) == "" {
		fmt.Fprintf(os.Stderr, "Slack configuration missing: slack_token and slack_signing_key are required in ponos.yml\n")
		logger.Error("Slack configuration missing", "has_token", cfg.Integrations.Slack.Token != "", "has_signing_key", cfg.Integrations.Slack.SigningKey != "")
		os.Exit(1)
	}

	if strings.TrimSpace(cfg.APIEndpoint) == "" {
		fmt.Fprintf(os.Stderr, "api_endpoint is not configured in ponos.yml\n")
		logger.Error("api_endpoint is not configured")
		os.Exit(1)
	}

	api := slack.New(cfg.Integrations.Slack.Token)
	bot := NewBot(cfg, logger, api, false)
	tui := NewPonosAgentTUI(bot, logger)
	tui.Start()
}
