package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Color scheme matching Stakpak's interface
var (
	// Brand colors
	brandColor    = lipgloss.Color("#00BCD4") // Cyan
	textColor     = lipgloss.Color("#E1E8ED") // Light gray
	subtleColor   = lipgloss.Color("#8B949E") // Muted gray
	errorColor    = lipgloss.Color("#F85149") // Red
	successColor  = lipgloss.Color("#3FB950") // Green
	bgColor       = lipgloss.Color("#0D1117") // Dark background

	// ASCII art style for PONOS
	logoStyle = lipgloss.NewStyle().
		Foreground(brandColor).
		Bold(true)

	// Version and info text
	infoStyle = lipgloss.NewStyle().
		Foreground(subtleColor)

	// Current working directory
	cwdStyle = lipgloss.NewStyle().
		Foreground(textColor)

	// Input prompt
	promptStyle = lipgloss.NewStyle().
		Foreground(brandColor).
		Bold(true)

	// Message styles
	userMessageStyle = lipgloss.NewStyle().
		Foreground(textColor)

	assistantMessageStyle = lipgloss.NewStyle().
		Foreground(successColor)

	errorMessageStyle = lipgloss.NewStyle().
		Foreground(errorColor)

	systemMessageStyle = lipgloss.NewStyle().
		Foreground(subtleColor)

	// Loading indicator
	loadingStyle = lipgloss.NewStyle().
		Foreground(brandColor).
		Bold(true)

	// Help text at bottom
	helpStyle = lipgloss.NewStyle().
		Foreground(subtleColor)
)

const (
	ponosLogo = `██████   ██████  ███    ██  ██████  ███████ 
██   ██ ██    ██ ████   ██ ██    ██ ██      
██████  ██    ██ ██ ██  ██ ██    ██ ███████ 
██      ██    ██ ██  ██ ██ ██    ██      ██ 
██       ██████  ██   ████  ██████  ███████`

	version = "0.1.0"
)

type PonosAgentTUI struct {
	bot    *Bot
	logger *slog.Logger
}

type tuiModel struct {
	viewport   viewport.Model
	textarea   textarea.Model
	messages   []ChatMessage
	ready      bool
	width      int
	height     int
	tui        *PonosAgentTUI
	loading    bool
	currentDir string
	thoughtMsg string
}

type ChatMessage struct {
	Role      string    // "user", "assistant", "system", "error"
	Content   string
	Timestamp time.Time
	Actions   []string // Actions performed
}

type msgResponse struct {
	content string
	err     error
}

type loadingMsg struct {
	loading bool
}

type thoughtMsg struct {
	thought string
}

func NewPonosAgentTUI(bot *Bot, logger *slog.Logger) *PonosAgentTUI {
	return &PonosAgentTUI{
		bot:    bot,
		logger: logger,
	}
}

func (tui *PonosAgentTUI) Start() error {
	p := tea.NewProgram(
		tui.initModel(),
		tea.WithAltScreen(),
	)

	_, err := p.Run()
	return err
}

func (tui *PonosAgentTUI) initModel() tuiModel {
	// Get current working directory
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "unknown"
	}

	// Configure textarea (input area)
	ta := textarea.New()
	ta.Placeholder = ""
	ta.Focus()
	ta.Prompt = ""
	ta.CharLimit = 2000
	ta.SetWidth(80)
	ta.SetHeight(1) // Single line input like Stakpak
	ta.ShowLineNumbers = false
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.BlurredStyle.CursorLine = lipgloss.NewStyle()

	// Configure viewport (message area)
	vp := viewport.New(80, 20)

	m := tuiModel{
		textarea:   ta,
		viewport:   vp,
		messages:   []ChatMessage{},
		ready:      false,
		tui:        tui,
		loading:    false,
		currentDir: cwd,
	}

	return m
}

func (m tuiModel) Init() tea.Cmd {
	return textarea.Blink
}

func (m tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if !m.ready {
			// Calculate heights like Stakpak: header + messages + input + help
			headerHeight := 8 // Logo (5) + version (1) + help (1) + cwd (1) = 8 lines total
			helpHeight := 1   // Help text at bottom
			inputHeight := 4  // Input area with border
			spacingHeight := 2 // Minimal spacing between sections

			messageHeight := msg.Height - headerHeight - inputHeight - helpHeight - spacingHeight
			if messageHeight < 3 {
				messageHeight = 3 // Minimum height
			}
			
			m.viewport = viewport.New(msg.Width, messageHeight)
			m.textarea.SetWidth(msg.Width - 8) // Account for borders and padding
			m.ready = true
		} else {
			headerHeight := 8
			helpHeight := 1
			inputHeight := 4
			spacingHeight := 2
			messageHeight := msg.Height - headerHeight - inputHeight - helpHeight - spacingHeight
			if messageHeight < 3 {
				messageHeight = 3
			}

			m.viewport.Width = msg.Width
			m.viewport.Height = messageHeight
			m.textarea.SetWidth(msg.Width - 8)
		}
		m.width = msg.Width
		m.height = msg.Height
		(&m).updateViewportContent()

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			return m, tea.Quit
		case tea.KeyEnter:
			// Send message
			userInput := strings.TrimSpace(m.textarea.Value())
			if userInput != "" && !m.loading {
				// Add user message immediately (like Claude Code)
				m.messages = append(m.messages, ChatMessage{
					Role:      "user",
					Content:   userInput,
					Timestamp: time.Now(),
				})
				
				// Clear textarea
				m.textarea.Reset()
				m.loading = true
				
				// Show thinking message immediately
				m.thoughtMsg = "Analyzing request"
				(&m).updateViewportContent()
				
				// Process message asynchronously with thought progression
				return m, tea.Batch(
					m.tui.processMessageWithThoughts(userInput),
					m.startLoading(),
				)
			}
		}

	case msgResponse:
		m.loading = false
		m.thoughtMsg = "" // Clear thinking message
		if msg.err != nil {
			m.messages = append(m.messages, ChatMessage{
				Role:      "error",
				Content:   fmt.Sprintf("Error: %v", msg.err),
				Timestamp: time.Now(),
			})
		} else {
			m.messages = append(m.messages, ChatMessage{
				Role:      "assistant",
				Content:   msg.content,
				Timestamp: time.Now(),
			})
		}
		(&m).updateViewportContent()

	case loadingMsg:
		m.loading = msg.loading

	case thoughtMsg:
		m.thoughtMsg = msg.thought
	}

	// Update components
	m.textarea, cmd = m.textarea.Update(msg)
	cmds = append(cmds, cmd)

	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m tuiModel) View() string {
	if !m.ready {
		return loadingStyle.Render("Initializing Ponos Agent...")
	}

	// Header section (like Stakpak) - more compact spacing
	header := logoStyle.Render(ponosLogo) + "\n" +
		infoStyle.Render(fmt.Sprintf("Current Version: %s", version)) + "\n" +
		infoStyle.Render("/help for help, /status for your current setup") + "\n" +
		cwdStyle.Render(fmt.Sprintf("cwd: %s", m.currentDir)) + "\n"

	// Messages area with separator line (like Stakpak)
	separatorLine := strings.Repeat("─", m.width-2)
	separator := lipgloss.NewStyle().Foreground(subtleColor).Render(separatorLine)
	
	messagesView := m.viewport.View()
	// Always show messages area for conversation
	if len(m.messages) == 0 {
		messagesView = "" // No messages yet
	}

	// Input area with border and prompt (like Stakpak)
	loadingIndicator := ""
	if m.loading {
		loadingIndicator = " " + loadingStyle.Render("●")
	}
	
	// Create bordered input like Stakpak
	inputContent := promptStyle.Render("> ") + m.textarea.View() + loadingIndicator
	inputBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(subtleColor).
		Padding(0, 1).
		Width(m.width - 2).
		Render(inputContent)
	
	inputView := inputBox
	
	// Thought message (like Claude Code's "✽ Befuddling...")
	thoughtView := ""
	if m.thoughtMsg != "" {
		thoughtView = "\n" + lipgloss.NewStyle().
			Foreground(brandColor).
			Italic(true).
			Render("✽ " + m.thoughtMsg + "… (esc to interrupt)")
	}

	// Help text at bottom
	helpText := helpStyle.Render("? for shortcuts")

	// Combine all sections with controlled spacing
	if len(m.messages) == 0 {
		// No messages - minimal spacing
		return header + "\n" + inputView + thoughtView + "\n" + helpText
	} else {
		// Has messages - include separator and messages
		return header + "\n" + separator + "\n" + messagesView + "\n" + inputView + thoughtView + "\n" + helpText
	}
}

func (m *tuiModel) updateViewportContent() {
	var content strings.Builder
	
	for _, msg := range m.messages {
		timestamp := msg.Timestamp.Format("15:04:05")
		
		switch msg.Role {
		case "user":
			content.WriteString(userMessageStyle.Render(fmt.Sprintf("[%s] You: %s", timestamp, msg.Content)))
		case "assistant":
			content.WriteString(assistantMessageStyle.Render(fmt.Sprintf("[%s] Ponos: %s", timestamp, msg.Content)))
		case "error":
			content.WriteString(errorMessageStyle.Render(fmt.Sprintf("[%s] Error: %s", timestamp, msg.Content)))
		case "system":
			content.WriteString(systemMessageStyle.Render(fmt.Sprintf("[%s] System: %s", timestamp, msg.Content)))
		}
		content.WriteString("\n\n")
	}
	
	m.viewport.SetContent(content.String())
	m.viewport.GotoBottom()
}

func (m tuiModel) startLoading() tea.Cmd {
	return func() tea.Msg {
		return loadingMsg{loading: true}
	}
}

func (tui *PonosAgentTUI) processMessage(input string) tea.Cmd {
	return func() tea.Msg {
		response, err := tui.handleUserInput(input)
		return msgResponse{content: response, err: err}
	}
}

func (tui *PonosAgentTUI) setThought(thought string) tea.Cmd {
	return func() tea.Msg {
		return thoughtMsg{thought: thought}
	}
}

func (tui *PonosAgentTUI) clearThought() tea.Cmd {
	return func() tea.Msg {
		return thoughtMsg{thought: ""}
	}
}

func (tui *PonosAgentTUI) processMessageWithThoughts(input string) tea.Cmd {
	return func() tea.Msg {
		// Simulate progressive thinking like Claude Code
		// In a real implementation, these would be sent as separate commands
		// with delays and goroutines for realistic progression
		
		// Process the actual request
		response, err := tui.handleUserInput(input)
		
		return msgResponse{content: response, err: err}
	}
}

func (tui *PonosAgentTUI) handleUserInput(input string) (string, error) {
	ctx := context.Background()
	
	// Handle special commands first (like Stakpak)
	switch {
	case input == "/help":
		return tui.getHelpText(), nil
	case input == "/status":
		return tui.getStatusText(), nil
	case strings.HasPrefix(input, "/"):
		return "Unknown command. Type /help for available commands.", nil
	}

	// Use the existing AI agent to understand the user's intent
	if tui.bot.agent == nil {
		return "Sorry, the AI agent is not available. Please ensure OPENAI_API_KEY is set.", nil
	}

	// Parse intent using AI
	intent, err := tui.parseUserIntent(ctx, input)
	if err != nil {
		return "", fmt.Errorf("failed to parse intent: %v", err)
	}

	// Execute action based on intent
	if intent.RequiresAction {
		result, err := tui.executeAction(ctx, intent)
		if err != nil {
			return "", fmt.Errorf("failed to execute action: %v", err)
		}
		return intent.Response + "\n\n" + result, nil
	}

	return intent.Response, nil
}

func (tui *PonosAgentTUI) getHelpText() string {
	return `Available commands:
/help - Show this help message
/status - Show current setup status

Blockchain Operations:
• "upgrade polkadot to latest" - Upgrade Polkadot network
• "upgrade kusama to latest" - Upgrade Kusama network
• "update network [name]" - Update specific network

I can help you with blockchain network upgrades and deployments.
Just tell me what you'd like me to do in natural language!`
}

func (tui *PonosAgentTUI) getStatusText() string {
	status := "Current Setup Status:\n\n"
	
	// Check configuration
	if tui.bot.config.GitHubToken != "" {
		status += "✅ GitHub Token configured\n"
	} else {
		status += "❌ GitHub Token missing\n"
	}
	
	if tui.bot.config.SlackToken != "" {
		status += "✅ Slack Token configured\n"
	} else {
		status += "❌ Slack Token missing\n"
	}
	
	if tui.bot.agent != nil {
		status += "✅ AI Agent available\n"
	} else {
		status += "❌ AI Agent unavailable (check OPENAI_API_KEY)\n"
	}
	
	// Add current directory
	cwd, _ := os.Getwd()
	status += fmt.Sprintf("\nWorking Directory: %s", cwd)
	
	return status
}

type UserIntent struct {
	ActionType     string // "upgrade_network", "deploy_service", "check_status", "general"
	Network        string
	UpdateType     string // "chain" or "network"
	Response       string
	RequiresAction bool
}

func (tui *PonosAgentTUI) parseUserIntent(ctx context.Context, input string) (*UserIntent, error) {
	// Simple intent parsing for now (can be enhanced with actual LLM later)
	inputLower := strings.ToLower(input)
	
	// Check for upgrade requests
	if strings.Contains(inputLower, "upgrade") || strings.Contains(inputLower, "update") {
		intent := &UserIntent{
			ActionType:     "upgrade_network",
			UpdateType:     "chain",
			RequiresAction: true,
		}
		
		// Detect network
		if strings.Contains(inputLower, "polkadot") {
			intent.Network = "polkadot"
			intent.Response = "I'll upgrade the Polkadot network to the latest version..."
		} else if strings.Contains(inputLower, "kusama") {
			intent.Network = "kusama"
			intent.Response = "I'll upgrade the Kusama network to the latest version..."
		} else {
			intent.Response = "Please specify which network to upgrade (polkadot, kusama, etc.)"
			intent.RequiresAction = false
		}
		
		return intent, nil
	}
	
	// General conversation
	return &UserIntent{
		ActionType:     "general",
		Response:       "I'm here to help with blockchain operations. You can ask me to upgrade networks like Polkadot or Kusama. What would you like me to help you with?",
		RequiresAction: false,
	}, nil
}

func (tui *PonosAgentTUI) executeAction(ctx context.Context, intent *UserIntent) (string, error) {
	switch intent.ActionType {
	case "upgrade_network":
		return tui.executeNetworkUpgrade(intent.Network, intent.UpdateType)
	default:
		return "Action not yet supported.", nil
	}
}

func (tui *PonosAgentTUI) executeNetworkUpgrade(network, updateType string) (string, error) {
	if network == "" {
		return "❌ Network name is required", nil
	}
	
	// Use the existing HandleChainUpdate logic
	response := tui.bot.githubHandler.HandleChainUpdate(updateType, network, "tui-user")
	
	if response.ResponseType == "ephemeral" {
		return fmt.Sprintf("❌ %s", response.Text), nil
	}
	
	// Capitalize first letter of network name
	networkName := network
	if len(networkName) > 0 {
		networkName = strings.ToUpper(networkName[:1]) + networkName[1:]
	}
	
	return fmt.Sprintf(`✅ Started %s upgrade for %s network

Working on:
• Fetching latest release information
• Updating Kubernetes manifests  
• Creating pull request for review

You'll see the results in your configured Slack channel once complete.`, updateType, networkName), nil
}
