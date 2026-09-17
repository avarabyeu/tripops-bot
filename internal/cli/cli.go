// Package cli defines the tripops command line.
//
// Commands are thin: they load configuration, open the database, build the
// application and hand over. Anything a command does that is worth testing
// lives in a domain package, not here.
package cli

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"

	"github.com/urfave/cli/v3"

	"github.com/avarabyeu/tripops-bot/internal/config"
	"github.com/avarabyeu/tripops-bot/internal/db"
)

// version is stamped at build time with -ldflags "-X …=v1.2.3"; it falls back
// to the module build info so a `go install` still reports something useful.
var version = ""

// New builds the root command.
func New() *cli.Command {
	return &cli.Command{
		Name:                  "tripops",
		Usage:                 "Group trip coordinator for Telegram",
		Version:               buildVersion(),
		EnableShellCompletion: true,
		DefaultCommand:        "serve",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:    "log-level",
				Usage:   "log verbosity: debug, info, warn, error",
				Sources: cli.EnvVars("LOG_LEVEL"),
			},
			&cli.StringFlag{
				Name:    "database-url",
				Usage:   "PostgreSQL DSN or SQLite path (postgres://… | sqlite://tripops.db)",
				Sources: cli.EnvVars("DATABASE_URL"),
			},
		},
		Commands: []*cli.Command{
			serveCommand(),
			migrateCommand(),
			botCommand(),
		},
	}
}

// app is everything a command needs after the common start-up work.
type app struct {
	cfg      config.Config
	log      *slog.Logger
	database *db.DB
}

// boot loads configuration, opens the database and returns a closer. Every
// command that touches data starts with it.
func boot(ctx context.Context, cmd *cli.Command) (*app, func(), error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, err
	}
	// Flags win over the environment the config loader read.
	if level := cmd.Root().String("log-level"); level != "" {
		cfg.LogLevel = level
	}
	if url := cmd.Root().String("database-url"); url != "" {
		cfg.DatabaseURL = url
	}

	log := newLogger(cfg)
	database, err := db.Open(ctx, db.Options{
		URL:      cfg.DatabaseURL,
		MaxConns: int(cfg.DatabaseMaxConn),
		Log:      log,
		Debug:    cfg.LogLevel == "debug",
	})
	if err != nil {
		return nil, nil, err
	}
	log.Info("database connected", "engine", database.Dialect())

	return &app{cfg: cfg, log: log, database: database}, func() {
		if err := database.Close(); err != nil {
			log.Warn("closing database", "err", err)
		}
	}, nil
}

func newLogger(cfg config.Config) *slog.Logger {
	level := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	}
	opts := &slog.HandlerOptions{Level: level}
	if cfg.IsDevelopment() {
		return slog.New(slog.NewTextHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewJSONHandler(os.Stdout, opts))
}

func buildVersion() string {
	if version != "" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		var revision, modified string
		for _, setting := range info.Settings {
			switch setting.Key {
			case "vcs.revision":
				revision = setting.Value
			case "vcs.modified":
				modified = setting.Value
			}
		}
		if revision != "" {
			if len(revision) > 12 {
				revision = revision[:12]
			}
			if modified == "true" {
				return revision + "-dirty"
			}
			return revision
		}
	}
	return "dev"
}

// errUsage reports a mistake in how a command was invoked.
func errUsage(format string, args ...any) error {
	return fmt.Errorf(format, args...)
}
