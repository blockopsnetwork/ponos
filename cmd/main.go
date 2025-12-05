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

func main() {
	flag.Parse()

	if len(os.Args) > 1 && os.Args[1] == "server" {
		runServer()
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
		fmt.Fprintf(os.Stderr, "Set either GITHUB_TOKEN (PAT) or GITHUB_APP_ID/GITHUB_INSTALL_ID/GITHUB_PEM_KEY (Github Bot auth).\n")
		logger.Error("GitHub auth not configured", "error", err)
		os.Exit(1)
	}

	if strings.TrimSpace(cfg.SlackToken) == "" || strings.TrimSpace(cfg.SlackSigningKey) == "" {
		fmt.Fprintf(os.Stderr, "Slack configuration missing: SLACK_TOKEN and SLACK_SIGNING_SECRET are required.\n")
		logger.Error("Slack configuration missing", "has_token", cfg.SlackToken != "", "has_signing_key", cfg.SlackSigningKey != "")
		os.Exit(1)
	}

	if strings.TrimSpace(cfg.AgentCoreURL) == "" {
		fmt.Fprintf(os.Stderr, "nodeoperator api URL is not configured. Set AGENT_CORE_URL.\n")
		logger.Error("nodeoperator api URL is not configured")
		os.Exit(1)
	}

	if strings.TrimSpace(cfg.GitHubMCPURL) == "" {
		logger.Warn("GITHUB_MCP_URL is empty; GitHub MCP calls may fail", "github_mcp_url", cfg.GitHubMCPURL)
	}

	api := slack.New(cfg.SlackToken)

	agent, err := NewNodeOperatorAgent(logger)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to instantiate agent: %v\n", err)
		fmt.Fprintf(os.Stderr, "Please check your nodeoperator api service configuration (API Key, URL, etc.)\n")
		logger.Error("failed to instantiate nodeoperator api client", "error", err)
		os.Exit(1)
	}

	bot := NewBot(cfg, logger, api, agent)

	tui := NewPonosAgentTUI(bot, logger)
	tui.Start()
}
