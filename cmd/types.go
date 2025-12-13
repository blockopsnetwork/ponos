package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/blockops-sh/ponos/config"
	"github.com/slack-go/slack"
)

type NodeOperatorAgent struct {
	logger       *slog.Logger
	agentCoreURL string
	httpClient   *http.Client
	useAgentCore bool
}

type AgentSummary struct {
	DetectedNetworks    []string                  `json:"detected_networks"`
	Severity            string                    `json:"severity"`
	Reasoning           string                    `json:"reasoning"`
	ReleaseSummary      string                    `json:"release_summary"`
	ConfigChangesNeeded string                    `json:"config_changes_needed"`
	ConfigChangesJSON   []ConfigChangeInstruction `json:"config_changes_json"`
	RiskAssessment      string                    `json:"risk_assessment"`
	DockerTag           string                    `json:"docker_tag"`
	PRTitle             string                    `json:"pr_title"`
	Success             bool                      `json:"success"`
	Error               string                    `json:"error,omitempty"`
}

type ConfigChangeInstruction struct {
	Description string      `json:"description,omitempty"`
	Action      string      `json:"action,omitempty"`
	Path        string      `json:"path,omitempty"`
	Value       interface{} `json:"value,omitempty"`
	Match       interface{} `json:"match,omitempty"`
}

type NetworkReleaseInfo struct {
	Network    string      `json:"network"`
	Repository Repository  `json:"repository"`
	Release    ReleaseInfo `json:"release"`
}

type StreamingUpdate struct {
	Type         string     `json:"type"`
	Message      string     `json:"message"`
	Tool         string     `json:"tool"`
	Success      bool       `json:"success"`
	Summary      string     `json:"summary"`
	SessionID    string     `json:"session_id"`
	CheckpointID string     `json:"checkpoint_id"`
	MessageID    string     `json:"message_id"`
	IsAppending  bool       `json:"is_appending"`
	Todos        []TodoItem `json:"todos,omitempty"`
	ToolName     string     `json:"tool_name,omitempty"`
}

type TodoItem struct {
	Content    string `json:"content"`
	Status     string `json:"status"`
	ActiveForm string `json:"active_form"`
}

type UpgradeIntent struct {
	RequiresAction bool   `json:"requires_action"`
	Network        string `json:"network"`
	ActionType     string `json:"action_type"`
	Confidence     string `json:"confidence"`
	Explanation    string `json:"explanation"`
}

type InfrastructureContext struct {
	DetectedClients    []DetectedClient `json:"detected_clients"`
	DeploymentType     string           `json:"deployment_type"`
	NetworkEnvironment string           `json:"network_environment"`
	ConfiguredImages   []string         `json:"configured_images"`
	Confidence         string           `json:"confidence"`
}

type DetectedClient struct {
	Repository  string `json:"repository"`
	CurrentTag  string `json:"current_tag"`
	ClientType  string `json:"client_type"`
	DockerImage string `json:"docker_image"`
	FilePath    string `json:"file_path"`
	NetworkName string `json:"network_name"`
}

type EnhancedUpgradeRequest struct {
	UserMessage         string                 `json:"user_message"`
	Intent              *UpgradeIntent         `json:"intent"`
	Infrastructure      *InfrastructureContext `json:"infrastructure"`
	NeedsClarification  bool                   `json:"needs_clarification"`
	ClarificationPrompt string                 `json:"clarification_prompt,omitempty"`
	TargetClientType    string                 `json:"target_client_type,omitempty"`
}

type SlashCommandResponse struct {
	ResponseType string        `json:"response_type"`
	Text         string        `json:"text,omitempty"`
	Blocks       []slack.Block `json:"blocks,omitempty"`
}

type GitHubMCPClient struct {
	baseURL        string
	sseURL         string
	messagesURL    string
	token          string
	client         *http.Client
	logger         *slog.Logger
	sessionID      string
	appID          string
	installID      string
	pemKey         string
	botName        string
	clientName     string
	userAgent      string
	cachedToken    string
	tokenExpiry    time.Time
	requestTimeout time.Duration
	connectTimeout time.Duration
	authMode       string
	authModeLogged bool

	ctx     context.Context
	cancel  context.CancelFunc
	sseBody io.ReadCloser

	pendingMu      sync.Mutex
	pending        map[int]chan mcpEnvelope
	requestCounter atomic.Int64
	connectMu      sync.Mutex

	endpointCh chan string
}

type GitHubDeployHandler struct {
	logger    *slog.Logger
	config    *config.Config
	slack     SlackClient
	agent     AgentClient
	mcpClient *GitHubMCPClient
	docker    *DockerOperations
	yaml      *YAMLOperations
}

type MCPRequest struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Method  string      `json:"method"`
	Params  interface{} `json:"params,omitempty"`
}

type MCPResponse struct {
	JSONRPC string      `json:"jsonrpc"`
	ID      int         `json:"id"`
	Result  interface{} `json:"result,omitempty"`
	Error   *MCPError   `json:"error,omitempty"`
}

type MCPError struct {
	Code    int         `json:"code"`
	Message string      `json:"message"`
	Data    interface{} `json:"data,omitempty"`
}

type mcpEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      *int            `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *MCPError       `json:"error,omitempty"`
}

type AgentClient interface {
	ProcessReleaseUpdate(ctx context.Context, payload ReleasesWebhookPayload) (*AgentSummary, error)
	ExtractImages(ctx context.Context, yamlContent string) ([]string, error)
	StreamConversation(ctx context.Context, userMessage string, conversationHistory []map[string]string, updates chan<- StreamingUpdate) error
}

type SlackClient interface {
	PostMessage(channelID string, options ...slack.MsgOption) (string, string, error)
}