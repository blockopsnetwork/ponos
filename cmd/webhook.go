package main

import (
	"context"
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

type WebhookHandler struct {
	bot                  *Bot
	agent                *NodeOperatorAgent
	AgentFeedbackChannel string
}

func NewWebhookHandler(bot *Bot) *WebhookHandler {
	return &WebhookHandler{
		bot:                  bot,
		agent:                bot.agent,
		AgentFeedbackChannel: "sre-tasks",
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

	wh.bot.logger.Info("Processing release webhook",
		"repositories", len(payload.Repositories),
		"releases", len(payload.Releases))

	if wh.agent != nil {
		summary, err := wh.agent.ProcessReleaseUpdate(r.Context(), payload)
		if err != nil {
			wh.bot.logger.Error("agent processing failed", "error", err)
		} else {

			go func() {
				ctx := context.Background()
				prURL, err := wh.bot.githubHandler.agentUpdatePR(ctx, payload, summary)
				if err != nil {
					wh.bot.logger.Error("Agent failed to create PR", "error", err)
					wh.bot.sendReleaseSummaryFromAgent(wh.AgentFeedbackChannel, payload, summary)
				} else {
					wh.bot.logger.Info("PR created", "url", prURL)
					wh.bot.sendReleaseSummaryFromAgent(wh.AgentFeedbackChannel, payload, summary, prURL)
				}
			}()
		}
	} else {
		wh.bot.logger.Info("nodeagent not available, skipping processing")
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "received", "processed": true}`))
}
