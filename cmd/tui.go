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
		&model,  
		tea.WithAltScreen(),
	)
	
	model.program = p

	_, err := p.Run()
	return err
}

func (tui *PonosAgentTUI) initModel() tuiModel {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "unknown"
	}

	ta := textarea.New()
	ta.Placeholder = ""
	ta.Focus()
	ta.Prompt = ""
	ta.CharLimit = 2000
	ta.SetWidth(80)
	ta.SetHeight(1) 
	ta.ShowLineNumbers = false
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle()
	ta.BlurredStyle.CursorLine = lipgloss.NewStyle()

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
			headerHeight := 8  
			loadingHeight := 1 
			helpHeight := 1    
			inputHeight := 4  
			spacingHeight := 2 

			messageHeight := msg.Height - headerHeight - loadingHeight - inputHeight - helpHeight - spacingHeight
			if messageHeight < 3 {
				messageHeight = 3
			}
			
			m.viewport = viewport.New(msg.Width, messageHeight)
			m.textarea.SetWidth(msg.Width - 8) 
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
			if m.loading {
				m.loading = false
				m.thoughtMsg = ""
				m.loadingText = ""
				
				if m.cancelThinking != nil {
					m.cancelThinking()
					m.cancelThinking = nil
				}
				
				m.messages = append(m.messages, ChatMessage{
					Role:      "system",
					Content:   "Operation interrupted by user",
					Timestamp: time.Now(),
				})
				m.updateViewportContent()
				return m, nil
			}
		case tea.KeyEnter:
			userInput := strings.TrimSpace(m.textarea.Value())
			if userInput != "" && !m.loading {
				m.messages = append(m.messages, ChatMessage{
					Role:      "user",
					Content:   userInput,
					Timestamp: time.Now(),
				})
				
				m.textarea.Reset()
				m.loading = true
				m.thoughtMsg = "Thinking"
				m.loadingText = "Ponosing..."
				m.updateViewportContent()
				
				ctx, cancel := context.WithCancel(context.Background())
				m.cancelThinking = cancel
				
				program := m.program
				go func() {
					m.tui.logger.Info("Starting async processing", "input", userInput)
					
					if program != nil {
						m.tui.logger.Info("Sending progress update")
						program.Send(progressUpdate{
							thought:     "Understanding request",
							loadingText: "Thinking...",
						})
					}
					
					m.tui.logger.Info("About to call handleUserInput")
					response, err := m.tui.handleUserInput(userInput)
					m.tui.logger.Info("handleUserInput completed", "response_length", len(response), "error", err)
					
					select {
					case <-ctx.Done():
						m.tui.logger.Info("Processing cancelled")
						return
					default:
					}
					
					if program != nil {
						m.tui.logger.Info("Sending final response")
						program.Send(msgResponse{content: response, err: err})
					} else {
						m.tui.logger.Error("program is nil, cannot send response")
					}
				}()
				
				return m, tea.Batch(
					m.startLoading(),
					m.tui.tickSpinner(),
				)
			}
		}

	case msgResponse:
		m.loading = false
		m.thoughtMsg = ""  
		m.loadingText = "" 
		m.cancelThinking = nil 
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
			return m, m.tui.tickSpinner()
		}
	}

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

	header := logoStyle.Render(ponosLogo) + "\n" +
		infoStyle.Render(fmt.Sprintf("Current Version: %s", version)) + "\n" +
		infoStyle.Render("/help for help, /status for your current setup") + "\n" +
		cwdStyle.Render(fmt.Sprintf("cwd: %s", m.currentDir)) + "\n"

	separatorLine := strings.Repeat("─", m.width-2)
	separator := lipgloss.NewStyle().Foreground(subtleColor).Render(separatorLine)
	
	messagesView := m.viewport.View()
	if len(m.messages) == 0 {
		messagesView = "" 
	}

	loadingIndicator := ""
	if m.loading {
		loadingIndicator = " " + loadingStyle.Render("●")
	}
	
	inputContent := promptStyle.Render("> ") + m.textarea.View() + loadingIndicator
	inputBox := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(subtleColor).
		Padding(0, 1).
		Width(m.width - 2).
		Render(inputContent)
	
	inputView := inputBox
	
	loadingView := m.renderLoadingLine()
	
	thoughtView := ""
	if m.thoughtMsg != "" {
		thoughtView = "\n" + lipgloss.NewStyle().
			Foreground(brandColor).
			Italic(true).
			Render("✽ " + m.thoughtMsg + "… (esc to interrupt)")
	}

	helpText := helpStyle.Render("? for shortcuts")

	if len(m.messages) == 0 {
		return header + "\n" + loadingView + "\n" + inputView + thoughtView + "\n" + helpText
	} else {
		return header + "\n" + separator + "\n" + messagesView + "\n" + loadingView + "\n" + inputView + thoughtView + "\n" + helpText
	}
}

func (m *tuiModel) updateViewportContent() {
	var content strings.Builder
	
	availableWidth := m.viewport.Width - 4
	if availableWidth < 20 {
		availableWidth = 20 
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
		
		wrappedContent := wrapText(prefix+text, availableWidth)
		content.WriteString(style.Render(wrappedContent))
		content.WriteString("\n\n")
	}
	
	m.viewport.SetContent(content.String())
	m.viewport.GotoBottom()
}

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
		if currentLine == "" {
			currentLine = word
		} else {
			testLine := currentLine + " " + word
			if len(testLine) <= width {
				currentLine = testLine
			} else {
				lines = append(lines, currentLine)
				currentLine = word
			}
		}
	}
	
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
		return "" 
	}
	
	spinnerChars := []string{"▐▌", "▄▄", "▀▀", "▐▌"}
	spinner := spinnerChars[m.spinnerFrame%len(spinnerChars)]
	
	loadingText := "Ponosing..."
	if m.loadingText != "" {
		loadingText = m.loadingText
	}
	
	return lipgloss.NewStyle().
		Foreground(brandColor).
		Bold(true).
		Render(fmt.Sprintf("%s %s", spinner, loadingText))
}

func (tui *PonosAgentTUI) tickSpinner() tea.Cmd {
	return tea.Tick(time.Millisecond*200, func(t time.Time) tea.Msg {
		return spinnerTick{}
	})
}




func (tui *PonosAgentTUI) handleUserInput(input string) (string, error) {
	ctx := context.Background()
	
	tui.logger.Info("Processing user input", "input", input)
	
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

	if tui.bot.agent == nil {
		tui.logger.Error("AI agent not available")
		return "Sorry, the AI agent is not available. Please ensure OPENAI_API_KEY is set.", nil
	}

	tui.logger.Info("AI agent available, processing with AI")

	if input == "test" {
		tui.logger.Info("Test mode - simple AI call")
		return "Test response: AI agent is working! Try asking 'hello, what can you do?'", nil
	}

	return tui.handleConversation(ctx, input)
}

func (tui *PonosAgentTUI) handleConversation(ctx context.Context, input string) (string, error) {
	tui.logger.Info("Starting AI conversation", "input", input)
	
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
	
	cwd, _ := os.Getwd()
	status += fmt.Sprintf("\nWorking Directory: %s", cwd)
	
	return status
}
