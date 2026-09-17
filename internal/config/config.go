// Package config loads process configuration from the environment. There is no
// config file: every deployment target this project supports (Docker Compose,
// a container platform) speaks environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Env  string // development | production
	Port int

	DatabaseURL     string
	DatabaseMaxConn int32

	// Telegram
	BotToken      string
	BotMode       string // polling | webhook
	WebhookURL    string // public https base URL, webhook mode only
	WebhookSecret string
	BotUsername   string // used to build t.me deep links

	// DefaultTimezone is used for trips created from the bot, where there is
	// no timezone picker. The Mini App always sends one explicitly.
	DefaultTimezone string
	DefaultCurrency string

	// Mini App
	MiniAppURL  string
	CORSOrigins []string

	// Auth
	InitDataTTL time.Duration
	// DevUserTelegramID lets the Mini App be developed in a browser without
	// Telegram. Ignored unless Env == "development".
	DevUserTelegramID int64

	// Workers
	SchedulerInterval time.Duration
	NotifyInterval    time.Duration
	LogLevel          string
}

// Load reads the environment and validates it. It returns a single error
// listing everything that is wrong rather than failing on the first problem.
func Load() (Config, error) {
	c := Config{
		Env:               envStr("APP_ENV", "development"),
		Port:              envInt("PORT", 8080),
		DatabaseURL:       envStr("DATABASE_URL", "postgres://tripops:tripops@localhost:5432/tripops?sslmode=disable"),
		DatabaseMaxConn:   int32(envInt("DATABASE_MAX_CONNECTIONS", 10)),
		BotToken:          envStr("TELEGRAM_BOT_TOKEN", ""),
		BotMode:           envStr("TELEGRAM_BOT_MODE", "polling"),
		WebhookURL:        strings.TrimSuffix(envStr("TELEGRAM_WEBHOOK_URL", ""), "/"),
		WebhookSecret:     envStr("TELEGRAM_WEBHOOK_SECRET", ""),
		BotUsername:       strings.TrimPrefix(envStr("TELEGRAM_BOT_USERNAME", ""), "@"),
		DefaultTimezone:   envStr("DEFAULT_TIMEZONE", "UTC"),
		DefaultCurrency:   strings.ToUpper(envStr("DEFAULT_CURRENCY", "EUR")),
		MiniAppURL:        strings.TrimSuffix(envStr("MINIAPP_URL", ""), "/"),
		CORSOrigins:       envList("CORS_ORIGINS", "http://localhost:5173"),
		InitDataTTL:       envDuration("TELEGRAM_INITDATA_TTL", 24*time.Hour),
		DevUserTelegramID: int64(envInt("DEV_USER_TELEGRAM_ID", 0)),
		SchedulerInterval: envDuration("SCHEDULER_INTERVAL", time.Minute),
		NotifyInterval:    envDuration("NOTIFY_INTERVAL", 15*time.Second),
		LogLevel:          envStr("LOG_LEVEL", "info"),
	}

	var problems []string
	if c.DatabaseURL == "" {
		problems = append(problems, "DATABASE_URL is required")
	}
	switch c.BotMode {
	case "polling":
	case "webhook":
		if c.WebhookURL == "" {
			problems = append(problems, "TELEGRAM_WEBHOOK_URL is required when TELEGRAM_BOT_MODE=webhook")
		}
		if c.WebhookSecret == "" {
			problems = append(problems, "TELEGRAM_WEBHOOK_SECRET is required when TELEGRAM_BOT_MODE=webhook")
		}
	default:
		problems = append(problems, "TELEGRAM_BOT_MODE must be polling or webhook")
	}
	if c.Env != "development" && c.BotToken == "" {
		problems = append(problems, "TELEGRAM_BOT_TOKEN is required outside development")
	}
	if !isValidTimezone(c.DefaultTimezone) {
		problems = append(problems, "DEFAULT_TIMEZONE must be a valid IANA timezone")
	}
	if c.Env != "development" && c.DevUserTelegramID != 0 {
		problems = append(problems, "DEV_USER_TELEGRAM_ID must not be set outside development")
	}
	if len(problems) > 0 {
		return c, fmt.Errorf("invalid configuration:\n  - %s", strings.Join(problems, "\n  - "))
	}
	return c, nil
}

// IsDevelopment reports whether developer-only shortcuts are allowed.
func (c Config) IsDevelopment() bool { return c.Env == "development" }

// BotConfigured reports whether Telegram integration can start at all.
func (c Config) BotConfigured() bool { return c.BotToken != "" }

// DeepLink builds the invite link for a token, e.g. https://t.me/bot?start=inv_x.
func (c Config) DeepLink(payload string) string {
	if c.BotUsername == "" {
		return ""
	}
	return fmt.Sprintf("https://t.me/%s?start=%s", c.BotUsername, payload)
}

func isValidTimezone(name string) bool {
	_, err := time.LoadLocation(name)
	return err == nil
}

func envStr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
		return strings.TrimSpace(v)
	}
	return def
}

func envInt(key string, def int) int {
	if v, ok := os.LookupEnv(key); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	if v, ok := os.LookupEnv(key); ok {
		if d, err := time.ParseDuration(strings.TrimSpace(v)); err == nil {
			return d
		}
	}
	return def
}

func envList(key, def string) []string {
	raw := envStr(key, def)
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
