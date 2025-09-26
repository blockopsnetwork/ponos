package main

import (
	"context"
	"log/slog"
	"time"

	"github.com/blockops-sh/ponos/config"
	humanize "github.com/dustin/go-humanize"
	"github.com/go-co-op/gocron/v2"
	"github.com/slack-go/slack"
	"github.com/uptrace/bun"
)

type User struct {
	FirstName string    `json:"firstName,omitempty" bun:"firstName"`
	LastName  string    `json:"lastName,omitempty" bun:"lastName"`
	Email     string    `json:"email,omitempty"`
	CreatedAt time.Time `json:"createdAt,omitempty" bun:"createdAt"`
}

type SupportMessage struct {
	CreatedAt time.Time `json:"createdAt,omitempty" bun:"createdAt"`
	FullName  string    `json:"fullName" bun:"fullName"`
	Email     string    `json:"email" bun:"email"`
	Message   string    `json:"message" bun:"message"`
	bun.BaseModel `bun:"table:supportMessages" json:"-"`
}

func createScheduleJobs(s gocron.Scheduler, db *bun.DB, logger *slog.Logger, botClient *slack.Client) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}

	// Create users report job
	if _, err := s.NewJob(
		gocron.DurationJob(30*time.Minute),
		gocron.NewTask(func() { createUsersReport(db, logger, botClient, cfg.UsersReportChannel) }),
	); err != nil {
		return err
	}

	// Create support messages report job
	_, err = s.NewJob(
		gocron.DurationJob(30*time.Minute),
		gocron.NewTask(func() { createSupportReport(db, logger, botClient, cfg.SupportReportChannel) }),
	)

	return err
}

func createUsersReport(db *bun.DB, logger *slog.Logger, botClient *slack.Client, channel string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var users []User
	err := db.NewSelect().
		Where("createdAt >= NOW() - INTERVAL 30 MINUTE").
		Model(&users).
		Scan(ctx)
	if err != nil {
		logger.Error("could not fetch recent users", "error", err)
		return
	}

	if len(users) == 0 {
		logger.Info("no new users in the last 30 minutes")
		return
	}

	message := "*New Users Report* - Last 30 minutes\n\n"
	for i, user := range users {
		message += "• *" + user.FirstName + " " + user.LastName + "*\n"
		message += "  📧 " + user.Email + "\n"
		message += "  📅 " + humanize.Time(user.CreatedAt) + "\n"
		if i < len(users)-1 {
			message += "\n"
		}
	}

	if _, _, err := botClient.PostMessage(channel, slack.MsgOptionText(message, false), slack.MsgOptionAsUser(true)); err != nil {
		logger.Error("could not post users report to slack", "error", err, "channel", channel)
	}
}

func createSupportReport(db *bun.DB, logger *slog.Logger, botClient *slack.Client, channel string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var messages []SupportMessage
	err := db.NewSelect().
		Where("createdAt >= NOW() - INTERVAL 30 MINUTE").
		Model(&messages).
		Scan(ctx)
	if err != nil {
		logger.Error("could not fetch recent support messages", "error", err)
		return
	}

	if len(messages) == 0 {
		logger.Info("no new support messages in the last 30 minutes")
		return
	}

	message := "*New Support Messages Report* - Last 30 minutes\n\n"
	for i, msg := range messages {
		message += "• *" + msg.FullName + "*\n"
		message += "  📧 " + msg.Email + "\n"
		message += "  📅 " + humanize.Time(msg.CreatedAt) + "\n"
		message += "  💬 " + msg.Message + "\n"
		if i < len(messages)-1 {
			message += "\n"
		}
	}

	if _, _, err := botClient.PostMessage(channel, slack.MsgOptionText(message, false), slack.MsgOptionAsUser(true)); err != nil {
		logger.Error("could not post support report to slack", "error", err, "channel", channel)
	}
}
