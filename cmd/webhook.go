package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"

	"github.com/blockops-sh/ponos/config"
)

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

type WebhookHandler struct {
	bot                  *Bot
	AgentFeedbackChannel string
}

func NewWebhookHandler(bot *Bot) *WebhookHandler {
	return &WebhookHandler{
		bot:                  bot,
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

	summary, err := wh.bot.ProcessReleaseUpdate(r.Context(), payload)
	if err != nil {
		wh.bot.logger.Error("agent processing failed", "error", err)
	} else {

		go func() {
			ctx := context.Background()
			if len(wh.bot.config.Projects) == 0 {
				wh.bot.logger.Error("no projects configured")
				return
			}
			prURL, err := wh.bot.githubHandler.agentUpdatePR(ctx, payload, summary, &config.ProjectConfig{Projects: wh.bot.config.Projects})
			if err != nil {
				wh.bot.logger.Error("Agent failed to create PR", "error", err)
				wh.bot.sendReleaseSummaryFromAgent(wh.AgentFeedbackChannel, payload, summary)
			} else {
				wh.bot.logger.Info("PR created", "url", prURL)
				wh.bot.sendReleaseSummaryFromAgent(wh.AgentFeedbackChannel, payload, summary, prURL)
			}
		}()
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status": "received", "processed": true}`))
}
