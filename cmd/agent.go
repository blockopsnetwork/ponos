package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	
	"github.com/tmc/langchaingo/llms"
	"github.com/tmc/langchaingo/llms/openai"
)

type NodeOperatorAgent struct {
	llm    llms.LLM
	logger *slog.Logger
}

// decision about whether or not to make a release update
type UpdateDecision struct {
	ShouldUpdate     bool     `json:"should_update"`
	DetectedNetworks []string `json:"detected_networks"`
	Severity         string   `json:"severity"`
	UpdateStrategy   string   `json:"update_strategy"`
	Reasoning        string   `json:"reasoning"`
}

func NewNodeOperatorAgent(logger *slog.Logger) (*NodeOperatorAgent, error) {
	apiKey := os.Getenv("OPENAI_API_KEY")
	if apiKey == "" {
		// Boilerplate: Return agent without LLM for testing
		return &NodeOperatorAgent{
			llm:    nil,
			logger: logger,
		}, nil
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

// analyzes a release webhook and decides what actions to take
func (agent *NodeOperatorAgent) ProcessReleaseUpdate(ctx context.Context, payload ReleasesWebhookPayload) (*UpdateDecision, error) {
	if agent.llm == nil {
		agent.logger.Info("No LLM available, using fallback decision")
		decision := &UpdateDecision{
			ShouldUpdate:     true,
			DetectedNetworks: []string{"unknown"},
			Severity:         "medium",
			UpdateStrategy:   "create_pr",
			Reasoning:        "Fallback decision - no OpenAI API key configured",
		}
		return decision, nil
	}

	prompt := agent.buildAnalysisPrompt(payload)
	
	agent.logger.Info("Calling OpenAI for release analysis")
	response, err := agent.llm.GenerateContent(ctx, []llms.MessageContent{
		llms.TextParts(llms.ChatMessageTypeHuman, prompt),
	})
	if err != nil {
		agent.logger.Error("OpenAI call failed", "error", err)
		return nil, err
	}

	agent.logger.Info("OpenAI response received", "response_length", len(response.Choices[0].Content))
	
	// Parse the LLM response (for now, extract key info manually)
	llmResponse := response.Choices[0].Content
	decision := agent.parseLLMResponse(llmResponse, payload)
	
	agent.logger.Info("AI agent processed release",
		"event_type", payload.EventType,
		"should_update", decision.ShouldUpdate,
		"severity", decision.Severity,
		"detected_networks", decision.DetectedNetworks)
	
	return decision, nil
}

func (agent *NodeOperatorAgent) buildAnalysisPrompt(payload ReleasesWebhookPayload) string {
	var repoInfo, releaseInfo string
	
	for _, repo := range payload.Repositories {
		repoInfo += fmt.Sprintf("Repository: %s/%s (%s)\nNetwork: %s\nClient Type: %s\n", 
			repo.Owner, repo.Name, repo.DisplayName, repo.NetworkName, repo.ClientType)
	}
	
	for key, release := range payload.Releases {
		releaseInfo += fmt.Sprintf("Release: %s\nTag: %s\nName: %s\nPrerelease: %t\nPublished: %s\n", 
			key, release.TagName, release.Name, release.Prerelease, release.PublishedAt)
	}
	
	prompt := fmt.Sprintf(`You are NodeOperator.ai, an expert blockchain infrastructure AI agent.

RELEASE INFORMATION:
%s

REPOSITORY INFORMATION:
%s

TASK: Analyze this blockchain node release and make a decision.

Respond with your analysis covering:
1. Should we update? (yes/no and why)
2. Which blockchain network is this for?
3. Severity level (low/medium/high/critical)
4. Update strategy (create_pr/immediate/wait)
5. Key reasoning

Keep response concise but informative.`, releaseInfo, repoInfo)

	return prompt
}

func (agent *NodeOperatorAgent) parseLLMResponse(response string, payload ReleasesWebhookPayload) *UpdateDecision {
	shouldUpdate := strings.Contains(strings.ToLower(response), "yes") || 
					strings.Contains(strings.ToLower(response), "update")
	
	severity := "medium"
	responseLower := strings.ToLower(response)
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
	
	return &UpdateDecision{
		ShouldUpdate:     shouldUpdate,
		DetectedNetworks: detectedNetworks,
		Severity:         severity,
		UpdateStrategy:   "create_pr",
		Reasoning:        response[:min(len(response), 300)], // Truncate for logging
	}
}
