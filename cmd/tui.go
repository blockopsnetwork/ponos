package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	brandColor   = lipgloss.Color("#FFFFFF") // white
	textColor    = lipgloss.Color("#E5E7EB") // light gray
	subtleColor  = lipgloss.Color("#9CA3AF") // muted text
	successColor = lipgloss.Color("#10B981") // green for success
	errorColor   = lipgloss.Color("#EF4444") // red for errors
	accentColor  = lipgloss.Color("#F59E0B") // yellow for highlights
	purpleColor  = lipgloss.Color("#8B5CF6") // purple

	logoStyle = lipgloss.NewStyle().
			Foreground(purpleColor).
			Bold(true)

	titleStyle = lipgloss.NewStyle().
			Foreground(textColor).
			Bold(true)

	subtitleStyle = lipgloss.NewStyle().
			Foreground(subtleColor)

	promptStyle = lipgloss.NewStyle().
			Foreground(brandColor).
			Bold(true)

	userMessageStyle = lipgloss.NewStyle().
				Foreground(brandColor)

	assistantMessageStyle = lipgloss.NewStyle().
				Foreground(textColor)

	systemMessageStyle = lipgloss.NewStyle().
				Foreground(subtleColor)

	errorMessageStyle = lipgloss.NewStyle().
				Foreground(errorColor)

	successMessageStyle = lipgloss.NewStyle().
				Foreground(successColor)

	helpStyle = lipgloss.NewStyle().
			Foreground(subtleColor)

	activityStyle = lipgloss.NewStyle().
			Foreground(subtleColor).
			Italic(true)
)

const (
	appName = "Ponos Agent"
	version = "v0.2.0"

	asciiLogo = `
██████╗  ██████╗ ███╗   ██╗ ██████╗ ███████╗
██╔══██╗██╔═══██╗████╗  ██║██╔═══██╗██╔════╝
██████╔╝██║   ██║██╔██╗ ██║██║   ██║███████╗
██╔═══╝ ██║   ██║██║╚██╗██║██║   ██║╚════██║
██║     ╚██████╔╝██║ ╚████║╚██████╔╝███████║
╚═╝      ╚═════╝ ╚═╝  ╚═══╝ ╚═════╝ ╚══════╝
                                              
    Node Operator AI Agent                
`
)

type PonosAgentTUI struct {
	bot    *Bot
	logger *slog.Logger
}

type tuiModel struct {
	viewport            viewport.Model
	textarea            textarea.Model
	messages            []ChatMessage
	conversationHistory []map[string]string
	sessionID           string
	ready               bool
	width               int
	height              int
	tui                 *PonosAgentTUI
	loading             bool
	loadingMsg          string
	currentDir          string
	program             *tea.Program
	cancelThinking      context.CancelFunc
	showHelp            bool
	animationFrame      int

	isStreaming        bool
	streamingMessageID string
	streamingToolID    string

	currentTodos []TodoItem
	showTodos    bool

	helperVisible   bool
	helperSelected  int
	filteredHelpers []HelperCommand

	// autoScroll controls whether new messages jump to bottom; disabled when user scrolls manually.
	autoScroll bool
}

type HelperCommand struct {
	Command     string
	Description string
}

var helperCommands = []HelperCommand{
	{Command: "/help", Description: "Show detailed help and shortcuts"},
	{Command: "/status", Description: "Show configured tokens and working directory"},
	{Command: "/clear", Description: "Clear chat history"},
	{Command: "upgrade polkadot", Description: "Upgrade a network to the latest release"},
	{Command: "diagnose polkadot", Description: "Run diagnostics for a network"},
}

type ChatMessage struct {
	ID        string
	Role      string
	Content   string
	Timestamp time.Time
	Actions   []string
}

type msgResponse struct {
	content string
	err     error
}

type thoughtMsg struct {
	thought string
}

type streamingUpdate struct {
	update StreamingUpdate
}

type animationTick struct{}

type animationUpdate struct {
	frame int
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
		tea.WithMouseCellMotion(),
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
	ta.Placeholder = "..."
	ta.Focus()
	ta.Prompt = ""
	ta.CharLimit = 2000
	ta.SetWidth(80) // Will be properly sized in window resize
	ta.SetHeight(3)
	ta.ShowLineNumbers = false

	ta.FocusedStyle.Base = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(brandColor).
		Foreground(textColor)

	ta.BlurredStyle.Base = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(subtleColor).
		Foreground(subtleColor)

	ta.FocusedStyle.Placeholder = lipgloss.NewStyle().Foreground(subtleColor)
	ta.BlurredStyle.Placeholder = lipgloss.NewStyle().Foreground(subtleColor)

	vp := viewport.New(80, 20)

	sessionID := generateSessionID()

	m := tuiModel{
		textarea:            ta,
		viewport:            vp,
		messages:            []ChatMessage{},
		conversationHistory: []map[string]string{},
		sessionID:           sessionID,
		ready:               false,
		tui:                 tui,
		loading:             false,
		loadingMsg:          "",
		currentDir:          cwd,
		showHelp:            true,
		filteredHelpers:     helperCommands,
		autoScroll:          true,
	}

	return m
}

func generateSessionID() string {
	return fmt.Sprintf("%08x-%04x-%04x-%04x-%012x",
		rand.Uint32(),
		rand.Uint32()&0xffff,
		rand.Uint32()&0xffff,
		rand.Uint32()&0xffff,
		rand.Uint64()&0xffffffffffff,
	)
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
			logoHeight := 8
			titleHeight := 2
			loadingHeight := 1
			helpHeight := 1
			inputHeight := 3
			spacingHeight := 2

			messageHeight := msg.Height - logoHeight - titleHeight - loadingHeight - inputHeight - helpHeight - spacingHeight
			if messageHeight < 10 {
				messageHeight = 10
			}

			m.viewport = viewport.New(msg.Width, messageHeight)
			m.textarea.SetWidth(msg.Width - 4)
			m.ready = true
		} else {
			logoHeight := 8
			titleHeight := 2
			loadingHeight := 1
			helpHeight := 1
			inputHeight := 3
			spacingHeight := 2
			messageHeight := msg.Height - logoHeight - titleHeight - loadingHeight - inputHeight - helpHeight - spacingHeight
			if messageHeight < 10 {
				messageHeight = 10
			}

			m.viewport.Width = msg.Width
			m.viewport.Height = messageHeight
			m.textarea.SetWidth(msg.Width - 4)
		}
		m.width = msg.Width
		m.height = msg.Height
		m.updateViewportContent()

	case tea.KeyMsg:
		if m.handleHelperNavigation(msg) {
			return m, nil
		}

		switch msg.String() {
		case "pgup":
			m.viewport.HalfPageUp()
			m.autoScroll = false
			return m, nil
		case "pgdown":
			m.viewport.HalfPageDown()
			if m.viewport.AtBottom() {
				m.autoScroll = true
			}
			return m, nil
		}

		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyEsc:
			if m.loading {
				m.loading = false
				m.loadingMsg = ""

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
				if strings.HasPrefix(userInput, "/") {
					switch userInput {
					case "/", "/h", "/help":
						m.messages = append(m.messages, ChatMessage{
							Role:      "system",
							Content:   m.tui.getHelpText(),
							Timestamp: time.Now(),
						})
						m.textarea.Reset()
						m.updateHelperDropdown()
						m.updateViewportContent()
						return m, nil
					case "/status":
						m.messages = append(m.messages, ChatMessage{
							Role:      "system",
							Content:   "Ponos Agent is running. Type /h for help.",
							Timestamp: time.Now(),
						})
						m.textarea.Reset()
						m.updateHelperDropdown()
						m.updateViewportContent()
						return m, nil
					case "/clear":
						m.messages = []ChatMessage{}
						m.textarea.Reset()
						m.updateHelperDropdown()
						m.updateViewportContent()
						return m, nil
					default:
						m.messages = append(m.messages, ChatMessage{
							Role:      "system",
							Content:   fmt.Sprintf("Unknown command: %s. Type / for available commands.", userInput),
							Timestamp: time.Now(),
						})
						m.textarea.Reset()
						m.updateHelperDropdown()
						m.updateViewportContent()
						return m, nil
					}
				}

				m.messages = append(m.messages, ChatMessage{
					ID:        generateMessageID(),
					Role:      "user",
					Content:   userInput,
					Timestamp: time.Now(),
				})

				m.conversationHistory = append(m.conversationHistory, map[string]string{
					"role":    "user",
					"content": userInput,
				})

				m.textarea.Reset()
				m.updateHelperDropdown()
				m.loading = true
				m.loadingMsg = "Ponos thinking..."
				m.animationFrame = 0
				m.autoScroll = true
				m.updateViewportContent()

				ctx, cancel := context.WithCancel(context.Background())
				m.cancelThinking = cancel

				go func() {
					defer func() {
						if m.program != nil {
							m.program.Send(tea.Msg("loading_done"))
						}
					}()

					updates := make(chan StreamingUpdate, 10)
					go func() {
						defer close(updates)
						err := m.tui.handleUserInputWithStreaming(ctx, userInput, m.conversationHistory, updates)
						if err != nil {
							m.tui.logger.Error("Error handling user input", "error", err)
							// Send error message to TUI
							if m.program != nil {
								m.program.Send(msgResponse{content: "", err: err})
							}
						}
					}()

					var finalResponse string
					for update := range updates {
						if m.program == nil {
							continue
						}
						if ctx.Err() != nil {
							return
						}

						switch update.Type {
						case "thinking":
							m.program.Send(streamingUpdate{update: update})
						case "assistant":
							m.program.Send(streamingUpdate{update: update})
						case "tool_call":
							m.program.Send(streamingUpdate{update: update})
						case "complete":
							finalResponse = update.Message
						default:
							m.program.Send(streamingUpdate{update: update})
						}
					}

					if finalResponse != "" && ctx.Err() == nil {
						m.conversationHistory = append(m.conversationHistory, map[string]string{
							"role":    "assistant",
							"content": finalResponse,
						})
					}
				}()

				return m, nil
			}
		}

		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)
		m.updateHelperDropdown()

	case tea.MouseMsg:
		if msg.Action == tea.MouseActionPress {
			if msg.Button == tea.MouseButtonWheelUp {
				m.viewport.ScrollUp(3)
				m.autoScroll = false
				return m, nil
			}
			if msg.Button == tea.MouseButtonWheelDown {
				m.viewport.ScrollDown(3)
				if m.viewport.AtBottom() {
					m.autoScroll = true
				}
				return m, nil
			}
		}
		m.viewport, cmd = m.viewport.Update(msg)
		cmds = append(cmds, cmd)

	case msgResponse:
		m.loading = false
		m.loadingMsg = ""
		m.cancelThinking = nil

		if msg.err != nil {
			m.messages = append(m.messages, ChatMessage{
				ID:        generateMessageID(),
				Role:      "error",
				Content:   msg.err.Error(),
				Timestamp: time.Now(),
			})
		} else if msg.content != "" {
			m.messages = append(m.messages, ChatMessage{
				ID:        generateMessageID(),
				Role:      "assistant",
				Content:   msg.content,
				Timestamp: time.Now(),
			})

			m.conversationHistory = append(m.conversationHistory, map[string]string{
				"role":    "assistant",
				"content": msg.content,
			})
		}
		m.updateViewportContent()

	case thoughtMsg:
		m.loadingMsg = msg.thought

	case streamingUpdate:
		switch msg.update.Type {
		case "thinking":
			m.loadingMsg = msg.update.Message
			// Add thinking message to chat history to show context
			m.messages = append(m.messages, ChatMessage{
				ID:        generateMessageID(),
				Role:      "thinking",
				Content:   msg.update.Message,
				Timestamp: time.Now(),
			})
			m.updateViewportContent()
		case "tool_start":
			m.loadingMsg = fmt.Sprintf("Executing %s...", msg.update.Tool)
			m.messages = append(m.messages, ChatMessage{
				ID:        generateMessageID(),
				Role:      "tool_header",
				Content:   msg.update.Tool,
				Timestamp: time.Now(),
			})
			m.updateViewportContent()
		case "todo_update":
			if len(msg.update.Todos) > 0 {
				m.currentTodos = msg.update.Todos
				m.showTodos = true

				var activityMsg string
				switch msg.update.ToolName {
				case "create_todo":
					activityMsg = "Created new task"
				case "create_deployment_todos":
					activityMsg = fmt.Sprintf("Created deployment plan (%d tasks)", len(msg.update.Todos))
				case "update_todo":
					activityMsg = "Updated task status"
				case "list_todos":
					activityMsg = fmt.Sprintf("Showing %d active tasks", len(msg.update.Todos))
				default:
					activityMsg = "Updated tasks"
				}

				m.messages = append(m.messages, ChatMessage{
					ID:        generateMessageID(),
					Role:      "activity",
					Content:   activityMsg,
					Timestamp: time.Now(),
				})

				m.updateViewportContent()
			}
		case "tool_result":
			var statusMsg string
			if msg.update.Success {
				statusMsg = fmt.Sprintf("%s completed successfully", msg.update.Tool)
			} else {
				statusMsg = fmt.Sprintf("%s failed", msg.update.Tool)
			}

			m.loadingMsg = statusMsg
			m.messages = append(m.messages, ChatMessage{
				ID:        generateMessageID(),
				Role:      "tool_result",
				Content:   fmt.Sprintf("%s|%t", msg.update.Tool, msg.update.Success),
				Timestamp: time.Now(),
			})
			m.updateViewportContent()
		case "stream_append":
			if msg.update.MessageID != "" {
				m.handleStreamAppend(msg.update.MessageID, msg.update.Message)
			}
		case "assistant":
			if msg.update.MessageID != "" && msg.update.IsAppending {
				if m.findMessageByID(msg.update.MessageID) == nil {
					m.startStreamingMessage("assistant", msg.update.Message)
				} else {
					m.handleStreamAppend(msg.update.MessageID, msg.update.Message)
				}
			} else {
				m.messages = append(m.messages, ChatMessage{
					ID:        generateMessageID(),
					Role:      "assistant",
					Content:   msg.update.Message,
					Timestamp: time.Now(),
				})
				m.updateViewportContent()
			}
		case "complete":
			m.loading = false
			m.loadingMsg = ""
			m.completeStreamingMessage()

			if msg.update.SessionID != "" {
				m.sessionID = msg.update.SessionID
			}

			if msg.update.CheckpointID != "" {
				m.tui.logger.Info("Checkpoint created", "checkpoint_id", msg.update.CheckpointID, "session_id", msg.update.SessionID)
			}
		default:
			m.messages = append(m.messages, ChatMessage{
				ID:        generateMessageID(),
				Role:      "activity",
				Content:   msg.update.Message,
				Timestamp: time.Now(),
			})
			m.updateViewportContent()
		}

	case animationUpdate:
		if m.loading {
			m.animationFrame = msg.frame
		}

	case tea.Msg:
		if msg == "loading_done" {
			m.loading = false
			m.loadingMsg = ""
			m.cancelThinking = nil
		}

	}

	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m *tuiModel) View() string {
	if !m.ready {
		return titleStyle.Render("Initializing Ponos Agent...")
	}

	var sections []string

	logoSection := logoStyle.Render(asciiLogo)
	sections = append(sections, logoSection)

	titleSection := titleStyle.Render(appName) + " " + subtitleStyle.Render(version) + "\n" +
		subtitleStyle.Render("Node Operator AI Agent") + "\n" +
		subtitleStyle.Render(fmt.Sprintf("Working Directory: %s", m.currentDir)) + "\n" +
		subtitleStyle.Render("profile: default")

	sections = append(sections, titleSection)

	if len(m.messages) > 0 {
		sections = append(sections, m.viewport.View())
	} else {
		checkpointText := "checkpoint " + m.sessionID
		checkpointLen := len(checkpointText)
		var checkpointLine string
		if checkpointLen < m.width {
			leftDashes := (m.width - checkpointLen) / 2
			rightDashes := m.width - checkpointLen - leftDashes
			checkpointLine = strings.Repeat("─", leftDashes) + checkpointText + strings.Repeat("─", rightDashes)
		} else {
			checkpointLine = checkpointText[:m.width]
		}
		sections = append(sections, subtitleStyle.Render(checkpointLine))
	}

	if m.helperVisible && len(m.filteredHelpers) > 0 {
		sections = append(sections, m.renderHelperDropdown())
	}

	if len(m.currentTodos) > 0 {
		todoSection := m.renderTodoSection()
		sections = append(sections, todoSection)
	}

	if m.loading {
		loadingText := m.loadingMsg
		if loadingText == "" {
			loadingText = "Ponos thinking..."
		}
		indicator := m.getAnimatedIndicator()
		loadingLine := fmt.Sprintf("%s %s - Esc to cancel", indicator, loadingText)
		sections = append(sections, systemMessageStyle.Render(loadingLine))
	}

	sections = append(sections, m.textarea.View())

	leftHelp := "/help for help and / to see available commands"
	rightHelp := "⌘P to generate a command to know what ponos can do for you"
	if m.loading {
		indicator := m.getAnimatedIndicator()
		rightHelp = fmt.Sprintf("%s Processing...", indicator)
	}

	// Ensure help text fits within terminal width
	totalHelpLen := len(leftHelp) + len(rightHelp)
	if totalHelpLen >= m.width {
		// If too long, just show the left help
		helpText := helpStyle.Render(leftHelp)
		sections = append(sections, helpText)
	} else {
		spacePadding := strings.Repeat(" ", m.width-totalHelpLen)
		helpText := helpStyle.Render(leftHelp) + spacePadding + helpStyle.Render(rightHelp)
		sections = append(sections, helpText)
	}

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}

func (m *tuiModel) getAnimatedIndicator() string {
	frames := []string{"|", "/", "-", "\\"}
	return frames[m.animationFrame%len(frames)]
}

func (m *tuiModel) updateViewportContent() {
	var content strings.Builder
	maxWidth := m.viewport.Width - 4
	if maxWidth < 20 {
		maxWidth = 80
	}

	if len(m.messages) == 0 {
		content.WriteString("No messages yet. Type a message below to start.\n")
		m.viewport.SetContent(content.String())
		return
	}

	for _, msg := range m.messages {
		var prefix, text string
		var style lipgloss.Style

		switch msg.Role {
		case "user":
			prefix = "-> "
			text = msg.Content
			style = userMessageStyle
		case "assistant":
			prefix = ""
			text = msg.Content
			style = assistantMessageStyle
		case "thinking":
			prefix = "> "
			text = msg.Content
			style = systemMessageStyle
		case "system":
			prefix = ""
			text = msg.Content
			style = systemMessageStyle
		case "error":
			prefix = "Error: "
			text = msg.Content
			style = errorMessageStyle
		case "tool_header":
			prefix = ""
			text = getToolContextualMessage(msg.Content)
			style = lipgloss.NewStyle().Foreground(brandColor).Bold(false)
		case "tool_result":
			parts := strings.Split(msg.Content, "|")
			toolName := parts[0]
			success := len(parts) > 1 && parts[1] == "true"
			friendlyName := getToolCompletionMessage(toolName, success)
			if success {
				prefix = "✓ "
				text = friendlyName
				style = lipgloss.NewStyle().Foreground(successColor)
			} else {
				prefix = "✗ "
				text = friendlyName
				style = lipgloss.NewStyle().Foreground(errorColor)
			}
		case "activity":
			prefix = ""
			text = msg.Content
			style = activityStyle
		}

		// Wrap text to fit viewport width
		fullText := prefix + text
		wrappedText := wrapText(fullText, maxWidth)

		content.WriteString(style.Render(wrappedText))
		content.WriteString("\n\n")
	}

	m.viewport.SetContent(content.String())

	if m.autoScroll || m.isStreaming {
		m.viewport.GotoBottom()
	}
}

func (tui *PonosAgentTUI) getHelpText() string {
	return `Usage Modes
• Interactive – run ponos-tui and start chatting
• Non-interactive – ponos-tui -p "upgrade polkadot" to send a one-off prompt

Slash Commands
• /help      Show detailed help and shortcuts
• /status    Show configured tokens and working directory
• /clear     Clear chat history

Scrolling
• ↑/k        Scroll up one line
• ↓/j        Scroll down one line
• PgUp/Ctrl+U  Scroll up half page
• PgDn/Ctrl+D  Scroll down half page
• Home/g     Go to top
• End/G      Go to bottom

Tips
• Type / to open the helper menu, use ↑/↓ to pick a command, press Tab to autocomplete
• Shift+Enter inserts a newline, Enter sends your prompt, Ctrl+C exits the TUI
• Mouse selection and copy/paste work normally in the viewport

Example Prompts
• "Upgrade polkadot archive node to the latest stable release"
• "Run diagnostics for polkadot and summarize the findings"
• "List networks that have pending upgrades"`
}

func (tui *PonosAgentTUI) getStatusText() string {
	status := "Current Setup Status:\n\n"

	if tui.bot.config.Integrations.GitHub.Token != "" {
		status += "GitHub Token configured\n"
	} else {
		status += "GitHub Token missing\n"
	}

	if tui.bot.config.Integrations.Slack.Token != "" {
		status += "Slack Token configured\n"
	} else {
		status += "Slack Token missing\n"
	}

	if tui.bot.agentCoreURL != "" {
		status += "llm available\n"
	} else {
		status += "llm unavailable (check OPENAI_API_KEY)\n"
	}

	cwd, _ := os.Getwd()
	status += fmt.Sprintf("\nWorking Directory: %s", cwd)

	return status
}

func (tui *PonosAgentTUI) handleUserInputWithStreaming(ctx context.Context, input string, conversationHistory []map[string]string, updates chan<- StreamingUpdate) error {
	tui.logger.Info("Processing user input with streaming", "input", input)

	switch {
	case input == "/help":
		tui.logger.Info("Handling help command")
		updates <- StreamingUpdate{Type: "assistant", Message: tui.getHelpText()}
		updates <- StreamingUpdate{Type: "complete", Message: "Done"}
		return nil
	case input == "/status":
		tui.logger.Info("Handling status command")
		updates <- StreamingUpdate{Type: "assistant", Message: tui.getStatusText()}
		updates <- StreamingUpdate{Type: "complete", Message: "Done"}
		return nil
	case strings.HasPrefix(input, "/"):
		tui.logger.Info("Unknown slash command", "input", input)
		updates <- StreamingUpdate{Type: "assistant", Message: "Unknown command. Type /help for available commands."}
		updates <- StreamingUpdate{Type: "complete", Message: "Done"}
		return nil
	}

	if tui.bot.agentCoreURL == "" {
		tui.logger.Error("Agent-core not available")
		updates <- StreamingUpdate{Type: "assistant", Message: "Sorry, the nodeoperator api is not available. Please ensure the api is running and accessible."}
		updates <- StreamingUpdate{Type: "complete", Message: "Done"}
		return nil
	}

	tui.logger.Info("Sending user prompt directly to nodeoperator api", "input", input)
	return tui.bot.StreamConversation(ctx, input, conversationHistory, updates)
}

func (tui *PonosAgentTUI) handleUpgradeRequest(ctx context.Context, intent *UpgradeIntent, updates chan<- StreamingUpdate) error {
	if intent.Network == "unknown" || intent.Network == "" {
		updates <- StreamingUpdate{Type: "assistant", Message: "I couldn't determine which network you want to upgrade. Please specify the network (e.g., 'polkadot', 'kusama')."}
		updates <- StreamingUpdate{Type: "complete", Message: "Done"}
		return nil
	}

	updates <- StreamingUpdate{Type: "assistant", Message: fmt.Sprintf("🚀 Starting %s for %s network...", intent.ActionType, intent.Network)}
	updates <- StreamingUpdate{Type: "activity", Message: fmt.Sprintf("Network update started for %s", intent.Network)}

	go func() {
		defer func() {
			if r := recover(); r != nil {
				tui.logger.Error("Panic in handleUpgradeRequest", "error", r)
			}
			tui.safeSendUpdate(updates, StreamingUpdate{Type: "complete", Message: "Done"})
		}()

		if tui.bot.agentCoreURL != "" {
			userMessage := fmt.Sprintf("upgrade %s to latest", intent.Network)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			err := tui.bot.StreamConversation(ctx, userMessage, nil, updates)
			if err != nil {
				tui.safeSendUpdate(updates, StreamingUpdate{Type: "assistant", Message: fmt.Sprintf("❌ Failed to process upgrade request: %v", err)})
			}
		} else {
			tui.safeSendUpdate(updates, StreamingUpdate{Type: "assistant", Message: "❌ Agent not available. Cannot process upgrade request."})
		}
	}()

	return nil
}

func (tui *PonosAgentTUI) safeSendUpdate(updates chan<- StreamingUpdate, update StreamingUpdate) {
	defer func() {
		if r := recover(); r != nil {
			tui.logger.Warn("Failed to send update - channel likely closed", "error", r, "update", update.Type)
		}
	}()

	select {
	case updates <- update:
	default:
		tui.logger.Warn("Update channel blocked or closed", "update", update.Type)
	}
}

func (m *tuiModel) renderTodoSection() string {
	if len(m.currentTodos) == 0 {
		return ""
	}

	var todoLines []string

	var pending, inProgress, completed int
	for _, todo := range m.currentTodos {
		switch todo.Status {
		case "pending":
			pending++
		case "in_progress":
			inProgress++
		case "completed":
			completed++
		}
	}

	header := fmt.Sprintf("Active Tasks (%d pending, %d in progress, %d completed):",
		pending, inProgress, completed)
	todoLines = append(todoLines, titleStyle.Render(header))
	todoLines = append(todoLines, "") // Empty line for spacing

	for i, todo := range m.currentTodos {
		var status string
		var color lipgloss.Color

		switch todo.Status {
		case "pending":
			status = "[ ]"
			color = subtleColor
		case "in_progress":
			status = "[~]"
			color = accentColor
		case "completed":
			status = "[x]"
			color = successColor
		default:
			status = "[?]"
			color = subtleColor
		}

		line := fmt.Sprintf("%d. %s %s", i+1, status, todo.Content)
		styledLine := lipgloss.NewStyle().Foreground(color).Render(line)
		todoLines = append(todoLines, styledLine)
	}

	todoLines = append(todoLines, "") // Empty line after TODOs
	return lipgloss.JoinVertical(lipgloss.Left, todoLines...)
}

func (m *tuiModel) handleTodoUpdate(message string) error {
	var streamData struct {
		Type     string     `json:"type"`
		Todos    []TodoItem `json:"todos"`
		ToolName string     `json:"tool_name"`
	}

	m.tui.logger.Info("Handling TODO update", "message", message)

	if err := json.Unmarshal([]byte(message), &streamData); err != nil {
		m.tui.logger.Warn("Failed to parse todo update JSON", "error", err, "message", message)
		return m.handleSimpleTodoMessage(message)
	}

	if len(streamData.Todos) > 0 {
		m.currentTodos = streamData.Todos
		m.showTodos = true

		m.tui.logger.Info("Updated TODOs", "count", len(streamData.Todos), "tool", streamData.ToolName)

		if streamData.ToolName != "" {
			var activityMsg string
			switch streamData.ToolName {
			case "create_todo":
				activityMsg = "Created new task"
			case "create_deployment_todos":
				activityMsg = fmt.Sprintf("Created deployment plan (%d tasks)", len(streamData.Todos))
			case "update_todo":
				activityMsg = "Updated task status"
			case "list_todos":
				activityMsg = fmt.Sprintf("Showing %d active tasks", len(streamData.Todos))
			default:
				activityMsg = "Updated tasks"
			}

			m.messages = append(m.messages, ChatMessage{
				ID:        generateMessageID(),
				Role:      "activity",
				Content:   activityMsg,
				Timestamp: time.Now(),
			})
		}
	}

	return nil
}

func (m *tuiModel) handleSimpleTodoMessage(message string) error {
	if strings.Contains(message, "TODO") || strings.Contains(message, "task") {
		m.messages = append(m.messages, ChatMessage{
			ID:        generateMessageID(),
			Role:      "activity",
			Content:   message,
			Timestamp: time.Now(),
		})
	}
	return nil
}

func (m *tuiModel) updateHelperDropdown() {
	input := strings.TrimSpace(m.textarea.Value())
	if strings.HasPrefix(input, "/") {
		query := strings.ToLower(strings.TrimPrefix(input, "/"))
		var filtered []HelperCommand
		for _, helper := range helperCommands {
			cmd := strings.ToLower(helper.Command)
			desc := strings.ToLower(helper.Description)
			if query == "" || strings.Contains(cmd, query) || strings.Contains(desc, query) {
				filtered = append(filtered, helper)
			}
		}
		if len(filtered) == 0 {
			filtered = helperCommands
		}
		m.filteredHelpers = filtered
		m.helperVisible = true
		if m.helperSelected >= len(filtered) {
			m.helperSelected = 0
		}
	} else {
		m.helperVisible = false
		m.filteredHelpers = nil
		m.helperSelected = 0
	}
}

func (m *tuiModel) handleHelperNavigation(msg tea.KeyMsg) bool {
	if !m.helperVisible || len(m.filteredHelpers) == 0 {
		return false
	}

	switch msg.String() {
	case "up":
		if m.helperSelected > 0 {
			m.helperSelected--
		}
		return true
	case "down":
		if m.helperSelected+1 < len(m.filteredHelpers) {
			m.helperSelected++
		}
		return true
	case "tab":
		m.applyHelperSelection()
		m.updateHelperDropdown()
		return true
	}
	return false
}

func (m *tuiModel) applyHelperSelection() {
	if len(m.filteredHelpers) == 0 {
		return
	}
	selected := m.filteredHelpers[m.helperSelected]
	text := selected.Command
	if !strings.HasSuffix(text, " ") {
		text += " "
	}
	m.textarea.SetValue(text)
	m.textarea.CursorEnd()
	m.helperVisible = false
	m.helperSelected = 0
	m.filteredHelpers = helperCommands
}

func (m *tuiModel) renderHelperDropdown() string {
	if len(m.filteredHelpers) == 0 {
		return ""
	}

	var lines []string
	lines = append(lines, titleStyle.Render("Slash Commands"))

	for i, helper := range m.filteredHelpers {
		indicator := "  "
		style := helpStyle
		if i == m.helperSelected {
			indicator = "› "
			style = helpStyle.Foreground(accentColor).Bold(true)
		}
		entry := fmt.Sprintf("%s%s – %s", indicator, helper.Command, helper.Description)
		lines = append(lines, style.Render(entry))
	}

	lines = append(lines, "")
	lines = append(lines, helpStyle.Render("Shortcuts: Enter send • Shift+Enter newline • Tab autocomplete • Ctrl+C exit"))

	panel := strings.Join(lines, "\n")
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(subtleColor).
		Padding(0, 1).
		Render(panel)
}

func getToolContextualMessage(toolName string) string {
	contextMessages := map[string]string{
		"upgrade_blockchain_client": "Starting the node upgrade process...",
		"create_pull_request":       "Creating a pull request with the changes...",
		"update_network_images":     "Updating container images across the network...",
		"fetch_github_content":      "Fetching the latest configuration from GitHub...",
		"update_deployment_files":   "Updating deployment configuration files...",
		"run_network_diagnostics":   "Running diagnostics to check network health...",
		"validate_configuration":    "Validating the configuration settings...",
		"backup_current_state":      "Creating a backup of the current state...",
		"rollback_changes":          "Rolling back to the previous configuration...",
		"verify_upgrade_success":    "Verifying the upgrade completed successfully...",
	}

	if message, exists := contextMessages[toolName]; exists {
		return message
	}

	// Fallback: create a contextual message from the tool name
	friendlyName := strings.ReplaceAll(toolName, "_", " ")
	return fmt.Sprintf("Working on %s...", friendlyName)
}

func getToolCompletionMessage(toolName string, success bool) string {
	if success {
		successMessages := map[string]string{
			"upgrade_blockchain_client": "Node upgrade completed successfully",
			"create_pull_request":       "Pull request created successfully",
			"update_network_images":     "Container images updated across the network",
			"fetch_github_content":      "Configuration fetched from GitHub",
			"update_deployment_files":   "Deployment configuration updated",
			"run_network_diagnostics":   "Network diagnostics completed - all systems healthy",
			"validate_configuration":    "Configuration validation passed",
			"backup_current_state":      "Current state backed up successfully",
			"rollback_changes":          "Successfully rolled back to previous configuration",
			"verify_upgrade_success":    "Upgrade verification completed successfully",
		}

		if message, exists := successMessages[toolName]; exists {
			return message
		}

		// Fallback
		friendlyName := strings.ReplaceAll(toolName, "_", " ")
		return fmt.Sprintf("%s completed successfully", strings.Title(friendlyName))
	} else {
		failureMessages := map[string]string{
			"upgrade_blockchain_client": "Node upgrade failed - please check the logs",
			"create_pull_request":       "Failed to create pull request",
			"update_network_images":     "Failed to update container images",
			"fetch_github_content":      "Failed to fetch configuration from GitHub",
			"update_deployment_files":   "Failed to update deployment configuration",
			"run_network_diagnostics":   "Network diagnostics failed - issues detected",
			"validate_configuration":    "Configuration validation failed",
			"backup_current_state":      "Failed to backup current state",
			"rollback_changes":          "Failed to rollback changes",
			"verify_upgrade_success":    "Upgrade verification failed",
		}

		if message, exists := failureMessages[toolName]; exists {
			return message
		}

		// Fallback
		friendlyName := strings.ReplaceAll(toolName, "_", " ")
		return fmt.Sprintf("%s failed", strings.Title(friendlyName))
	}
}

func wrapText(text string, width int) string {
	if width <= 0 {
		return text
	}

	var result strings.Builder
	lines := strings.Split(text, "\n")

	for i, line := range lines {
		if i > 0 {
			result.WriteString("\n")
		}

		if len(line) <= width {
			result.WriteString(line)
			continue
		}

		words := strings.Fields(line)
		if len(words) == 0 {
			result.WriteString(line)
			continue
		}

		currentLine := ""
		for _, word := range words {
			if len(word) > width {
				if currentLine != "" {
					result.WriteString(currentLine + "\n")
					currentLine = ""
				}
				for len(word) > width {
					result.WriteString(word[:width] + "\n")
					word = word[width:]
				}
				if word != "" {
					currentLine = word
				}
			} else if currentLine == "" {
				currentLine = word
			} else if len(currentLine)+1+len(word) <= width {
				currentLine += " " + word
			} else {
				result.WriteString(currentLine + "\n")
				currentLine = word
			}
		}
		if currentLine != "" {
			result.WriteString(currentLine)
		}
	}

	return result.String()
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

