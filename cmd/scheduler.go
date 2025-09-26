package main

import (
	"context"
	"log/slog"
	"time"

	humanize "github.com/dustin/go-humanize"
	"github.com/go-co-op/gocron/v2"
	"github.com/slack-go/slack"
	"github.com/uptrace/bun"
)

func createScheduleJobs(s gocron.Scheduler, db *bun.DB,
	logger *slog.Logger, botClient *slack.Client) error {

	_, err := s.NewJob(
		gocron.DurationJob(
			30*time.Minute,
		),
		gocron.NewTask(
			func() {

				ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
				defer cancel()

				type user struct {
					FirstName string    `json:"firstName,omitempty" bun:"firstName"`
					LastName  string    `json:"lastName,omitempty" bun:"lastName"`
					Email     string    `json:"email,omitempty"`
					CreatedAt time.Time `json:"createdAt,omitempty" bun:"createdAt"`
				}

				var users []user

				err := db.NewSelect().
					Where("createdAt >= NOW() - INTERVAL 30 MINUTE").
					Model(&users).
					Scan(ctx)
				if err != nil {
					logger.Error("could not fetch most recent users", "error", err.Error())
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

				_, _, err = botClient.PostMessage(
					"C05TT7LULP8", // hard coded but yeah
					slack.MsgOptionText(message, false),
					slack.MsgOptionAsUser(true),
				)
				if err != nil {
					logger.Error("could not post message to slack", "error", err.Error())
					return
				}
			},
		),
	)

	if err != nil {
		return err
	}

	_, err = s.NewJob(
		gocron.DurationJob(
			30*time.Minute,
		),
		gocron.NewTask(
			func() {

				ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
				defer cancel()

				type supportMessages struct {
					CreatedAt time.Time `json:"createdAt,omitempty" bun:"createdAt"`
					FullName  string    `json:"fullName" bun:"fullName"`
					Email     string    `json:"email" bun:"email"`
					Message   string    `json:"message" bun:"message"`

					bun.BaseModel `bun:"table:supportMessages" json:"-"`
				}

				var messages []supportMessages

				err := db.NewSelect().
					Where("createdAt >= NOW() - INTERVAL 30 MINUTE").
					Model(&messages).
					Scan(ctx)
				if err != nil {
					logger.Error("could not fetch most recent support messages", "error", err.Error())
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

				_, _, err = botClient.PostMessage(
					"C06GTAC99T9", // hard coded but yeah
					slack.MsgOptionText(message, false),
					slack.MsgOptionAsUser(true),
				)
				if err != nil {
					logger.Error("could not post message to slack", "error", err.Error())
					return
				}
			},
		))

	return err
}
