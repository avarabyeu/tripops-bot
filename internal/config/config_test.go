package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

// isolateEnv runs the test against an empty environment.
//
// Everything is cleared rather than a list of known keys, because the list
// would rot: a new setting would silently start reading the developer's own
// .env, which `task` exports for every task. That is how these tests passed
// under `go test` and failed under `task test`.
func isolateEnv(t *testing.T) {
	t.Helper()
	saved := os.Environ()
	os.Clearenv()
	t.Cleanup(func() {
		os.Clearenv()
		for _, entry := range saved {
			if key, value, ok := strings.Cut(entry, "="); ok {
				_ = os.Setenv(key, value)
			}
		}
	})
}

// Configuration is the one place a mistake is silent until production, so the
// loader reports everything that is wrong at once and refuses to start.
func TestLoadDefaults(t *testing.T) {
	isolateEnv(t)
	cfg, err := Load()
	if err != nil {
		t.Fatalf("an empty environment should be a valid development setup: %v", err)
	}
	if cfg.Env != "development" || !cfg.IsDevelopment() {
		t.Errorf("env = %q, want development", cfg.Env)
	}
	if cfg.Port != 8080 {
		t.Errorf("port = %d, want 8080", cfg.Port)
	}
	if cfg.BotMode != "polling" {
		t.Errorf("bot mode = %q; polling needs no public URL and is the right default", cfg.BotMode)
	}
	// A fresh checkout must run with no database server.
	if cfg.DatabaseURL != "sqlite://tripops.db" {
		t.Errorf("database URL = %q, want a local SQLite file", cfg.DatabaseURL)
	}
	if cfg.BotConfigured() {
		t.Error("no token was set, so the bot must be reported as unconfigured")
	}
	if cfg.InitDataTTL != 24*time.Hour {
		t.Errorf("init data TTL = %v", cfg.InitDataTTL)
	}
}

func TestLoadReadsTheEnvironment(t *testing.T) {
	isolateEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("PORT", "9000")
	t.Setenv("DATABASE_URL", "postgres://u:p@db:5432/tripops")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:ABC")
	t.Setenv("TELEGRAM_BOT_USERNAME", "@tripops_bot")
	t.Setenv("MINIAPP_URL", "https://app.example.com/")
	t.Setenv("CORS_ORIGINS", "https://a.example.com, https://b.example.com ")
	t.Setenv("SCHEDULER_INTERVAL", "30s")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != 9000 {
		t.Errorf("port = %d", cfg.Port)
	}
	// A leading @ is what people copy out of Telegram; it must not end up in
	// the deep link.
	if cfg.BotUsername != "tripops_bot" {
		t.Errorf("bot username = %q, want the @ stripped", cfg.BotUsername)
	}
	// A trailing slash would produce a double slash in every built URL.
	if cfg.MiniAppURL != "https://app.example.com" {
		t.Errorf("mini app URL = %q, want the trailing slash trimmed", cfg.MiniAppURL)
	}
	if len(cfg.CORSOrigins) != 2 || cfg.CORSOrigins[0] != "https://a.example.com" {
		t.Errorf("CORS origins = %q, want two trimmed entries", cfg.CORSOrigins)
	}
	if cfg.SchedulerInterval != 30*time.Second {
		t.Errorf("scheduler interval = %v", cfg.SchedulerInterval)
	}
	if !cfg.BotConfigured() || cfg.IsDevelopment() {
		t.Error("production with a token should be configured and not development")
	}
}

func TestDeepLink(t *testing.T) {
	cfg := Config{BotUsername: "tripops_bot"}
	if got := cfg.DeepLink("inv_abc123"); got != "https://t.me/tripops_bot?start=inv_abc123" {
		t.Errorf("DeepLink = %q", got)
	}
	// Without a username there is no link to build, and an invented one would
	// be worse than none.
	if got := (Config{}).DeepLink("inv_abc123"); got != "" {
		t.Errorf("DeepLink without a username = %q, want empty", got)
	}
}

func TestLoadRejectsBrokenConfigurations(t *testing.T) {
	cases := map[string]struct {
		env  map[string]string
		want string
	}{
		"webhook without a URL": {
			env:  map[string]string{"TELEGRAM_BOT_MODE": "webhook", "TELEGRAM_WEBHOOK_SECRET": "s"},
			want: "TELEGRAM_WEBHOOK_URL",
		},
		"webhook without a secret": {
			env:  map[string]string{"TELEGRAM_BOT_MODE": "webhook", "TELEGRAM_WEBHOOK_URL": "https://x"},
			want: "TELEGRAM_WEBHOOK_SECRET",
		},
		"unknown bot mode": {
			env:  map[string]string{"TELEGRAM_BOT_MODE": "carrier-pigeon"},
			want: "TELEGRAM_BOT_MODE",
		},
		"production without a token": {
			env:  map[string]string{"APP_ENV": "production"},
			want: "TELEGRAM_BOT_TOKEN",
		},
		// Falling back to a local SQLite file in production would look like it
		// worked and quietly serve an empty database.
		"production without a database URL": {
			env:  map[string]string{"APP_ENV": "production", "TELEGRAM_BOT_TOKEN": "123:ABC"},
			want: "DATABASE_URL must be set explicitly",
		},
		// The development bypass accepts unsigned requests. Outside
		// development that is an authentication hole, so it refuses to boot.
		"development bypass in production": {
			env: map[string]string{
				"APP_ENV":              "production",
				"TELEGRAM_BOT_TOKEN":   "123:ABC",
				"DEV_USER_TELEGRAM_ID": "42",
			},
			want: "DEV_USER_TELEGRAM_ID",
		},
		"unknown timezone": {
			env:  map[string]string{"DEFAULT_TIMEZONE": "Mars/Olympus"},
			want: "DEFAULT_TIMEZONE",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			isolateEnv(t)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			_, err := Load()
			if err == nil {
				t.Fatal("expected the configuration to be rejected")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error does not mention %s: %v", tc.want, err)
			}
		})
	}
}

// Every problem is reported together; fixing them one restart at a time is
// miserable.
func TestLoadReportsEveryProblemAtOnce(t *testing.T) {
	isolateEnv(t)
	t.Setenv("APP_ENV", "production")
	t.Setenv("TELEGRAM_BOT_MODE", "webhook")
	t.Setenv("DEFAULT_TIMEZONE", "Nowhere/Special")

	_, err := Load()
	if err == nil {
		t.Fatal("expected the configuration to be rejected")
	}
	for _, want := range []string{
		"TELEGRAM_WEBHOOK_URL", "TELEGRAM_WEBHOOK_SECRET",
		"TELEGRAM_BOT_TOKEN", "DEFAULT_TIMEZONE",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error is missing %s: %v", want, err)
		}
	}
}
