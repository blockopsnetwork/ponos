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

type NodeOperatorAgent struct {
	logger       *slog.Logger
	agentCoreURL string
	httpClient   *http.Client
	useAgentCore bool
}

type AgentSummary struct {
	DetectedNetworks    []string `json:"detected_networks"`
	Severity            string   `json:"severity"`
	Reasoning           string   `json:"reasoning"`
	ReleaseSummary      string   `json:"release_summary"`
	ConfigChangesNeeded string   `json:"config_changes_needed"`
	RiskAssessment      string   `json:"risk_assessment"`
	DockerTag           string   `json:"docker_tag"`
	PRTitle             string   `json:"pr_title"`
	Success             bool     `json:"success"`
	Error               string   `json:"error,omitempty"`
}

type YAMLAnalysisResult struct {
	BlockchainContainers []string `json:"blockchain_containers"`
	Reasoning            string   `json:"reasoning"`
	NetworkTypes         []string `json:"network_types"`
}

type NetworkReleaseInfo struct {
	Network    string      `json:"network"`
	Repository Repository  `json:"repository"`
	Release    ReleaseInfo `json:"release"`
}

type InfrastructureContext struct {
	DetectedClients    []DetectedClient `json:"detected_clients"`
	DeploymentType     string          `json:"deployment_type"`
	NetworkEnvironment string          `json:"network_environment"`
	ConfiguredImages   []string        `json:"configured_images"`
	Confidence         string          `json:"confidence"`
}

type DetectedClient struct {
	Repository   string `json:"repository"`
	CurrentTag   string `json:"current_tag"`
	ClientType   string `json:"client_type"`
	DockerImage  string `json:"docker_image"`
	FilePath     string `json:"file_path"`
	NetworkName  string `json:"network_name"`
}

type EnhancedUpgradeRequest struct {
	UserMessage        string                  `json:"user_message"`
	Intent            *UpgradeIntent          `json:"intent"`
	Infrastructure    *InfrastructureContext  `json:"infrastructure"`
	NeedsClarification bool                   `json:"needs_clarification"`
	ClarificationPrompt string                `json:"clarification_prompt,omitempty"`
	TargetClientType   string                `json:"target_client_type,omitempty"`
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
				assistantMessageID = fmt.Sprintf("msg_%d", time.Now().UnixNano())
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
	response = strings.TrimSpace(response)

	startIdx := strings.Index(response, "[")
	endIdx := strings.LastIndex(response, "]")

	if startIdx == -1 || endIdx == -1 || startIdx >= endIdx {
		agent.logger.Warn("Invalid JSON response from LLM", "response", response[:min(len(response), 200)])
		return []string{}
	}

	jsonStr := response[startIdx : endIdx+1]

	var repos []string
	jsonStr = strings.Trim(jsonStr, "[]")
	if jsonStr == "" {
		return []string{}
	}

	parts := strings.Split(jsonStr, ",")
	for _, part := range parts {
		repo := strings.Trim(strings.TrimSpace(part), `"`)
		if repo != "" {
			repos = append(repos, repo)
		}
	}

	return repos
}

type ConversationResponse struct {
	Content  string
	Finished bool
	Error    error
}

type StreamingUpdate struct {
	Type         string // "thinking", "tool_start", "tool_result", "assistant", "complete", "stream_append"
	Message      string
	Tool         string
	Success      bool
	Summary      string // Detailed results or error information from tool execution
	SessionID    string 
	CheckpointID string 
	MessageID    string 
	IsAppending  bool  
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

	responseLower := strings.ToLower(response)
	if strings.Contains(responseLower, `"requires_action": true`) {
		intent.RequiresAction = true
	}

	if strings.Contains(responseLower, `"network": "ethereum"`) {
		intent.Network = "ethereum"
	} else if strings.Contains(responseLower, `"network": "polkadot"`) {
		intent.Network = "polkadot"
	} else if strings.Contains(responseLower, `"network": "kusama"`) {
		intent.Network = "kusama"
	} else if strings.Contains(responseLower, `"network": "solana"`) {
		intent.Network = "solana"
	}

	userMessage := strings.ToLower(response)
	if strings.Contains(userMessage, "ethereum") || strings.Contains(userMessage, "geth") || 
	   strings.Contains(userMessage, "lighthouse") || strings.Contains(userMessage, "reth") ||
	   strings.Contains(userMessage, "besu") {
		intent.Network = "ethereum"
		intent.RequiresAction = true
		intent.ActionType = "upgrade"
	} else if strings.Contains(userMessage, "polkadot") || strings.Contains(userMessage, "dot") {
		intent.Network = "polkadot"
		intent.RequiresAction = true
		intent.ActionType = "upgrade"
	} else if strings.Contains(userMessage, "kusama") || strings.Contains(userMessage, "ksm") {
		intent.Network = "kusama"
		intent.RequiresAction = true
		intent.ActionType = "upgrade"
	}

	if strings.Contains(responseLower, `"action_type": "upgrade"`) {
		intent.ActionType = "upgrade"
	} else if strings.Contains(responseLower, `"action_type": "update"`) {
		intent.ActionType = "update"
	}

	if intent.Network != "unknown" {
		intent.Confidence = "high"
	}

	return intent
}

func (agent *NodeOperatorAgent) AnalyzeUpgradeRequestWithContext(ctx context.Context, userMessage string, configFiles []string) (*EnhancedUpgradeRequest, error) {
	intent, err := agent.ParseUpgradeIntent(ctx, userMessage)
	if err != nil {
		return nil, fmt.Errorf("failed to parse user intent: %w", err)
	}

	infrastructure, err := agent.AnalyzeCurrentInfrastructure(ctx, configFiles)
	if err != nil {
		agent.logger.Warn("Failed to analyze infrastructure, using empty context", "error", err)
		infrastructure = &InfrastructureContext{
			DetectedClients: []DetectedClient{},
			Confidence:     "low",
		}
	}

	request := &EnhancedUpgradeRequest{
		UserMessage:    userMessage,
		Intent:        intent,
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



func (agent *NodeOperatorAgent) extractImagesFromYAML(content string) []string {
	yamlOps := NewYAMLOperations()
	return yamlOps.ExtractImageReposFromYAML(content)
}

func (agent *NodeOperatorAgent) analyzeDockerImage(imageRef, filePath string) *DetectedClient {
	parts := strings.Split(imageRef, ":")
	if len(parts) != 2 {
		return nil
	}

	repository := parts[0]
	tag := parts[1]

	clientMappings := map[string]struct {
		ClientType     string
		Repository     string
		NetworkName    string
	}{
		"ethereum/client-go":    {ClientType: "execution", Repository: "ethereum/go-ethereum", NetworkName: "ethereum"},
		"sigp/lighthouse":       {ClientType: "consensus", Repository: "sigp/lighthouse", NetworkName: "ethereum"},
		"parity/polkadot":       {ClientType: "node", Repository: "paritytech/polkadot-sdk", NetworkName: "polkadot"},
		"chainsafe/lodestar":    {ClientType: "consensus", Repository: "ChainSafe/lodestar", NetworkName: "ethereum"},
		"statusim/nimbus-eth2":  {ClientType: "consensus", Repository: "status-im/nimbus-eth2", NetworkName: "ethereum"},
		"hyperledger/besu":      {ClientType: "execution", Repository: "hyperledger/besu", NetworkName: "ethereum"},
		"paradigmxyz/reth":      {ClientType: "execution", Repository: "paradigmxyz/reth", NetworkName: "ethereum"},
	}

	if clientInfo, exists := clientMappings[repository]; exists {
		return &DetectedClient{
			Repository:  clientInfo.Repository,
			CurrentTag:  tag,
			ClientType:  clientInfo.ClientType,
			DockerImage: imageRef,
			FilePath:    filePath,
			NetworkName: clientInfo.NetworkName,
		}
	}

	return nil
}

func (agent *NodeOperatorAgent) inferDeploymentType(clients []DetectedClient) string {
	if len(clients) == 0 {
		return "unknown"
	}

	clientTypes := make(map[string]bool)
	for _, client := range clients {
		clientTypes[client.ClientType] = true
	}

	if clientTypes["execution"] && clientTypes["consensus"] {
		return "validator"  
	} else if clientTypes["execution"] {
		return "fullnode" 
	} else if clientTypes["node"] {
		return "node"    
	}

	return "unknown"
}

func (agent *NodeOperatorAgent) inferNetworkEnvironment(clients []DetectedClient) string {
	for _, client := range clients {
		lowerPath := strings.ToLower(client.FilePath)
		lowerTag := strings.ToLower(client.CurrentTag)
		
		if strings.Contains(lowerPath, "testnet") || strings.Contains(lowerPath, "sepolia") || 
		   strings.Contains(lowerPath, "holesky") || strings.Contains(lowerTag, "testnet") {
			return "testnet"
		}
		if strings.Contains(lowerPath, "mainnet") || strings.Contains(lowerTag, "mainnet") {
			return "mainnet"
		}
	}
	return "unknown"
}

func (agent *NodeOperatorAgent) calculateConfidence(ctx *InfrastructureContext) string {
	if len(ctx.DetectedClients) == 0 {
		return "low"
	}
	if len(ctx.DetectedClients) >= 2 && ctx.DeploymentType != "unknown" {
		return "high"
	}
	if len(ctx.DetectedClients) >= 1 {
		return "medium"
	}
	return "low"
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
	apiURL := fmt.Sprintf("https://api.nodereleases.com/releases?network=%s&limit=20", network)
	if clientType != "" {
		apiURL += fmt.Sprintf("&client_type=%s", clientType)
		agent.logger.Info("Fetching latest release with client type filter", "network", network, "client_type", clientType, "url", apiURL)
	} else {
		agent.logger.Info("Fetching latest release without client type filter", "network", network, "url", apiURL)
	}

	resp, err := http.Get(apiURL)
	if err != nil {
		agent.logger.Error("Failed to fetch release data", "error", err, "network", network)
		return nil, fmt.Errorf("failed to fetch release data: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		agent.logger.Error("API returned non-200 status", "status", resp.StatusCode, "network", network)
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	var releaseResp struct {
		Releases []struct {
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
			Metadata *struct {
				ClientType  string `json:"client_type"`
				NetworkName string `json:"network_name"`
				DisplayName string `json:"display_name"`
			} `json:"metadata"`
		} `json:"releases"`
		Total int `json:"total"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&releaseResp); err != nil {
		agent.logger.Error("Failed to decode API response", "error", err, "network", network)
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	if len(releaseResp.Releases) == 0 {
		agent.logger.Warn("No releases found for network", "network", network, "client_type", clientType)
		return nil, fmt.Errorf("no releases found for network: %s with client_type: %s", network, clientType)
	}

	var selectedRelease *struct {
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
		Metadata *struct {
			ClientType  string `json:"client_type"`
			NetworkName string `json:"network_name"`
			DisplayName string `json:"display_name"`
		} `json:"metadata"`
	}

	if clientType != "" {
		selectedRelease = &releaseResp.Releases[0]
		clientTypeValue := "unknown"
		if selectedRelease.Metadata != nil {
			clientTypeValue = selectedRelease.Metadata.ClientType
		}
		agent.logger.Info("Selected release using API client type filter", 
			"repository", selectedRelease.Repository, 
			"client_type", clientTypeValue,
			"tag", selectedRelease.Release.TagName)
	} else {
		preferredClientTypes := agent.getPreferredClientTypes(network)
		agent.logger.Info("Looking for preferred client types", "network", network, "types", preferredClientTypes)

		for _, preferredType := range preferredClientTypes {
			for _, release := range releaseResp.Releases {
				if release.Metadata != nil && strings.EqualFold(release.Metadata.ClientType, preferredType) {
					selectedRelease = &release
					agent.logger.Info("Selected release with preferred client type", 
						"repository", release.Repository, 
						"client_type", release.Metadata.ClientType,
						"tag", release.Release.TagName)
					break
				}
			}
			if selectedRelease != nil {
				break
			}
		}

		if selectedRelease == nil {
			selectedRelease = &releaseResp.Releases[0]
			fallbackClientType := "unknown"
			if selectedRelease.Metadata != nil {
				fallbackClientType = selectedRelease.Metadata.ClientType
			}
			agent.logger.Warn("No preferred client type found, using first available", 
				"repository", selectedRelease.Repository,
				"client_type", fallbackClientType)
		}
	}

	if selectedRelease.Release == nil {
		return nil, fmt.Errorf("release data is nil for network: %s", network)
	}

	repoParts := strings.Split(selectedRelease.Repository, "/")
	if len(repoParts) != 2 {
		return nil, fmt.Errorf("invalid repository format: %s", selectedRelease.Repository)
	}

	return &NetworkReleaseInfo{
		Network: network,
		Repository: Repository{
			Owner:       repoParts[0],
			Name:        repoParts[1],
			DisplayName: selectedRelease.Metadata.DisplayName,
			NetworkName: selectedRelease.Metadata.NetworkName,
			ClientType:  selectedRelease.Metadata.ClientType,
			ReleaseTag:  selectedRelease.Release.TagName,
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

func (agent *NodeOperatorAgent) getFileContentFromConfig(ctx context.Context, filePath string) (string, error) {
	return "", fmt.Errorf("file content retrieval not implemented yet")
}

func (agent *NodeOperatorAgent) AnalyzeCurrentInfrastructure(ctx context.Context, configFiles []string) (*InfrastructureContext, error) {
	infraCtx := &InfrastructureContext{
		DetectedClients:    []DetectedClient{},
		DeploymentType:     "unknown",
		NetworkEnvironment: "unknown",
		ConfiguredImages:   []string{},
		Confidence:         "low",
	}

	for _, configFile := range configFiles {
		content, err := agent.getFileContentFromConfig(ctx, configFile)
		if err != nil {
			agent.logger.Warn("Failed to read config file", "file", configFile, "error", err)
			continue
		}

		images := agent.extractImagesFromYAML(content)
		infraCtx.ConfiguredImages = append(infraCtx.ConfiguredImages, images...)

		for _, imageRef := range images {
			if client := agent.analyzeDockerImage(imageRef, configFile); client != nil {
				infraCtx.DetectedClients = append(infraCtx.DetectedClients, *client)
			}
		}
	}

	infraCtx.DeploymentType = agent.inferDeploymentType(infraCtx.DetectedClients)
	infraCtx.NetworkEnvironment = agent.inferNetworkEnvironment(infraCtx.DetectedClients)
	infraCtx.Confidence = agent.calculateConfidence(infraCtx)

	return infraCtx, nil
}

func (agent *NodeOperatorAgent) getPreferredClientTypes(network string) []string {
	switch strings.ToLower(network) {
	case "ethereum":
		return []string{"execution", "node", "consensus"}
	case "polkadot", "kusama":
		return []string{"node", "parachain"}
	case "solana":
		return []string{"validator", "node"}
	default:
		return []string{"node", "execution", "validator", "consensus"}
	}
}
