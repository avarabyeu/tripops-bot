package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/urfave/cli/v3"

	"github.com/avarabyeu/tripops-bot/internal/accommodation"
	"github.com/avarabyeu/tripops-bot/internal/activity"
	"github.com/avarabyeu/tripops-bot/internal/api"
	"github.com/avarabyeu/tripops-bot/internal/attachments"
	"github.com/avarabyeu/tripops-bot/internal/attention"
	"github.com/avarabyeu/tripops-bot/internal/checklists"
	"github.com/avarabyeu/tripops-bot/internal/dashboard"
	"github.com/avarabyeu/tripops-bot/internal/decisions"
	"github.com/avarabyeu/tripops-bot/internal/events"
	"github.com/avarabyeu/tripops-bot/internal/expenses"
	"github.com/avarabyeu/tripops-bot/internal/logistics"
	"github.com/avarabyeu/tripops-bot/internal/notify"
	"github.com/avarabyeu/tripops-bot/internal/scheduler"
	"github.com/avarabyeu/tripops-bot/internal/telegram"
	"github.com/avarabyeu/tripops-bot/internal/trips"
	"github.com/avarabyeu/tripops-bot/internal/users"
	"github.com/avarabyeu/tripops-bot/migrations"
)

func serveCommand() *cli.Command {
	return &cli.Command{
		Name:  "serve",
		Usage: "Run the API, the Telegram bot, the scheduler and the notification worker",
		Flags: []cli.Flag{
			&cli.IntFlag{
				Name:    "port",
				Usage:   "HTTP port",
				Sources: cli.EnvVars("PORT"),
			},
			&cli.BoolFlag{
				Name:  "skip-migrations",
				Usage: "do not apply pending migrations on start-up",
			},
		},
		Action: runServe,
	}
}

func runServe(ctx context.Context, cmd *cli.Command) error {
	application, closeDB, err := boot(ctx, cmd)
	if err != nil {
		return err
	}
	defer closeDB()

	cfg, log, database := application.cfg, application.log, application.database
	if port := cmd.Int("port"); port > 0 {
		cfg.Port = port
	}

	if !cmd.Bool("skip-migrations") {
		if err := migrations.Run(ctx, database, log); err != nil {
			return err
		}
	}

	// ------------------------------------------------------------ services --

	gdb := database.DB
	activityLog := activity.NewService(gdb, log)
	notifier := notify.NewService(gdb, log)
	usersSvc := users.NewService(gdb)

	tripsSvc := trips.NewService(database, usersSvc, activityLog, notifier, cfg.DeepLink)
	eventsSvc := events.NewService(database, tripsSvc, activityLog, notifier)
	decisionsSvc := decisions.NewService(database, tripsSvc, activityLog, notifier)
	logisticsSvc := logistics.NewService(database, tripsSvc, activityLog)
	accommodationSvc := accommodation.NewService(database, tripsSvc, activityLog)
	checklistsSvc := checklists.NewService(database, tripsSvc, activityLog)
	expensesSvc := expenses.NewService(database, tripsSvc, activityLog, notifier)
	attachmentsSvc := attachments.NewService(gdb)

	attentionEngine := attention.NewEngine(attention.Sources{
		Trips:         tripsSvc,
		Events:        eventsSvc.Repo(),
		Decisions:     decisionsSvc.Repo(),
		Vehicles:      logisticsSvc.Repo(),
		Accommodation: accommodationSvc.Repo(),
		Checklists:    checklistsSvc.Repo(),
	})
	dashboardSvc := dashboard.NewService(dashboard.Sources{
		Trips:         tripsSvc,
		Events:        eventsSvc.Repo(),
		Decisions:     decisionsSvc.Repo(),
		Vehicles:      logisticsSvc.Repo(),
		Accommodation: accommodationSvc.Repo(),
		Checklists:    checklistsSvc.Repo(),
		Expenses:      expensesSvc.Repo(),
		Attention:     attentionEngine,
	})

	// ----------------------------------------------------------------- API --

	server := api.New(cfg, log, api.Services{
		Users:         usersSvc,
		Trips:         tripsSvc,
		Events:        eventsSvc,
		Decisions:     decisionsSvc,
		Logistics:     logisticsSvc,
		Accommodation: accommodationSvc,
		Checklists:    checklistsSvc,
		Expenses:      expensesSvc,
		Attachments:   attachmentsSvc,
		Activity:      activityLog,
		Notify:        notifier,
		Dashboard:     dashboardSvc,
	}, database.Ping)

	root := http.NewServeMux()
	root.Handle("/", server.Handler())

	var wg sync.WaitGroup

	// ------------------------------------------------------------ Telegram --

	if cfg.BotConfigured() {
		client := telegram.NewClient(cfg.BotToken)
		bot := telegram.NewBot(cfg, log, client, telegram.Deps{
			Users:         usersSvc,
			Trips:         tripsSvc,
			Events:        eventsSvc,
			Decisions:     decisionsSvc,
			Logistics:     logisticsSvc,
			Accommodation: accommodationSvc,
			Checklists:    checklistsSvc,
			Expenses:      expensesSvc,
			Dashboard:     dashboardSvc,
		})

		if me, err := client.GetMe(ctx); err != nil {
			log.Warn("could not reach Telegram", "err", err)
		} else {
			log.Info("telegram bot ready", "username", me.Username)
			// Invite links are built from the configured username, so a typo
			// here produces links that 404 in Telegram and nothing else that
			// looks wrong. Telegram knows the real one; compare them.
			switch {
			case cfg.BotUsername == "":
				log.Warn("TELEGRAM_BOT_USERNAME is not set: invite links cannot be built",
					"expected", me.Username)
			case !strings.EqualFold(cfg.BotUsername, me.Username):
				log.Warn("TELEGRAM_BOT_USERNAME does not match this bot: invite links will not work",
					"configured", cfg.BotUsername, "actual", me.Username)
			}
		}
		if err := bot.Setup(ctx); err != nil {
			log.Warn("telegram setup failed", "err", err)
		}

		if cfg.BotMode == "webhook" {
			root.Handle(telegram.WebhookPath, bot.WebhookHandler(log))
			log.Info("telegram webhook registered", "path", telegram.WebhookPath)
		} else {
			wg.Go(func() {
				log.Info("telegram long polling started")
				bot.RunPolling(ctx)
			})
		}

		deliverer := telegram.NewDeliverer(client, notifier, log, cfg.MiniAppURL)
		wg.Go(func() { deliverer.Run(ctx, cfg.NotifyInterval) })
	} else {
		log.Warn("TELEGRAM_BOT_TOKEN is not set: the bot and notifications are disabled")
	}

	// ----------------------------------------------------------- scheduler --

	sched := scheduler.New(scheduler.Deps{
		Trips:         tripsSvc,
		Decisions:     decisionsSvc,
		Events:        eventsSvc.Repo(),
		Checklists:    checklistsSvc.Repo(),
		Accommodation: accommodationSvc.Repo(),
		Notify:        notifier,
	}, log)
	wg.Go(func() { sched.Run(ctx, cfg.SchedulerInterval) })

	// -------------------------------------------------------------- listen --

	httpServer := &http.Server{
		Addr:              fmt.Sprintf(":%d", cfg.Port),
		Handler:           root,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       90 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		log.Info("http server listening", "addr", httpServer.Addr, "env", cfg.Env)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
		}
	}()

	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
		log.Info("shutting down")
	}

	// Stop accepting requests first, then wait for the workers to notice the
	// cancelled context.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Warn("http shutdown", "err", err)
	}
	wg.Wait()
	log.Info("bye")
	return nil
}
