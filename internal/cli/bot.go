package cli

import (
	"context"
	"fmt"

	"github.com/urfave/cli/v3"

	"github.com/avarabyeu/tripops-bot/internal/config"
	"github.com/avarabyeu/tripops-bot/internal/telegram"
)

func botCommand() *cli.Command {
	return &cli.Command{
		Name:  "bot",
		Usage: "Telegram bot administration",
		Commands: []*cli.Command{
			{
				Name:   "info",
				Usage:  "Show the bot account this token belongs to",
				Action: runBotInfo,
			},
			{
				Name:  "set-webhook",
				Usage: "Point Telegram at this deployment",
				Flags: []cli.Flag{
					&cli.StringFlag{
						Name:    "url",
						Usage:   "public base URL, e.g. https://tripops.example.com",
						Sources: cli.EnvVars("TELEGRAM_WEBHOOK_URL"),
					},
				},
				Action: runSetWebhook,
			},
			{
				Name:   "delete-webhook",
				Usage:  "Stop webhook delivery and allow long polling again",
				Action: runDeleteWebhook,
			},
			{
				Name:   "set-commands",
				Usage:  "Publish the command menu shown in Telegram",
				Action: runSetCommands,
			},
		},
	}
}

// botClient builds a Telegram client without touching the database: none of
// these commands need it.
func botClient() (*telegram.Client, config.Config, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, cfg, err
	}
	if !cfg.BotConfigured() {
		return nil, cfg, errUsage("TELEGRAM_BOT_TOKEN is not set")
	}
	return telegram.NewClient(cfg.BotToken), cfg, nil
}

func runBotInfo(ctx context.Context, _ *cli.Command) error {
	client, _, err := botClient()
	if err != nil {
		return err
	}
	me, err := client.GetMe(ctx)
	if err != nil {
		return err
	}
	fmt.Printf("id:       %d\nusername: @%s\nname:     %s\n", me.ID, me.Username, me.FirstName)
	return nil
}

func runSetWebhook(ctx context.Context, cmd *cli.Command) error {
	client, cfg, err := botClient()
	if err != nil {
		return err
	}
	base := cmd.String("url")
	if base == "" {
		base = cfg.WebhookURL
	}
	if base == "" {
		return errUsage("pass --url or set TELEGRAM_WEBHOOK_URL")
	}
	if cfg.WebhookSecret == "" {
		return errUsage("TELEGRAM_WEBHOOK_SECRET must be set: it is what proves an update came from Telegram")
	}
	target := base + telegram.WebhookPath
	if err := client.SetWebhook(ctx, target, cfg.WebhookSecret); err != nil {
		return err
	}
	fmt.Printf("webhook set to %s\n", target)
	return nil
}

func runDeleteWebhook(ctx context.Context, _ *cli.Command) error {
	client, _, err := botClient()
	if err != nil {
		return err
	}
	if err := client.DeleteWebhook(ctx); err != nil {
		return err
	}
	fmt.Println("webhook deleted; long polling can take over")
	return nil
}

func runSetCommands(ctx context.Context, _ *cli.Command) error {
	client, _, err := botClient()
	if err != nil {
		return err
	}
	if err := client.SetMyCommands(ctx, telegram.Commands()); err != nil {
		return err
	}
	for _, c := range telegram.Commands() {
		fmt.Printf("/%s — %s\n", c.Command, c.Description)
	}
	return nil
}
