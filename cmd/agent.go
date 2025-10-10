package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/tmc/langchaingo/llms/openai"
)

type NodeOperatorAgent struct {
	llm    *openai.LLM
	logger *slog.Logger
}

type AgentSummary struct {
	DetectedNetworks    []string `json:"detected_networks"`
	Severity            string   `json:"severity"`
	Reasoning           string   `json:"reasoning"`
	ReleaseSummary      string   `json:"release_summary"`
	ConfigChangesNeeded string   `json:"config_changes_needed"`
	RiskAssessment      string   `json:"risk_assessment"`
}

type YAMLAnalysisResult struct {
	BlockchainContainers []string `json:"blockchain_containers"`
	Reasoning           string   `json:"reasoning"`
	NetworkTypes        []string `json:"network_types"`
}

func NewNodeOperatorAgent(logger *slog.Logger) (*NodeOperatorAgent, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		return nil, fmt.Errorf("OPENAI_API_KEY environment variable is required")
	}

	llm, err := openai.New(openai.WithToken(apiKey))
	if err != nil {
		return nil, err
	}

	return &NodeOperatorAgent{
		llm:    llm,
		logger: logger,
	}, nil
}

func (agent *NodeOperatorAgent) ProcessReleaseUpdate(ctx context.Context, payload ReleasesWebhookPayload) (*AgentSummary, error) {
	prompt := BuildAIAnalysisPrompt(payload)

	response, err := agent.llm.Call(ctx, prompt)
	if err != nil {
		agent.logger.Error("OpenAI call failed", "error", err)
		return nil, err
	}

	llmResponse := response
	decision := agent.parseLLMResponse(llmResponse, payload)

	return decision, nil
}

func (agent *NodeOperatorAgent) AnalyzeYAMLForBlockchainContainers(ctx context.Context, yamlContent string) ([]string, error) {
	prompt := agent.buildYAMLAnalysisPrompt(yamlContent)
	
	response, err := agent.llm.Call(ctx, prompt)
	if err != nil {
		agent.logger.Error("LLM YAML analysis failed", "error", err)
		return nil, err
	}
	
	repos := agent.parseYAMLAnalysisResponse(response)
	agent.logger.Info("LLM YAML analysis completed", "containers_found", len(repos))
	
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

func (agent *NodeOperatorAgent) parseLLMResponse(response string, payload ReleasesWebhookPayload) *AgentSummary {
	responseLower := strings.ToLower(response)

	severity := "medium"
	if strings.Contains(responseLower, "critical") {
		severity = "critical"
	} else if strings.Contains(responseLower, "high") {
		severity = "high"
	} else if strings.Contains(responseLower, "low") {
		severity = "low"
	}

	var detectedNetworks []string
	for _, repo := range payload.Repositories {
		if repo.NetworkName != "" {
			detectedNetworks = append(detectedNetworks, strings.ToLower(repo.NetworkName))
		}
	}
	if len(detectedNetworks) == 0 {
		detectedNetworks = []string{"unknown"}
	}

	releaseSummary := agent.extractSection(response, "RELEASE SUMMARY", "NETWORK IDENTIFICATION")
	configChanges := agent.extractSection(response, "CONFIGURATION CHANGES", "RISK ASSESSMENT")
	riskAssessment := agent.extractSection(response, "RISK ASSESSMENT", "")

	return &AgentSummary{
		DetectedNetworks:    detectedNetworks,
		Severity:            severity,
		Reasoning:           response[:min(len(response), 500)],
		ReleaseSummary:      releaseSummary,
		ConfigChangesNeeded: configChanges,
		RiskAssessment:      riskAssessment,
	}
}

func (agent *NodeOperatorAgent) extractSection(text, startSection, endSection string) string {
	startIdx := strings.Index(strings.ToUpper(text), strings.ToUpper(startSection))
	if startIdx == -1 {
		return "Not specified"
	}

	startIdx = strings.Index(text[startIdx:], ":") + startIdx + 1
	if startIdx == 0 {
		return "Not specified"
	}

	endIdx := strings.Index(strings.ToUpper(text[startIdx:]), strings.ToUpper(endSection))
	if endIdx == -1 {
		content := strings.TrimSpace(text[startIdx:])
		if len(content) > 300 {
			content = content[:300] + "..."
		}
		return content
	}

	content := strings.TrimSpace(text[startIdx : startIdx+endIdx])
	return content
}

type ConversationResponse struct {
	Content   string
	Finished  bool
	Error     error
}

func (agent *NodeOperatorAgent) ProcessConversation(ctx context.Context, userMessage string) (*ConversationResponse, error) {
	agent.logger.Info("ProcessConversation called", "message", userMessage)
	
	prompt := agent.buildConversationPrompt(userMessage)
	agent.logger.Info("Built conversation prompt", "prompt_length", len(prompt))
	
	agent.logger.Info("Making LLM call")
	response, err := agent.llm.Call(ctx, prompt)
	if err != nil {
		agent.logger.Error("AI conversation failed", "error", err)
		return &ConversationResponse{
			Error: err,
			Finished: true,
		}, err
	}
	
	agent.logger.Info("LLM call successful", "response_length", len(response))
	return &ConversationResponse{
		Content:  response,
		Finished: true,
	}, nil
}

func (agent *NodeOperatorAgent) buildConversationPrompt(userMessage string) string {
	return fmt.Sprintf(`You are Ponos, an AI blockchain operations assistant with ACTUAL execution capabilities. You specialize in:

🔗 AVAILABLE TOOLS & ACTIONS:
- Upgrade Polkadot/Kusama networks to latest releases
- Upgrade Ethereum execution clients (geth, besu, etc.)
- Update Kubernetes deployment manifests automatically
- Create GitHub pull requests for infrastructure changes
- Send Slack notifications for status updates
- Execute blockchain network upgrades immediately

🎯 BEHAVIOR - BE ACTION-ORIENTED:
- When users request upgrades, TAKE ACTION immediately
- Don't ask for clarification on standard upgrade requests
- Be confident: "I'll upgrade [network] now" not "Could you provide more details?"
- Explain what you're doing while doing it
- You have the tools and authority to execute blockchain operations

💻 EXECUTION EXAMPLES:
User: "upgrade polkadot"
You: "I'll upgrade Polkadot to the latest release now. Starting the upgrade process..."

User: "new geth release, upgrade ethereum nodes"  
You: "I'll upgrade your Ethereum execution nodes to the latest geth release. Initiating Kubernetes manifest updates..."

User: "hello, what can you do?"
You: "Hi! I'm Ponos, your blockchain operations assistant. I can actually upgrade networks like Polkadot, Kusama, and Ethereum - just tell me what needs upgrading!"

📋 UPGRADE RECOGNITION:
- "upgrade [network]" = EXECUTE upgrade immediately  
- "new release for [client]" = EXECUTE upgrade for that client
- "update [network] nodes" = EXECUTE network update
- General conversation = Be helpful and highlight capabilities

User Message: %s

Response:`, userMessage)
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
	
	response, err := agent.llm.Call(ctx, prompt)
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
