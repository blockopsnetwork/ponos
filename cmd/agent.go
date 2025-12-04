package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
)

func getStringFromMap(m map[string]interface{}, key string) string {
	if val, ok := m[key]; ok && val != nil {
		if str, ok := val.(string); ok {
			return str
		}
	}
	return ""
}

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

func NewNodeOperatorAgent(logger *slog.Logger) (*NodeOperatorAgent, error) {
	agentCoreURL := os.Getenv("AGENT_CORE_URL")
	if agentCoreURL == "" {
		agentCoreURL = "http://localhost:8001"
	}

	agent := &NodeOperatorAgent{
		logger:       logger,
		agentCoreURL: agentCoreURL,
		httpClient: &http.Client{
			Timeout: 300 * time.Second, // 5 minutes for streaming operations
		},
		useAgentCore: true, // Always use agent-core for conversations
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := agent.healthCheckAgentCore(ctx); err != nil {
		return nil, fmt.Errorf("agent-core is not available: %w. This system requires agent-core to be running", err)
	}

	logger.Info("Connected to intelligent agent-core backend", "url", agentCoreURL)

	return agent, nil
}

func (agent *NodeOperatorAgent) healthCheckAgentCore(ctx context.Context) error {
	url := fmt.Sprintf("%s/health", agent.agentCoreURL)
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return fmt.Errorf("failed to create health check request: %w", err)
	}

	resp, err := agent.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("health check failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("agent-core health check failed with status %d", resp.StatusCode)
	}

	return nil
}

func (agent *NodeOperatorAgent) callAgentCoreReleaseAnalysis(ctx context.Context, payload ReleasesWebhookPayload) (*AgentSummary, error) {
	request := map[string]interface{}{
		"repositories": payload.Repositories,
		"releases":     payload.Releases,
		"event_type":   payload.EventType,
		"username":     payload.Username,
	}

	requestBody, err := json.Marshal(request)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/blockchain/analyze-release", agent.agentCoreURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(requestBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := agent.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("HTTP request to agent-core failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("agent-core returned error status %d: %s", resp.StatusCode, string(body))
	}

	var analysisResult AgentSummary
	if err := json.NewDecoder(resp.Body).Decode(&analysisResult); err != nil {
		return nil, fmt.Errorf("failed to decode agent-core response: %w", err)
	}

	if !analysisResult.Success {
		return nil, fmt.Errorf("agent-core analysis failed: %s", analysisResult.Error)
	}

	return &analysisResult, nil
}

func (agent *NodeOperatorAgent) callAgentCore(ctx context.Context, prompt string) (string, error) {
	request := map[string]interface{}{
		"message": prompt,
		"context": map[string]interface{}{
			"source":    "ponos-network-analysis",
			"timestamp": time.Now().Format(time.RFC3339),
			"user_type": "blockchain_operator",
		},
	}

	requestBody, err := json.Marshal(request)
	if err != nil {
		return "", fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/agent/simple", agent.agentCoreURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(requestBody))
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := agent.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("HTTP request to agent-core failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("agent-core returned error status %d: %s", resp.StatusCode, string(body))
	}

	var response map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&response); err != nil {
		return "", fmt.Errorf("failed to decode agent-core response: %w", err)
	}

	content, ok := response["content"].(string)
	if !ok {
		return "", fmt.Errorf("invalid response format from agent-core")
	}

	return content, nil
}

func (agent *NodeOperatorAgent) processStreamingResponseWithUpdates(body io.Reader, updates chan<- StreamingUpdate) error {
	scanner := bufio.NewScanner(body)
	var assistantMessageID string

	agent.logger.Info("Processing streaming response with real-time updates")

	for scanner.Scan() {
		line := scanner.Text()

		if !strings.HasPrefix(line, "data: ") {
			continue
		}

		dataStr := strings.TrimPrefix(line, "data: ")
		var streamEvent map[string]interface{}

		if err := json.Unmarshal([]byte(dataStr), &streamEvent); err != nil {
			agent.logger.Warn("Failed to parse stream event", "error", err, "data", dataStr)
			continue
		}

		eventType, ok := streamEvent["type"].(string)
		if !ok {
			continue
		}

		message, _ := streamEvent["message"].(string)

		switch eventType {
		case "thinking":
			agent.logger.Info("Stream thinking", "message", message)
			updates <- StreamingUpdate{
				Type:    "thinking",
				Message: message,
			}

		case "tool_start":
			if tool, ok := streamEvent["tool"].(string); ok {
				agent.logger.Info("Stream tool start", "tool", tool)
				updates <- StreamingUpdate{
					Type:    "tool_start",
					Message: fmt.Sprintf("Running %s", tool),
					Tool:    tool,
				}
			}

		case "tool_result":
			if tool, ok := streamEvent["tool"].(string); ok {
				success, _ := streamEvent["success"].(bool)
				summary, _ := streamEvent["summary"].(string)

				agent.logger.Info("Stream tool result", "tool", tool, "success", success, "summary", summary)
				updates <- StreamingUpdate{
					Type:    "tool_result",
					Message: fmt.Sprintf("%s completed", tool),
					Tool:    tool,
					Success: success,
					Summary: summary,
				}
			}

		case "assistant":
			agent.logger.Info("Stream assistant response", "message", message)
			if assistantMessageID == "" {
				assistantMessageID = generateID("msg")
				updates <- StreamingUpdate{
					Type:        "assistant",
					Message:     message,
					MessageID:   assistantMessageID,
					IsAppending: false,
				}
			} else {
				updates <- StreamingUpdate{
					Type:      "stream_append",
					Message:   message,
					MessageID: assistantMessageID,
				}
			}

		case "status":
			agent.logger.Info("Stream status", "message", message)
			updates <- StreamingUpdate{
				Type:    "status",
				Message: message,
			}

		case "todo_update":
			agent.logger.Info("Stream TODO update", "message", message)

			var todos []TodoItem
			if todosInterface, ok := streamEvent["todos"].([]interface{}); ok {
				for _, todoInterface := range todosInterface {
					if todoMap, ok := todoInterface.(map[string]interface{}); ok {
						todo := TodoItem{
							Content:    getStringFromMap(todoMap, "content"),
							Status:     getStringFromMap(todoMap, "status"),
							ActiveForm: getStringFromMap(todoMap, "activeForm"),
						}
						todos = append(todos, todo)
					}
				}
			}

			toolName, _ := streamEvent["tool_name"].(string)

			updates <- StreamingUpdate{
				Type:     "todo_update",
				Message:  message,
				Todos:    todos,
				ToolName: toolName,
			}

		case "complete":
			agent.logger.Info("Stream completed")

			sessionID, _ := streamEvent["session_id"].(string)
			checkpointID, _ := streamEvent["checkpoint_id"].(string)

			updates <- StreamingUpdate{
				Type:         "complete",
				Message:      "Operation completed",
				SessionID:    sessionID,
				CheckpointID: checkpointID,
			}

		case "error":
			agent.logger.Error("Stream error", "message", message)
			return fmt.Errorf("agent-core error: %s", message)
		}
	}

	if err := scanner.Err(); err != nil {
		return fmt.Errorf("error reading stream: %w", err)
	}

	return nil
}

func (agent *NodeOperatorAgent) ProcessReleaseUpdate(ctx context.Context, payload ReleasesWebhookPayload) (*AgentSummary, error) {
	response, err := agent.callAgentCoreReleaseAnalysis(ctx, payload)
	if err != nil {
		agent.logger.Error("agent-core blockchain analysis failed", "error", err)
		return nil, err
	}

	return response, nil
}

func (agent *NodeOperatorAgent) AnalyzeYAMLForBlockchainContainers(ctx context.Context, yamlContent string) ([]string, error) {
	prompt := agent.buildYAMLAnalysisPrompt(yamlContent)

	response, err := agent.callAgentCore(ctx, prompt)
	if err != nil {
		agent.logger.Error("agent-core YAML analysis failed", "error", err)
		return nil, err
	}

	repos := agent.parseYAMLAnalysisResponse(response)
	agent.logger.Info("agent-core YAML analysis completed", "containers_found", len(repos))

	return repos, nil
}

func (agent *NodeOperatorAgent) buildYAMLAnalysisPrompt(yamlContent string) string {
	return fmt.Sprintf(`You are a blockchain infrastructure expert. Analyze this Kubernetes/Docker Compose YAML file and identify ONLY the main blockchain node containers that should be updated with new versions.

IMPORTANT RULES:
1. ONLY identify containers that are actual blockchain nodes/validators/consensus clients
2. EXCLUDE monitoring, logging, proxy, database, and utility containers
3. Look for containers that process blockchain transactions, maintain consensus, or validate blocks
4. Return ONLY the image repository names (without tags) that should be updated

Examples of what TO include:
- parity/polkadot (Polkadot/Kusama nodes)
- ethereum/client-go (Ethereum Geth)
- solanalabs/solana (Solana validators)
- inputoutput/cardano-node (Cardano nodes)
- cosmoshub/gaiad (Cosmos Hub)
- Custom blockchain images that clearly run blockchain nodes

Examples of what to EXCLUDE:
- nginx, envoy (proxies)
- postgres, redis, mysql (databases)  
- prometheus, grafana (monitoring)
- fluent-bit, filebeat (logging)
- busybox, alpine (utilities)

YAML Content:
%s

Return only a JSON array of image repository names (without tags) that should be updated:
["repo1/image1", "repo2/image2"]

If no blockchain containers are found, return: []`, yamlContent)
}

func (agent *NodeOperatorAgent) parseYAMLAnalysisResponse(response string) []string {
	var repos []string
	if err := agent.extractAndUnmarshalJSON(response, &repos); err != nil {
		agent.logger.Warn("Failed to parse YAML analysis response", "error", err, "response", response)
		return []string{}
	}
	return repos
}

func (agent *NodeOperatorAgent) extractAndUnmarshalJSON(input string, target interface{}) error {
	// Find the start and end of the JSON content
	input = strings.TrimSpace(input)
	
	// Handle markdown code blocks
	if strings.HasPrefix(input, "```") {
		lines := strings.Split(input, "\n")
		if len(lines) >= 2 {
			// Remove first line (```json or ```) and last line (```)
			if strings.HasPrefix(lines[0], "```") {
				lines = lines[1:]
			}
			if len(lines) > 0 && strings.HasPrefix(lines[len(lines)-1], "```") {
				lines = lines[:len(lines)-1]
			}
			input = strings.Join(lines, "\n")
		}
	}

	// Find first '{' or '['
	startIdx := strings.IndexAny(input, "{[")
	if startIdx == -1 {
		return fmt.Errorf("no JSON start found")
	}

	// Find last '}' or ']'
	endIdx := strings.LastIndexAny(input, "}]")
	if endIdx == -1 || endIdx <= startIdx {
		return fmt.Errorf("no JSON end found")
	}

	jsonStr := input[startIdx : endIdx+1]
	return json.Unmarshal([]byte(jsonStr), target)
}

type StreamingUpdate struct {
	Type         string // "thinking", "tool_start", "tool_result", "assistant", "complete", "stream_append", "todo_update"
	Message      string
	Tool         string
	Success      bool
	Summary      string // Detailed results or error information from tool execution
	SessionID    string
	CheckpointID string
	MessageID    string
	IsAppending  bool

	Todos    []TodoItem `json:"todos,omitempty"`
	ToolName string     `json:"tool_name,omitempty"`
}

type TodoItem struct {
	Content    string `json:"content"`
	Status     string `json:"status"` // pending, in_progress, completed
	ActiveForm string `json:"active_form"`
}

func (agent *NodeOperatorAgent) ProcessConversationWithStreaming(ctx context.Context, userMessage string, updates chan<- StreamingUpdate) error {
	agent.logger.Info("ProcessConversationWithStreaming called", "message", userMessage)
	return agent.processConversationWithAgentCoreStreaming(ctx, userMessage, updates)
}

func (agent *NodeOperatorAgent) ProcessConversationWithStreamingAndHistory(ctx context.Context, userMessage string, conversationHistory []map[string]string, updates chan<- StreamingUpdate) error {
	agent.logger.Info("ProcessConversationWithStreamingAndHistory called", "message", userMessage, "history_length", len(conversationHistory))
	return agent.processConversationWithAgentCoreStreamingAndHistory(ctx, userMessage, conversationHistory, updates)
}

func (agent *NodeOperatorAgent) processConversationWithAgentCoreStreaming(ctx context.Context, userMessage string, updates chan<- StreamingUpdate) error {
	agent.logger.Info("Using intelligent agent-core for real-time streaming", "message", userMessage)

	request := map[string]interface{}{
		"message": userMessage,
		"context": map[string]interface{}{
			"source":    "ponos-tui",
			"timestamp": time.Now().Format(time.RFC3339),
			"user_type": "blockchain_operator",
			"capabilities": []string{
				"network_upgrades",
				"file_operations",
				"system_commands",
				"blockchain_analysis",
			},
		},
	}

	requestBody, err := json.Marshal(request)
	if err != nil {
		agent.logger.Error("Failed to marshal request", "error", err)
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/agent/stream", agent.agentCoreURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(requestBody))
	if err != nil {
		agent.logger.Error("Failed to create HTTP request", "error", err)
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", "Ponos-TUI/1.0")

	resp, err := agent.httpClient.Do(req)
	if err != nil {
		agent.logger.Error("HTTP request to agent-core failed", "error", err, "url", url)
		return fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		agent.logger.Error("Agent-core returned error", "status", resp.StatusCode, "body", string(body))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return agent.processStreamingResponseWithUpdates(resp.Body, updates)
}

func (agent *NodeOperatorAgent) processConversationWithAgentCoreStreamingAndHistory(ctx context.Context, userMessage string, conversationHistory []map[string]string, updates chan<- StreamingUpdate) error {
	agent.logger.Info("Using agent-core for real-time streaming with conversation history", "message", userMessage, "history_length", len(conversationHistory))

	request := map[string]interface{}{
		"message":              userMessage,
		"conversation_history": conversationHistory,
		"context": map[string]interface{}{
			"source":    "ponos-tui",
			"timestamp": time.Now().Format(time.RFC3339),
			"user_type": "blockchain_operator",
			"capabilities": []string{
				"network_upgrades",
				"file_operations",
				"system_commands",
				"blockchain_analysis",
			},
		},
	}

	if len(conversationHistory) > 0 {
		request["session_id"] = nil
	}

	requestBody, err := json.Marshal(request)
	if err != nil {
		agent.logger.Error("Failed to marshal request", "error", err)
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	url := fmt.Sprintf("%s/agent/stream", agent.agentCoreURL)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewBuffer(requestBody))
	if err != nil {
		agent.logger.Error("Failed to create HTTP request", "error", err)
		return fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("User-Agent", "Ponos-TUI/1.0")

	resp, err := agent.httpClient.Do(req)
	if err != nil {
		agent.logger.Error("HTTP request to agent-core failed", "error", err, "url", url)
		return fmt.Errorf("connection failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		agent.logger.Error("Agent-core returned error", "status", resp.StatusCode, "body", string(body))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}

	return agent.processStreamingResponseWithUpdates(resp.Body, updates)
}

func (agent *NodeOperatorAgent) ParseUpgradeIntent(ctx context.Context, userMessage string) (*UpgradeIntent, error) {
	prompt := fmt.Sprintf(`Analyze this user message to determine what action they want.

User Message: "%s"

Respond with JSON in this exact format:
{
  "requires_action": true/false,
  "network": "polkadot|kusama|ethereum|solana|other|unknown",
  "action_type": "check|status|info|upgrade|update|deploy|none",
  "confidence": "high|medium|low",
  "explanation": "brief explanation of what the user wants"
}

Guidelines:
- ONLY set requires_action=true if user wants to ACTUALLY CHANGE something (upgrade/update/deploy)
- If user asks to "check", "show", "get", "what is", "tell me" -> action_type="check" or "status", requires_action=false
- If user wants to "upgrade", "update", "deploy", "install" -> requires_action=true
- Action types:
  * "check" - asking for information/version/status (NO ACTION)
  * "status" - checking current state (NO ACTION) 
  * "info" - requesting information (NO ACTION)
  * "upgrade" - actually upgrade version (ACTION REQUIRED)
  * "update" - actually update configuration (ACTION REQUIRED)
  * "deploy" - actually deploy something (ACTION REQUIRED)
- Network detection:
  * Ethereum: "ethereum", "eth", "geth", "lighthouse", "reth", "besu"
  * Polkadot: "polkadot", "dot", "parity"
  * Kusama: "kusama", "ksm"
  * Solana: "solana", "sol"
- IMPORTANT: "check latest version" means action_type="check", requires_action=false`, userMessage)

	response, err := agent.callAgentCore(ctx, prompt)
	if err != nil {
		return nil, err
	}

	return agent.parseUpgradeIntentResponse(response), nil
}

type UpgradeIntent struct {
	RequiresAction bool   `json:"requires_action"`
	Network        string `json:"network"`
	ActionType     string `json:"action_type"`
	Confidence     string `json:"confidence"`
	Explanation    string `json:"explanation"`
}

func (agent *NodeOperatorAgent) parseUpgradeIntentResponse(response string) *UpgradeIntent {
	intent := &UpgradeIntent{
		RequiresAction: false,
		Network:        "unknown",
		ActionType:     "none",
		Confidence:     "low",
		Explanation:    "Unable to parse response",
	}

	if err := agent.extractAndUnmarshalJSON(response, intent); err != nil {
		agent.logger.Warn("Failed to parse upgrade intent response", "error", err, "response", response)
		// Fallback to fuzzy matching if JSON parsing fails, or just return default
		// For now, let's keep the default "unknown" state which is safe
		return intent
	}

	// Normalize fields
	intent.Network = strings.ToLower(intent.Network)
	intent.ActionType = strings.ToLower(intent.ActionType)

	// Ensure confidence is high if we successfully parsed valid data
	if intent.Network != "unknown" && intent.Network != "" {
		intent.Confidence = "high"
	}

	return intent
}

func (agent *NodeOperatorAgent) AnalyzeUpgradeRequestWithContext(ctx context.Context, userMessage string, configFiles []string) (*EnhancedUpgradeRequest, error) {
	intent, err := agent.ParseUpgradeIntent(ctx, userMessage)
	if err != nil {
		return nil, fmt.Errorf("failed to parse user intent: %w", err)
	}

	infrastructure := &InfrastructureContext{
		DetectedClients: []DetectedClient{},
		Confidence:      "dynamic",
	}

	request := &EnhancedUpgradeRequest{
		UserMessage:    userMessage,
		Intent:         intent,
		Infrastructure: infrastructure,
	}

	clarification := agent.determineIfClarificationNeeded(request)
	if clarification != "" {
		request.NeedsClarification = true
		request.ClarificationPrompt = clarification
		return request, nil
	}

	request.TargetClientType = agent.determineTargetClientType(request)

	return request, nil
}

func (agent *NodeOperatorAgent) determineIfClarificationNeeded(request *EnhancedUpgradeRequest) string {
	if request.Intent.Network == "unknown" && len(request.Infrastructure.DetectedClients) > 1 {
		return agent.buildNetworkClarificationPrompt(request)
	}

	if request.Intent.Network != "unknown" && len(agent.getClientsForNetwork(request, request.Intent.Network)) > 1 {
		return agent.buildClientTypeClarificationPrompt(request)
	}

	if request.Intent.RequiresAction && request.Intent.Network == "unknown" && len(request.Infrastructure.DetectedClients) == 0 {
		return agent.buildGeneralClarificationPrompt(request)
	}

	return ""
}

func (agent *NodeOperatorAgent) getClientsForNetwork(request *EnhancedUpgradeRequest, network string) []DetectedClient {
	var clients []DetectedClient
	for _, client := range request.Infrastructure.DetectedClients {
		if strings.EqualFold(client.NetworkName, network) {
			clients = append(clients, client)
		}
	}
	return clients
}

func (agent *NodeOperatorAgent) buildNetworkClarificationPrompt(request *EnhancedUpgradeRequest) string {
	networks := make(map[string][]DetectedClient)
	for _, client := range request.Infrastructure.DetectedClients {
		networks[client.NetworkName] = append(networks[client.NetworkName], client)
	}

	prompt := fmt.Sprintf("I found blockchain clients for multiple networks in your setup:\n\n")
	for network, clients := range networks {
		prompt += fmt.Sprintf("**%s:**\n", strings.Title(network))
		for _, client := range clients {
			prompt += fmt.Sprintf("- %s (%s client, version %s)\n", client.Repository, client.ClientType, client.CurrentTag)
		}
		prompt += "\n"
	}

	prompt += "Which network would you like to upgrade? Please specify the network name (e.g., 'ethereum', 'polkadot') or describe which specific client you want to update."

	return prompt
}

func (agent *NodeOperatorAgent) buildClientTypeClarificationPrompt(request *EnhancedUpgradeRequest) string {
	network := request.Intent.Network
	clients := agent.getClientsForNetwork(request, network)

	prompt := fmt.Sprintf("I found multiple %s clients in your setup:\n\n", strings.Title(network))
	for _, client := range clients {
		prompt += fmt.Sprintf("- **%s** (%s client, currently %s) in %s\n",
			client.Repository, client.ClientType, client.CurrentTag, client.FilePath)
	}

	prompt += fmt.Sprintf("\nWhich %s client would you like to upgrade? You can specify:\n", network)
	prompt += "- The client type (e.g., 'execution client', 'consensus client')\n"
	prompt += "- The specific client name (e.g., 'geth', 'lighthouse', 'reth')\n"
	prompt += "- Or say 'all clients' to upgrade everything"

	return prompt
}

func (agent *NodeOperatorAgent) buildGeneralClarificationPrompt(request *EnhancedUpgradeRequest) string {
	return `I couldn't detect any blockchain clients in your configuration files or determine which network you want to upgrade.

Please provide more specific information:
- Which blockchain network? (e.g., 'ethereum', 'polkadot', 'solana')
- Which client type? (e.g., 'execution client', 'consensus client', 'validator')
- Any specific client preferences? (e.g., 'geth', 'lighthouse', 'reth')

Example: "Upgrade my Ethereum execution client to the latest Geth version"`
}

func (agent *NodeOperatorAgent) determineTargetClientType(request *EnhancedUpgradeRequest) string {
	if request.Intent.Network == "unknown" {
		return ""
	}

	clients := agent.getClientsForNetwork(request, request.Intent.Network)
	if len(clients) == 1 {
		return clients[0].ClientType
	}

	preferences := agent.getPreferredClientTypes(request.Intent.Network)
	for _, prefType := range preferences {
		for _, client := range clients {
			if strings.EqualFold(client.ClientType, prefType) {
				return prefType
			}
		}
	}

	return ""
}

func (agent *NodeOperatorAgent) GetLatestNetworkRelease(ctx context.Context, network string) (*NetworkReleaseInfo, error) {
	return agent.GetLatestNetworkReleaseWithClientType(ctx, network, "")
}

func (agent *NodeOperatorAgent) GetLatestNetworkReleaseWithClientType(ctx context.Context, network, clientType string) (*NetworkReleaseInfo, error) {
	baseURL := os.Getenv("NODE_RELEASES_API_BASE_URL")
	if baseURL == "" {
		baseURL = "https://api.nodereleases.com"
	}

	requestedClientType := strings.ToLower(clientType)

	type releaseMetadata struct {
		ClientType   string `json:"client_type"`
		NetworkName  string `json:"network_name"`
		DisplayName  string `json:"display_name"`
		DockerRepo   string `json:"docker_repo"`
		DockerHubTag string `json:"dockerhub_tag"`
	}

	type releaseRecord struct {
		Repository string `json:"repository"`
		Release    *struct {
			TagName     string `json:"tag_name"`
			Name        string `json:"name"`
			Body        string `json:"body"`
			HTMLURL     string `json:"html_url"`
			PublishedAt string `json:"published_at"`
			Prerelease  bool   `json:"prerelease"`
			Draft       bool   `json:"draft"`
		} `json:"release"`
		Metadata *releaseMetadata `json:"metadata"`
	}

	var releaseResp struct {
		Releases []releaseRecord `json:"releases"`
		Total    int             `json:"total"`
	}

	for attempt := 0; attempt < 2; attempt++ {
		apiURL := fmt.Sprintf("%s/releases?network=%s&limit=20", baseURL, network)
		if attempt == 0 && requestedClientType != "" {
			apiURL += fmt.Sprintf("&client_type=%s&client=%s", requestedClientType, requestedClientType)
			agent.logger.Info("Fetching latest release with client type filter", "network", network, "client_type", requestedClientType, "url", apiURL)
		} else {
			agent.logger.Info("Fetching latest release without client type filter", "network", network, "url", apiURL)
		}

		resp, err := http.Get(apiURL)
		if err != nil {
			agent.logger.Error("Failed to fetch release data", "error", err, "network", network)
			return nil, fmt.Errorf("failed to fetch release data: %w", err)
		}

		body, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			agent.logger.Error("Failed to read release response body", "error", err)
			return nil, fmt.Errorf("failed to read response body: %w", err)
		}

		releaseResp = struct {
			Releases []releaseRecord `json:"releases"`
			Total    int             `json:"total"`
		}{}

		if err := json.Unmarshal(body, &releaseResp); err != nil {
			agent.logger.Error("Failed to decode API response", "error", err, "network", network, "body", string(body))
			return nil, fmt.Errorf("failed to decode response: %w", err)
		}

		if resp.StatusCode != http.StatusOK {
			agent.logger.Error("API returned non-200 status", "status", resp.StatusCode, "network", network, "body", string(body))
			return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
		}

		if len(releaseResp.Releases) == 0 && attempt == 0 && requestedClientType != "" {
			agent.logger.Warn("No releases found for client type filter, retrying without filter", "network", network, "client_type", requestedClientType)
			continue
		}

		break
	}

	if len(releaseResp.Releases) == 0 {
		agent.logger.Warn("No releases found for network", "network", network, "client_type", clientType)
		return nil, fmt.Errorf("no releases found for network: %s with client_type: %s", network, clientType)
	}

	preferredRepos := agent.getDeploymentRepositories(ctx, network)
	preferredRepoSet := make(map[string]struct{}, len(preferredRepos))
	for _, repo := range preferredRepos {
		for _, candidate := range normalizedRepoCandidates(repo) {
			if candidate != "" {
				preferredRepoSet[candidate] = struct{}{}
			}
		}
	}

	preferredClientTypes := agent.getPreferredClientTypes(network)

	selectedRelease := &releaseResp.Releases[0]
	bestScore := -1

	for i := range releaseResp.Releases {
		rel := &releaseResp.Releases[i]
		metadataRepo := ""
		metadataClientType := ""
		if rel.Metadata != nil {
			metadataRepo = rel.Metadata.DockerRepo
			metadataClientType = rel.Metadata.ClientType
		}

		repoMatch := false
		for _, candidate := range normalizedRepoCandidates(metadataRepo) {
			if _, ok := preferredRepoSet[candidate]; ok {
				repoMatch = true
				break
			}
		}
		if !repoMatch {
			for _, candidate := range normalizedRepoCandidates(rel.Repository) {
				if _, ok := preferredRepoSet[candidate]; ok {
					repoMatch = true
					break
				}
			}
		}

		clientMatch := requestedClientType != "" && strings.EqualFold(metadataClientType, requestedClientType)
		preferredMatch := false
		if requestedClientType == "" {
			for _, pref := range preferredClientTypes {
				if strings.EqualFold(metadataClientType, pref) {
					preferredMatch = true
					break
				}
			}
		}

		score := 0
		if repoMatch {
			score += 4
		}
		if clientMatch {
			score += 2
		} else if preferredMatch {
			score += 1
		}

		if score > bestScore {
			bestScore = score
			selectedRelease = rel
		}
	}

	if bestScore <= 0 && requestedClientType != "" {
		agent.logger.Warn("No strong match found; falling back to first release for requested client",
			"network", network,
			"client_type", requestedClientType)
	}

	if selectedRelease == nil || selectedRelease.Release == nil {
		return nil, fmt.Errorf("release data is nil for network: %s", network)
	}

	repoParts := strings.Split(selectedRelease.Repository, "/")
	if len(repoParts) != 2 {
		return nil, fmt.Errorf("invalid repository format: %s", selectedRelease.Repository)
	}

	var displayName, networkName, metadataClientType, dockerTag string
	if selectedRelease.Metadata != nil {
		displayName = selectedRelease.Metadata.DisplayName
		networkName = selectedRelease.Metadata.NetworkName
		metadataClientType = selectedRelease.Metadata.ClientType
		dockerTag = selectedRelease.Metadata.DockerHubTag
	}

	if dockerTag == "" {
		dockerTag = selectedRelease.Release.TagName
	}

	return &NetworkReleaseInfo{
		Network: network,
		Repository: Repository{
			Owner:       repoParts[0],
			Name:        repoParts[1],
			DisplayName: displayName,
			NetworkName: networkName,
			ClientType:  metadataClientType,
			ReleaseTag:  selectedRelease.Release.TagName,
			DockerTag:   dockerTag,
		},
		Release: ReleaseInfo{
			TagName:     selectedRelease.Release.TagName,
			Name:        selectedRelease.Release.Name,
			Body:        selectedRelease.Release.Body,
			HTMLURL:     selectedRelease.Release.HTMLURL,
			PublishedAt: selectedRelease.Release.PublishedAt,
			Prerelease:  selectedRelease.Release.Prerelease,
			Draft:       selectedRelease.Release.Draft,
		},
	}, nil
}

func (agent *NodeOperatorAgent) getDeploymentRepositories(ctx context.Context, network string) []string {
	endpoint := fmt.Sprintf("%s/tools/analyze_current_deployment", agent.agentCoreURL)
	payload := map[string]string{"network": network}
	data, err := json.Marshal(payload)
	if err != nil {
		agent.logger.Warn("Failed to marshal deployment analysis payload", "error", err)
		return nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(data))
	if err != nil {
		agent.logger.Warn("Failed to create deployment analysis request", "error", err)
		return nil
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := agent.httpClient.Do(req)
	if err != nil {
		agent.logger.Warn("Deployment analysis request failed", "error", err)
		return nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		agent.logger.Warn("Deployment analysis returned non-200", "status", resp.StatusCode)
		return nil
	}

	var result struct {
		Success  bool `json:"success"`
		Analysis struct {
			Clients map[string]struct {
				Repo string `json:"repo"`
			} `json:"clients"`
		} `json:"analysis"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		agent.logger.Warn("Failed to decode deployment analysis response", "error", err)
		return nil
	}

	if !result.Success {
		agent.logger.Warn("Deployment analysis reported failure", "network", network)
		return nil
	}

	repoSet := make(map[string]struct{})
	for _, client := range result.Analysis.Clients {
		repo := strings.TrimSpace(client.Repo)
		if repo != "" && !strings.EqualFold(repo, "unknown") {
			if _, exists := repoSet[repo]; !exists {
				repoSet[repo] = struct{}{}
			}
		}
	}
	repos := make([]string, 0, len(repoSet))
	for repo := range repoSet {
		repos = append(repos, repo)
	}
	return repos
}

func normalizedRepoCandidates(repo string) []string {
	repo = strings.TrimSpace(strings.ToLower(repo))
	if repo == "" {
		return nil
	}
	if strings.HasPrefix(repo, "docker.io/") {
		repo = strings.TrimPrefix(repo, "docker.io/")
	}
	if strings.HasPrefix(repo, "ghcr.io/") {
		repo = strings.TrimPrefix(repo, "ghcr.io/")
	}
	repo = strings.TrimSuffix(repo, ":latest")
	if idx := strings.Index(repo, "@"); idx != -1 {
		repo = repo[:idx]
	}
	if idx := strings.Index(repo, ":"); idx != -1 {
		repo = repo[:idx]
	}
	if repo == "" {
		return nil
	}

	candidates := []string{repo}
	if parts := strings.Split(repo, "/"); len(parts) > 1 {
		name := parts[len(parts)-1]
		if name != "" {
			candidates = append(candidates, name)
		}
	}

	unique := make(map[string]struct{}, len(candidates))
	result := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		if _, exists := unique[candidate]; !exists {
			unique[candidate] = struct{}{}
			result = append(result, candidate)
		}
	}
	return result
}

func (agent *NodeOperatorAgent) getPreferredClientTypes(network string) []string {
	switch strings.ToLower(network) {
	case "ethereum":
		return []string{"execution", "node", "consensus"}
	case "polkadot", "kusama":
		return []string{"archive", "validator", "full", "rpc", "node", "parachain"}
	case "solana":
		return []string{"validator", "node"}
	default:
		return []string{"node", "execution", "validator", "consensus"}
	}
}
