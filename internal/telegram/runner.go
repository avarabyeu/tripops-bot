package telegram

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"time"
)

// Setup registers the command menu and configures how updates arrive.
func (b *Bot) Setup(ctx context.Context) error {
	if err := b.api.SetMyCommands(ctx, Commands()); err != nil {
		return err
	}
	switch b.cfg.BotMode {
	case "webhook":
		return b.api.SetWebhook(ctx, b.cfg.WebhookURL+WebhookPath, b.cfg.WebhookSecret)
	default:
		// Long polling and a registered webhook are mutually exclusive.
		return b.api.DeleteWebhook(ctx)
	}
}

// RunPolling consumes updates until the context is cancelled. It is the
// development path: no public URL, no TLS, nothing to configure.
func (b *Bot) RunPolling(ctx context.Context) {
	offset := 0
	backoff := time.Second

	for {
		if ctx.Err() != nil {
			return
		}
		updates, err := b.api.GetUpdates(ctx, offset, 50)
		if err != nil {
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				return
			}
			b.log.Warn("getUpdates failed", "err", err, "retry_in", backoff)
			select {
			case <-ctx.Done():
				return
			case <-time.After(backoff):
			}
			// Cap the backoff: Telegram hiccups are usually brief and a long
			// sleep means the bot looks dead to the group.
			if backoff < 30*time.Second {
				backoff *= 2
			}
			continue
		}
		backoff = time.Second

		for _, u := range updates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			b.HandleUpdate(ctx, u)
		}
	}
}

// WebhookPath is where Telegram posts updates in webhook mode.
const WebhookPath = "/telegram/webhook"

// WebhookHandler verifies the secret header and processes the update.
//
// Updates are handled synchronously but the response is sent first: Telegram
// retries on a slow or failed response, and a retry would re-run whatever the
// user tapped.
func (b *Bot) WebhookHandler(log *slog.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		given := r.Header.Get("X-Telegram-Bot-Api-Secret-Token")
		if subtle.ConstantTimeCompare([]byte(given), []byte(b.cfg.WebhookSecret)) != 1 {
			log.Warn("telegram webhook rejected: bad secret")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}

		var update Update
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&update); err != nil {
			log.Warn("telegram webhook: malformed update", "err", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		// The update outlives the request, so the context is detached from it
		// but still bounded: a handler that hangs must not leak a goroutine.
		ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 30*time.Second)
		w.WriteHeader(http.StatusOK)

		go func(ctx context.Context, update Update) {
			defer cancel()
			b.HandleUpdate(ctx, update)
		}(ctx, update)
	})
}
