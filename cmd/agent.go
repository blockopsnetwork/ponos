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
