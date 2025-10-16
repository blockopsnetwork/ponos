package main

import (
	"context"
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
	
	isStreaming         bool
	streamingMessageID  string              
	streamingToolID     string              
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

type animationTick struct {}

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
	ta.SetWidth(75)
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
			logoHeight := 10 
			titleHeight := 4 
			loadingHeight := 1
			helpHeight := 1
			inputHeight := 4
			spacingHeight := 3

			messageHeight := msg.Height - logoHeight - titleHeight - loadingHeight - inputHeight - helpHeight - spacingHeight
			if messageHeight < 3 {
				messageHeight = 3
			}

			m.viewport = viewport.New(msg.Width, messageHeight)
			m.textarea.SetWidth(msg.Width - 4)
			m.ready = true
		} else {
			logoHeight := 10
			titleHeight := 4
			loadingHeight := 1
			helpHeight := 1
			inputHeight := 4
			spacingHeight := 3
			messageHeight := msg.Height - logoHeight - titleHeight - loadingHeight - inputHeight - helpHeight - spacingHeight
			if messageHeight < 3 {
				messageHeight = 3
			}

			m.viewport.Width = msg.Width
			m.viewport.Height = messageHeight
			m.textarea.SetWidth(msg.Width - 4)
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
					case "/h", "/help":
						m.showHelp = !m.showHelp
						m.textarea.Reset()
						return m, nil
					case "/status":
						m.messages = append(m.messages, ChatMessage{
							Role:      "system",
							Content:   "Ponos Agent is running. Type /h for help.",
							Timestamp: time.Now(),
						})
						m.textarea.Reset()
						m.updateViewportContent()
						return m, nil
					case "/clear":
						m.messages = []ChatMessage{}
						m.textarea.Reset()
						m.updateViewportContent()
						return m, nil
					default:
						m.messages = append(m.messages, ChatMessage{
							Role:      "system",
							Content:   fmt.Sprintf("Unknown command: %s. Type /h for help.", userInput),
							Timestamp: time.Now(),
						})
						m.textarea.Reset()
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
				m.loading = true
				m.loadingMsg = "Ponos thinking..."
				m.animationFrame = 0
				m.updateViewportContent()

				ctx, cancel := context.WithCancel(context.Background())
				m.cancelThinking = cancel

				program := m.program
				go func() {
					animFrame := 0
					for {
						if !m.loading {
							break
						}
						time.Sleep(150 * time.Millisecond)
						if program != nil && m.loading {
							program.Send(animationUpdate{frame: animFrame})
							animFrame++
						}
					}
				}()

				go func() {
					m.tui.logger.Info("Starting streaming processing", "input", userInput)

					updates := make(chan StreamingUpdate, 10)

					go func() {
						defer close(updates)
						err := m.tui.handleUserInputWithStreaming(ctx, userInput, m.conversationHistory, updates)
						if err != nil {
							m.tui.logger.Error("Streaming processing failed", "error", err)
							if program != nil {
								program.Send(msgResponse{content: "", err: err})
							}
						}
					}()

					var finalResponse string
					for update := range updates {
						select {
						case <-ctx.Done():
							m.tui.logger.Info("Processing cancelled")
							return
						default:
						}

						if program != nil {
							switch update.Type {
							case "thinking":
								program.Send(streamingUpdate{update: update})
							case "tool_start":
								program.Send(streamingUpdate{update: update})
							case "tool_result":
								program.Send(streamingUpdate{update: update})
							case "assistant":
								finalResponse = update.Message
							case "complete":
								program.Send(msgResponse{content: finalResponse, err: nil})
								return
							}
						}
					}

					if finalResponse != "" && program != nil {
						program.Send(msgResponse{content: finalResponse, err: nil})
					}
				}()

				return m, nil
			}
		}

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
		case "tool_start":
			// Show tool execution in both status and chat history
			m.loadingMsg = fmt.Sprintf("Executing %s...", msg.update.Tool)
			m.messages = append(m.messages, ChatMessage{
				ID:        generateMessageID(),
				Role:      "activity",
				Content:   fmt.Sprintf("Executing %s", msg.update.Tool),
				Timestamp: time.Now(),
			})
			m.updateViewportContent()
		case "tool_result":
			// Show tool result in both status and chat history
			var statusMsg, chatMsg string
			if msg.update.Success {
				statusMsg = fmt.Sprintf("%s completed successfully", msg.update.Tool)
				chatMsg = fmt.Sprintf("%s completed successfully", msg.update.Tool)
			} else {
				statusMsg = fmt.Sprintf("%s failed", msg.update.Tool)
				chatMsg = fmt.Sprintf("%s failed", msg.update.Tool)
			}
			
			// Add summary as subtitle if available with tree connector
			if msg.update.Summary != "" {
				chatMsg += fmt.Sprintf("\n└ %s", msg.update.Summary)
			}
			
			m.loadingMsg = statusMsg
			m.messages = append(m.messages, ChatMessage{
				ID:        generateMessageID(),
				Role:      "activity",
				Content:   chatMsg,
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

	}

	m.textarea, cmd = m.textarea.Update(msg)
	cmds = append(cmds, cmd)

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
		subtitleStyle.Render("Node Operator AI Assistant") + "\n" +
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

	bottomRight := "⌘P to generate a command to know what ponos can do for you"
	if m.loading {
		indicator := m.getAnimatedIndicator()
		bottomRight = fmt.Sprintf("%s Processing...", indicator)
	}
	helpText := helpStyle.Render("/help for help and / to see available commands") +
		strings.Repeat(" ", max(0, m.width-len("/help for help and / to see available commands")-len(bottomRight))) +
		helpStyle.Render(bottomRight)
	sections = append(sections, helpText)

	return lipgloss.JoinVertical(lipgloss.Left, sections...)
}


func (m *tuiModel) getAnimatedIndicator() string {
	frames := []string{"|", "/", "-", "\\"}
	return frames[m.animationFrame%len(frames)]
}

func (m *tuiModel) updateViewportContent() {
	var content strings.Builder

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
		case "system":
			prefix = ""
			text = msg.Content
			style = systemMessageStyle
		case "error":
			prefix = "Error: "
			text = msg.Content
			style = errorMessageStyle
		case "activity":
			prefix = ""
			text = msg.Content
			style = activityStyle
		}

		if prefix != "" {
			content.WriteString(style.Render(prefix + text))
		} else {
			content.WriteString(style.Render(text))
		}
		content.WriteString("\n\n")
	}

	m.viewport.SetContent(content.String())
	m.viewport.GotoBottom()
}

func (tui *PonosAgentTUI) getHelpText() string {
	return `Available commands:
/help - Show this help message
/status - Show current setup status

Note: Type / to see command suggestions and available slash commands.

Blockchain Operations:
• "upgrade [network] to latest" - Upgrade any blockchain network
• "update network [name]" - Update specific network
• "new release for [client]" - Process release updates

I can help you with blockchain network upgrades and deployments.
Just tell me what you'd like me to do in natural language!`
}

func (tui *PonosAgentTUI) getStatusText() string {
	status := "Current Setup Status:\n\n"

	if tui.bot.config.GitHubToken != "" {
		status += "GitHub Token configured\n"
	} else {
		status += "GitHub Token missing\n"
	}

	if tui.bot.config.SlackToken != "" {
		status += "Slack Token configured\n"
	} else {
		status += "Slack Token missing\n"
	}

	if tui.bot.agent != nil {
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

	if tui.bot.agent == nil {
		tui.logger.Error("Agent-core not available")
		updates <- StreamingUpdate{Type: "assistant", Message: "Sorry, the agent-core backend is not available. Please ensure agent-core is running and accessible."}
		updates <- StreamingUpdate{Type: "complete", Message: "Done"}
		return nil
	}

	tui.logger.Info("Sending user prompt directly to agent-core", "input", input)
	return tui.bot.agent.ProcessConversationWithStreamingAndHistory(ctx, input, conversationHistory, updates)
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

		if tui.bot.githubHandler != nil {
			response := tui.bot.githubHandler.HandleChainUpdate("network", intent.Network, "tui-user")

			if response != nil {
				if len(response.Blocks) > 0 {
					tui.safeSendUpdate(updates, StreamingUpdate{Type: "activity", Message: "Network update process initiated via GitHub workflow."})
				} else if response.Text != "" {
					tui.safeSendUpdate(updates, StreamingUpdate{Type: "assistant", Message: response.Text})
				}
			} else {
				tui.safeSendUpdate(updates, StreamingUpdate{Type: "assistant", Message: "❌ Failed to initiate network upgrade. Check logs for details."})
			}
		} else {
			tui.safeSendUpdate(updates, StreamingUpdate{Type: "assistant", Message: "❌ GitHub handler not available. Cannot process upgrade request."})
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
