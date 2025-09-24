package main

import (
	"encoding/json"
	"io"
	"net/http"
)

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type ReleasesWebhookPayload struct {
	EventType    string                         `json:"event_type"`
	Username     string                         `json:"username"`
	Timestamp    string                         `json:"timestamp"`
	Repositories []Repository                   `json:"repositories"`
	Releases     map[string]ReleaseInfo        `json:"releases"`
}

type Repository struct {
	Owner       string `json:"owner"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	NetworkKey  string `json:"network_key"`
	NetworkName string `json:"network_name"`
	ReleaseTag  string `json:"release_tag"`
	ClientType  string `json:"client_type"`
}

type ReleaseInfo struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Body        string `json:"body"`
	HTMLURL     string `json:"html_url"`
	PublishedAt string `json:"published_at"`
	Prerelease  bool   `json:"prerelease"`
	Draft       bool   `json:"draft"`
}

type WebhookHandler struct {
	bot   *Bot
	agent *NodeOperatorAgent
}

func NewWebhookHandler(bot *Bot) *WebhookHandler {
	agent, err := NewNodeOperatorAgent(bot.logger)
	if err != nil {
		bot.logger.Error("failed to create AI agent", "error", err)
		// continue without agent for now
		agent = nil
	}
	
	return &WebhookHandler{
		bot:   bot,
		agent: agent,
	}
}

func (wh *WebhookHandler) handleReleasesWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		wh.bot.logger.Error("failed to read webhook body", "error", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	
	var payload ReleasesWebhookPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		wh.bot.logger.Error("failed to parse webhook payload", 
			"error", err, 
			"body_length", len(body),
			"body_preview", string(body[:min(200, len(body))]))
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	wh.bot.logger.Info("received releases webhook",
		"event_type", payload.EventType,
		"username", payload.Username,
		"timestamp", payload.Timestamp,
		"repositories_count", len(payload.Repositories),
		"releases_count", len(payload.Releases))

	for _, repo := range payload.Repositories {
		wh.bot.logger.Info("repository in payload",
			"owner", repo.Owner,
			"name", repo.Name,
			"display_name", repo.DisplayName,
			"network", repo.NetworkName,
			"client_type", repo.ClientType)
	}

	for key, release := range payload.Releases {
		wh.bot.logger.Info("release in payload",
			"key", key,
			"tag_name", release.TagName,
			"name", release.Name,
			"prerelease", release.Prerelease,
			"published_at", release.PublishedAt)
	}

	// Process with agent if available
	if wh.agent != nil {
		decision, err := wh.agent.ProcessReleaseUpdate(r.Context(), payload)
		if err != nil {
			wh.bot.logger.Error("AI agent processing failed", "error", err)
		} else {
			wh.bot.logger.Info("AI agent decision",
				"should_update", decision.ShouldUpdate,
				"severity", decision.Severity,
				"update_strategy", decision.UpdateStrategy,
				"detected_networks", decision.DetectedNetworks,
				"reasoning", decision.Reasoning)
		}
	} else {
		wh.bot.logger.Info("AI agent not available, skipping processing")
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "received", "processed": true}`))
}
