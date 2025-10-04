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

var (
	brandColor    = lipgloss.Color("#9333EA") // Purple
	textColor     = lipgloss.Color("#E1E8ED") // Light gray
	subtleColor   = lipgloss.Color("#8B949E") // Muted gray
	errorColor    = lipgloss.Color("#F85149") // Red
	successColor  = lipgloss.Color("#3FB950") // Green


	logoStyle = lipgloss.NewStyle().
		Foreground(brandColor).
		Bold(true)

	infoStyle = lipgloss.NewStyle().
		Foreground(subtleColor)

	cwdStyle = lipgloss.NewStyle().
		Foreground(textColor)

	promptStyle = lipgloss.NewStyle().
		Foreground(brandColor).
		Bold(true)

	userMessageStyle = lipgloss.NewStyle().
		Foreground(textColor)

	assistantMessageStyle = lipgloss.NewStyle().
		Foreground(successColor)

	errorMessageStyle = lipgloss.NewStyle().
		Foreground(errorColor)

	systemMessageStyle = lipgloss.NewStyle().
		Foreground(subtleColor)

	loadingStyle = lipgloss.NewStyle().
		Foreground(brandColor).
		Bold(true)

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
	tui           *PonosAgentTUI
	loading       bool
	currentDir    string
	thoughtMsg    string
	spinnerFrame  int
	loadingText   string
	program       *tea.Program
	cancelThinking context.CancelFunc
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

type spinnerTick struct{}

type progressUpdate struct {
	thought     string
	loadingText string
}

func NewPonosAgentTUI(bot *Bot, logger *slog.Logger) *PonosAgentTUI {
	return &PonosAgentTUI{
		bot:    bot,
		logger: logger,
	}
}

func (tui *PonosAgentTUI) Start() error {
	model := tui.initModel()
	p := tea.NewProgram(
		&model,  // Pass pointer to model so program reference persists
		tea.WithAltScreen(),
	)
	
	// Store program reference for sending async updates
	model.program = p

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

func (m *tuiModel) Init() tea.Cmd {
	return textarea.Blink
}

func (m *tuiModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd
	var cmd tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if !m.ready {
			// Calculate heights exactly like Stakpak: header + messages + loading + input + help
			headerHeight := 8  // Logo (5) + version (1) + help (1) + cwd (1) = 8 lines total
			loadingHeight := 1 // Dedicated loading line like Stakpak
			helpHeight := 1    // Help text at bottom
			inputHeight := 4   // Input area with border
			spacingHeight := 2 // Minimal spacing between sections

			messageHeight := msg.Height - headerHeight - loadingHeight - inputHeight - helpHeight - spacingHeight
			if messageHeight < 3 {
				messageHeight = 3 // Minimum height
			}
			
			m.viewport = viewport.New(msg.Width, messageHeight)
			m.textarea.SetWidth(msg.Width - 8) // Account for borders and padding
			m.ready = true
		} else {
			headerHeight := 8
			loadingHeight := 1
			helpHeight := 1
			inputHeight := 4
			spacingHeight := 2
			messageHeight := msg.Height - headerHeight - loadingHeight - inputHeight - helpHeight - spacingHeight
			if messageHeight < 3 {
				messageHeight = 3
			}

			m.viewport.Width = msg.Width
			m.viewport.Height = messageHeight
			m.textarea.SetWidth(msg.Width - 8)
		}
		m.width = msg.Width
		m.height = msg.Height
		m.updateViewportContent()

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyEsc:
			// Handle ESC like Stakpak - interrupt current operation if loading
			if m.loading {
				m.loading = false
				m.thoughtMsg = ""
				m.loadingText = ""
				
				// Cancel the thinking goroutine
				if m.cancelThinking != nil {
					m.cancelThinking()
					m.cancelThinking = nil
				}
				
				// Add interruption message
				m.messages = append(m.messages, ChatMessage{
					Role:      "system",
					Content:   "Operation interrupted by user",
					Timestamp: time.Now(),
				})
				m.updateViewportContent()
				return m, nil
			}
			// If not loading, ESC does nothing (like Stakpak)
		case tea.KeyEnter:
			// Send message
			userInput := strings.TrimSpace(m.textarea.Value())
			if userInput != "" && !m.loading {
				// Add user message immediately (like Stakpak does)
				m.messages = append(m.messages, ChatMessage{
					Role:      "user",
					Content:   userInput,
					Timestamp: time.Now(),
				})
				
				// Clear textarea and update UI immediately
				m.textarea.Reset()
				m.loading = true
				m.thoughtMsg = "Thinking"
				m.loadingText = "Ponosing..."
				m.updateViewportContent()
				
				// Start async processing like Stakpak (non-blocking)
				ctx, cancel := context.WithCancel(context.Background())
				m.cancelThinking = cancel
				
				// Pass the program reference directly to avoid nil pointer issues
				program := m.program
				go func() {
					m.tui.logger.Info("Starting async processing", "input", userInput)
					
					// Send initial thinking state
					if program != nil {
						m.tui.logger.Info("Sending progress update")
						program.Send(progressUpdate{
							thought:     "Understanding request",
							loadingText: "Thinking...",
						})
					}
					
					// Process the request (non-blocking)
					m.tui.logger.Info("About to call handleUserInput")
					response, err := m.tui.handleUserInput(userInput)
					m.tui.logger.Info("handleUserInput completed", "response_length", len(response), "error", err)
					
					// Check if cancelled
					select {
					case <-ctx.Done():
						m.tui.logger.Info("Processing cancelled")
						return
					default:
					}
					
					// Send final result
					if program != nil {
						m.tui.logger.Info("Sending final response")
						program.Send(msgResponse{content: response, err: err})
					} else {
						m.tui.logger.Error("program is nil, cannot send response")
					}
				}()
				
				// Return immediately with loading state
				return m, tea.Batch(
					m.startLoading(),
					m.tui.tickSpinner(),
				)
			}
		}

	case msgResponse:
		m.loading = false
		m.thoughtMsg = ""  // Clear thinking message
		m.loadingText = "" // Clear loading text
		m.cancelThinking = nil // Clear cancellation function
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
		m.updateViewportContent()

	case loadingMsg:
		m.loading = msg.loading

	case thoughtMsg:
		m.thoughtMsg = msg.thought

	case progressUpdate:
		m.thoughtMsg = msg.thought
		m.loadingText = msg.loadingText

	case spinnerTick:
		if m.loading {
			m.spinnerFrame++
			// Keep the spinner going while loading
			return m, m.tui.tickSpinner()
		}
	}

	// Update components
	m.textarea, cmd = m.textarea.Update(msg)
	cmds = append(cmds, cmd)

	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m *tuiModel) View() string {
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
	
	// Loading indicator area (dedicated line like Stakpak)
	loadingView := m.renderLoadingLine()
	
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

	// Combine all sections with Stakpak-style layout
	if len(m.messages) == 0 {
		// No messages - show header + loading + input + help
		return header + "\n" + loadingView + "\n" + inputView + thoughtView + "\n" + helpText
	} else {
		// Has messages - show header + separator + messages + loading + input + help
		return header + "\n" + separator + "\n" + messagesView + "\n" + loadingView + "\n" + inputView + thoughtView + "\n" + helpText
	}
}

func (m *tuiModel) updateViewportContent() {
	var content strings.Builder
	
	// Calculate available width for message content (viewport width minus padding)
	availableWidth := m.viewport.Width - 4 // Account for padding and borders
	if availableWidth < 20 {
		availableWidth = 20 // Minimum width
	}
	
	for _, msg := range m.messages {
		timestamp := msg.Timestamp.Format("15:04:05")
		
		var prefix, text string
		var style lipgloss.Style
		
		switch msg.Role {
		case "user":
			prefix = fmt.Sprintf("[%s] You: ", timestamp)
			text = msg.Content
			style = userMessageStyle
		case "assistant":
			prefix = fmt.Sprintf("[%s] Ponos: ", timestamp)
			text = msg.Content
			style = assistantMessageStyle
		case "error":
			prefix = fmt.Sprintf("[%s] Error: ", timestamp)
			text = msg.Content
			style = errorMessageStyle
		case "system":
			prefix = fmt.Sprintf("[%s] System: ", timestamp)
			text = msg.Content
			style = systemMessageStyle
		}
		
		// Wrap the message content properly
		wrappedContent := wrapText(prefix+text, availableWidth)
		content.WriteString(style.Render(wrappedContent))
		content.WriteString("\n\n")
	}
	
	m.viewport.SetContent(content.String())
	m.viewport.GotoBottom()
}

// wrapText wraps text to the specified width
func wrapText(text string, width int) string {
	if width <= 0 {
		return text
	}
	
	words := strings.Fields(text)
	if len(words) == 0 {
		return text
	}
	
	var lines []string
	var currentLine string
	
	for _, word := range words {
		// If this is the first word on the line
		if currentLine == "" {
			currentLine = word
		} else {
			// Check if adding this word would exceed the width
			testLine := currentLine + " " + word
			if len(testLine) <= width {
				currentLine = testLine
			} else {
				// Start a new line
				lines = append(lines, currentLine)
				currentLine = word
			}
		}
	}
	
	// Add the last line
	if currentLine != "" {
		lines = append(lines, currentLine)
	}
	
	return strings.Join(lines, "\n")
}

func (m tuiModel) startLoading() tea.Cmd {
	return func() tea.Msg {
		return loadingMsg{loading: true}
	}
}

func (m tuiModel) renderLoadingLine() string {
	if !m.loading {
		return "" // Empty line when not loading
	}
	
	// Stakpak-style spinner characters
	spinnerChars := []string{"▄▀", "▐▌", "▀▄", "▐▌"}
	spinner := spinnerChars[m.spinnerFrame%len(spinnerChars)]
	
	// Loading text like "Ponosing..." (mimicking "Stakpaking...")
	loadingText := "Ponosing..."
	if m.loadingText != "" {
		loadingText = m.loadingText
	}
	
	// Render with brand color like Stakpak
	return lipgloss.NewStyle().
		Foreground(brandColor).
		Bold(true).
		Render(fmt.Sprintf("%s %s", spinner, loadingText))
}

// Add spinner animation timer
func (tui *PonosAgentTUI) tickSpinner() tea.Cmd {
	return tea.Tick(time.Millisecond*200, func(t time.Time) tea.Msg {
		return spinnerTick{}
	})
}




func (tui *PonosAgentTUI) handleUserInput(input string) (string, error) {
	ctx := context.Background()
	
	tui.logger.Info("Processing user input", "input", input)
	
	// Handle special commands first (like Stakpak)
	switch {
	case input == "/help":
		tui.logger.Info("Handling help command")
		return tui.getHelpText(), nil
	case input == "/status":
		tui.logger.Info("Handling status command")
		return tui.getStatusText(), nil
	case strings.HasPrefix(input, "/"):
		tui.logger.Info("Unknown slash command", "input", input)
		return "Unknown command. Type /help for available commands.", nil
	}

	// Check if AI agent is available
	if tui.bot.agent == nil {
		tui.logger.Error("AI agent not available")
		return "Sorry, the AI agent is not available. Please ensure OPENAI_API_KEY is set.", nil
	}

	tui.logger.Info("AI agent available, processing with AI")

	// Quick test to verify OPENAI_API_KEY is working
	if input == "test" {
		tui.logger.Info("Test mode - simple AI call")
		return "Test response: AI agent is working! Try asking 'hello, what can you do?'", nil
	}

	// TEMPORARY: Let's test with a simple prompt to see if the issue is with our complex conversation prompt
	if strings.ToLower(input) == "simple" {
		tui.logger.Info("Simple test mode")
		ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		
		response, err := tui.bot.agent.llm.Call(ctx, "Say 'Hello from Ponos!' in a friendly way.")
		if err != nil {
			return fmt.Sprintf("Simple test failed: %v", err), nil
		}
		return fmt.Sprintf("Simple test success: %s", response), nil
	}

	// TEMPORARY: For debugging, let's just return a simple response to test the TUI flow
	if strings.ToLower(input) == "bypass" {
		return "This is a bypass response to test TUI flow", nil
	}

	// Call AI agent and handle errors conversationally like Stakpak
	return tui.handleConversation(ctx, input)
}

// handleConversation processes general conversation with the AI agent
func (tui *PonosAgentTUI) handleConversation(ctx context.Context, input string) (string, error) {
	tui.logger.Info("Starting AI conversation", "input", input)
	
	// Add timeout to prevent hanging
	ctxWithTimeout, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	
	tui.logger.Info("Calling AI agent ProcessConversation")
	response, err := tui.bot.agent.ProcessConversation(ctxWithTimeout, input)
	
	if err != nil {
		tui.logger.Error("AI conversation failed", "error", err, "input", input)
		return fmt.Sprintf("I'm having trouble thinking right now. Error: %v", err), nil
	}
	
	if response.Error != nil {
		tui.logger.Error("AI agent error", "error", response.Error, "input", input)
		return fmt.Sprintf("AI agent error: %v", response.Error), nil
	}
	
	tui.logger.Info("AI conversation successful", "response_length", len(response.Content))
	return response.Content, nil
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
