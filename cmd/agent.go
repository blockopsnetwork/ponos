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

func NewNodeOperatorAgent(logger *slog.Logger) (*NodeOperatorAgent, error) {
	// Check if agent-core should be used (preferred)
	agentCoreURL := os.Getenv("AGENT_CORE_URL")
	if agentCoreURL == "" {
		agentCoreURL = "http://localhost:8001" // Default agent-core URL (updated port)
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

// callAgentCoreReleaseAnalysis sends release data to agent-core blockchain analysis endpoint
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

// callAgentCore sends a prompt to agent-core and returns the response content
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

				agent.logger.Info("Stream tool result", "tool", tool, "success", success)
				updates <- StreamingUpdate{
					Type:    "tool_result",
					Message: fmt.Sprintf("%s completed", tool),
					Tool:    tool,
					Success: success,
				}
			}

		case "assistant":
			agent.logger.Info("Stream assistant response", "message", message)
			updates <- StreamingUpdate{
				Type:    "assistant",
				Message: message,
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
	// Use new specialized agent-core blockchain analysis endpoint
	response, err := agent.callAgentCoreReleaseAnalysis(ctx, payload)
	if err != nil {
		agent.logger.Error("agent-core blockchain analysis failed", "error", err)
		return nil, err
	}

	return response, nil
}

func (agent *NodeOperatorAgent) AnalyzeYAMLForBlockchainContainers(ctx context.Context, yamlContent string) ([]string, error) {
	prompt := agent.buildYAMLAnalysisPrompt(yamlContent)

	// Use agent-core for AI analysis
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

// Removed parseLLMResponse and extractSection - migrated to agent-core blockchain.py

type ConversationResponse struct {
	Content  string
	Finished bool
	Error    error
}

type StreamingUpdate struct {
	Type         string // "thinking", "tool_start", "tool_result", "assistant", "complete"
	Message      string
	Tool         string
	Success      bool
	SessionID    string // For checkpoint management
	CheckpointID string // For checkpoint management
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
	agent.logger.Info("Using intelligent agent-core for real-time streaming with conversation history", "message", userMessage, "history_length", len(conversationHistory))

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
		// For now, we'll let the backend create/manage sessions
		// In the future, we could persist session_id locally
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
	prompt := fmt.Sprintf(`Analyze this user message for blockchain network upgrade intentions.

User Message: "%s"

Respond with JSON in this exact format:
{
  "requires_action": true/false,
  "network": "polkadot|kusama|ethereum|solana|other|unknown",
  "action_type": "upgrade|update|deploy|status|none",
  "confidence": "high|medium|low",
  "explanation": "brief explanation of what the user wants"
}

Guidelines:
- Set requires_action=true only if user clearly wants to upgrade/update a blockchain network
- Detect network from keywords like "polkadot", "kusama", "dot", "ksm", etc.
- Set confidence based on clarity of the request
- For greetings, questions, or general chat: requires_action=false`, userMessage)

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

	if strings.Contains(responseLower, `"network": "polkadot"`) {
		intent.Network = "polkadot"
	} else if strings.Contains(responseLower, `"network": "kusama"`) {
		intent.Network = "kusama"
	}

	if strings.Contains(responseLower, `"action_type": "upgrade"`) {
		intent.ActionType = "upgrade"
	} else if strings.Contains(responseLower, `"action_type": "update"`) {
		intent.ActionType = "update"
	}

	return intent
}

func (agent *NodeOperatorAgent) GetLatestNetworkRelease(ctx context.Context, network string) (*NetworkReleaseInfo, error) {
	// Fetch all releases for the network to filter properly
	apiURL := fmt.Sprintf("https://api.nodereleases.com/releases?network=%s&limit=20", network)

	agent.logger.Info("Fetching latest release", "network", network, "url", apiURL)

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
		agent.logger.Warn("No releases found for network", "network", network)
		return nil, fmt.Errorf("no releases found for network: %s", network)
	}

	// Filter releases to get appropriate client type based on network
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

	// Define preferred client types for each network
	preferredClientTypes := agent.getPreferredClientTypes(network)
	agent.logger.Info("Looking for preferred client types", "network", network, "types", preferredClientTypes)

	// Try to find a release with preferred client type
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

	// Fallback to first release if no preferred type found
	if selectedRelease == nil {
		selectedRelease = &releaseResp.Releases[0]
		agent.logger.Warn("No preferred client type found, using first available", 
			"repository", selectedRelease.Repository,
			"client_type", selectedRelease.Metadata.ClientType if selectedRelease.Metadata != nil else "unknown")
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

// getPreferredClientTypes returns ordered list of preferred client types for each network
func (agent *NodeOperatorAgent) getPreferredClientTypes(network string) []string {
	switch strings.ToLower(network) {
	case "ethereum":
		// For Ethereum, prefer execution clients as they're more commonly deployed in infrastructure
		// Consensus clients (Lighthouse, Nimbus, etc.) are typically paired with execution clients
		return []string{"execution", "node", "consensus"}
	case "polkadot", "kusama":
		// For Polkadot/Kusama, nodes are the primary client type
		return []string{"node", "parachain"}
	case "solana":
		// For Solana, validators are the main client type
		return []string{"validator", "node"}
	default:
		// Default preference order for unknown networks
		return []string{"node", "execution", "validator", "consensus"}
	}
}
