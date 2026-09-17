// Package testsupport builds a complete, real TripOps application against a
// throwaway database.
//
// Integration tests here use SQLite by default, so `go test ./...` needs no
// Docker, no Postgres and no fixtures. Point TEST_DATABASE_URL at a PostgreSQL
// instance to run exactly the same tests against the production engine — that
// is the whole reason the schema and the repositories stay dialect-neutral.
package testsupport

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/avarabyeu/tripops-bot/internal/accommodation"
	"github.com/avarabyeu/tripops-bot/internal/activity"
	"github.com/avarabyeu/tripops-bot/internal/api"
	"github.com/avarabyeu/tripops-bot/internal/attachments"
	"github.com/avarabyeu/tripops-bot/internal/attention"
	"github.com/avarabyeu/tripops-bot/internal/checklists"
	"github.com/avarabyeu/tripops-bot/internal/config"
	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/dashboard"
	"github.com/avarabyeu/tripops-bot/internal/db"
	"github.com/avarabyeu/tripops-bot/internal/decisions"
	"github.com/avarabyeu/tripops-bot/internal/events"
	"github.com/avarabyeu/tripops-bot/internal/expenses"
	"github.com/avarabyeu/tripops-bot/internal/logistics"
	"github.com/avarabyeu/tripops-bot/internal/notify"
	"github.com/avarabyeu/tripops-bot/internal/scheduler"
	"github.com/avarabyeu/tripops-bot/internal/trips"
	"github.com/avarabyeu/tripops-bot/internal/users"
	"github.com/avarabyeu/tripops-bot/migrations"

	// Registers the "pgx" driver used to create and drop the per-test schema.
	_ "github.com/jackc/pgx/v5/stdlib"
)

// BotToken is the fake token tests sign Telegram init data with.
const BotToken = "123456:TEST-BOT-TOKEN-DO-NOT-USE"

// App is a fully wired application against a temporary database.
type App struct {
	Cfg config.Config
	DB  *db.DB
	Log *slog.Logger

	Users         *users.Service
	Trips         *trips.Service
	Events        *events.Service
	Decisions     *decisions.Service
	Logistics     *logistics.Service
	Accommodation *accommodation.Service
	Checklists    *checklists.Service
	Expenses      *expenses.Service
	Attachments   *attachments.Service
	Activity      *activity.Service
	Notify        *notify.Service
	Dashboard     *dashboard.Service
	Attention     *attention.Engine
	Scheduler     *scheduler.Scheduler
	API           *api.Server
}

// NewApp starts an application on a fresh database and tears it down with the
// test.
func NewApp(t *testing.T) *App {
	t.Helper()
	ctx := t.Context()

	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	if testing.Verbose() {
		log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	}

	database, err := db.Open(ctx, db.Options{URL: databaseURL(t), Log: log})
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Logf("closing test database: %v", err)
		}
	})

	if err := migrations.Run(ctx, database, log); err != nil {
		t.Fatalf("migrate test database: %v", err)
	}

	cfg := config.Config{
		Env:               "development",
		Port:              0,
		BotToken:          BotToken,
		BotMode:           "polling",
		BotUsername:       "tripops_test_bot",
		MiniAppURL:        "https://miniapp.test",
		CORSOrigins:       []string{"*"},
		InitDataTTL:       time.Hour,
		DefaultTimezone:   "UTC",
		DefaultCurrency:   "EUR",
		SchedulerInterval: time.Minute,
		NotifyInterval:    time.Minute,
		LogLevel:          "warn",
	}

	gdb := database.DB
	app := &App{Cfg: cfg, DB: database, Log: log}
	app.Activity = activity.NewService(gdb, log)
	app.Notify = notify.NewService(gdb, log)
	app.Users = users.NewService(gdb)
	app.Trips = trips.NewService(database, app.Users, app.Activity, app.Notify, cfg.DeepLink)
	app.Events = events.NewService(database, app.Trips, app.Activity, app.Notify)
	app.Decisions = decisions.NewService(database, app.Trips, app.Activity, app.Notify)
	app.Logistics = logistics.NewService(database, app.Trips, app.Activity)
	app.Accommodation = accommodation.NewService(database, app.Trips, app.Activity)
	app.Checklists = checklists.NewService(database, app.Trips, app.Activity)
	app.Expenses = expenses.NewService(database, app.Trips, app.Activity, app.Notify)
	app.Attachments = attachments.NewService(gdb)

	app.Attention = attention.NewEngine(attention.Sources{
		Trips:         app.Trips,
		Events:        app.Events.Repo(),
		Decisions:     app.Decisions.Repo(),
		Vehicles:      app.Logistics.Repo(),
		Accommodation: app.Accommodation.Repo(),
		Checklists:    app.Checklists.Repo(),
	})
	app.Dashboard = dashboard.NewService(dashboard.Sources{
		Trips:         app.Trips,
		Events:        app.Events.Repo(),
		Decisions:     app.Decisions.Repo(),
		Vehicles:      app.Logistics.Repo(),
		Accommodation: app.Accommodation.Repo(),
		Checklists:    app.Checklists.Repo(),
		Expenses:      app.Expenses.Repo(),
		Attention:     app.Attention,
	})
	app.Scheduler = scheduler.New(scheduler.Deps{
		Trips:         app.Trips,
		Decisions:     app.Decisions,
		Events:        app.Events.Repo(),
		Checklists:    app.Checklists.Repo(),
		Accommodation: app.Accommodation.Repo(),
		Notify:        app.Notify,
	}, log)

	app.API = api.New(cfg, log, api.Services{
		Users:         app.Users,
		Trips:         app.Trips,
		Events:        app.Events,
		Decisions:     app.Decisions,
		Logistics:     app.Logistics,
		Accommodation: app.Accommodation,
		Checklists:    app.Checklists,
		Expenses:      app.Expenses,
		Attachments:   app.Attachments,
		Activity:      app.Activity,
		Notify:        app.Notify,
		Dashboard:     app.Dashboard,
	}, database.Ping)

	return app
}

// databaseURL picks the engine under test and gives the test a database of
// its own, so tests never see each other's rows and a re-run starts clean.
//
// SQLite gets a file in the test's temp directory — a file rather than
// :memory: because the pool and the migrator hold separate connections and an
// in-memory database exists only inside the one that created it.
//
// PostgreSQL gets a dedicated schema, created here and dropped afterwards. It
// is the same server, so the suite stays fast, but nothing is shared.
func databaseURL(t *testing.T) string {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		return fmt.Sprintf("file:%s/tripops-test.db?_pragma=busy_timeout(5000)", t.TempDir())
	}
	if db.DialectOf(url) != db.Postgres {
		return url
	}
	return postgresSchemaURL(t, url)
}

// postgresSchemaURL creates a throwaway schema and points the connection at it.
func postgresSchemaURL(t *testing.T, url string) string {
	t.Helper()

	schema := "test_" + strings.ReplaceAll(strings.ToLower(core.NewID().Compact()), "-", "")
	// Schema names are case-folded and limited to 63 bytes; the compact id can
	// contain "-" and "_", so keep only what is safe unquoted.
	schema = nonIdentifier.ReplaceAllString(schema, "")
	if len(schema) > 40 {
		schema = schema[:40]
	}

	admin, err := sql.Open("pgx", url)
	if err != nil {
		t.Fatalf("connect to test postgres: %v", err)
	}
	defer func() { _ = admin.Close() }()

	if _, err := admin.ExecContext(t.Context(), "CREATE SCHEMA "+schema); err != nil {
		t.Fatalf("create test schema: %v", err)
	}
	t.Cleanup(func() {
		dropper, err := sql.Open("pgx", url)
		if err != nil {
			t.Logf("drop test schema: %v", err)
			return
		}
		defer func() { _ = dropper.Close() }()
		if _, err := dropper.Exec("DROP SCHEMA " + schema + " CASCADE"); err != nil {
			t.Logf("drop test schema %s: %v", schema, err)
		}
	})

	separator := "?"
	if strings.Contains(url, "?") {
		separator = "&"
	}
	return url + separator + "search_path=" + schema
}

// nonIdentifier matches everything that may not appear in an unquoted
// PostgreSQL identifier.
var nonIdentifier = regexp.MustCompile(`[^a-z0-9_]`)

// Context returns a context bounded by the test, for calls made outside a
// request.
func Context(t *testing.T) context.Context {
	t.Helper()
	return t.Context()
}
