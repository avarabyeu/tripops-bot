package telegram

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/avarabyeu/tripops-bot/internal/notify"
)

// maxDeliveryAttempts abandons a notification rather than retrying forever. A
// reminder that is a day late is worse than no reminder.
const maxDeliveryAttempts = 5

// Deliverer drains the notification outbox into Telegram.
//
// Keeping delivery separate from the domain is what makes notifications
// testable and restart-safe: the services only ever write rows.
type Deliverer struct {
	api        *Client
	notify     *notify.Service
	log        *slog.Logger
	miniAppURL string
}

func NewDeliverer(api *Client, n *notify.Service, log *slog.Logger, miniAppURL string) *Deliverer {
	return &Deliverer{api: api, notify: n, log: log, miniAppURL: miniAppURL}
}

// Run drains the outbox on an interval until the context is cancelled.
func (d *Deliverer) Run(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = 15 * time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := d.Drain(ctx); err != nil && ctx.Err() == nil {
				d.log.Warn("notification delivery failed", "err", err)
			}
		}
	}
}

// Drain sends every due notification it can claim.
func (d *Deliverer) Drain(ctx context.Context) error {
	pending, err := d.notify.ClaimDue(ctx, 25)
	if err != nil {
		return err
	}
	for _, n := range pending {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		// A user who has never opened a private chat with the bot has nowhere
		// to receive this. That is not a failure worth retrying.
		if n.ChatID == 0 {
			_ = d.notify.MarkSkipped(ctx, n.ID, "no private chat with the bot")
			continue
		}
		if _, err := d.api.SendMessage(ctx, SendMessageRequest{
			ChatID:      n.ChatID,
			Text:        renderNotification(n),
			ReplyMarkup: d.notificationKeyboard(n),
		}); err != nil {
			var apiErr *APIError
			if asAPIError(err, &apiErr) && apiErr.Blocked() {
				_ = d.notify.MarkSkipped(ctx, n.ID, apiErr.Description)
				continue
			}
			_ = d.notify.MarkFailed(ctx, n.ID, err.Error(), maxDeliveryAttempts)
			continue
		}
		if err := d.notify.MarkSent(ctx, n.ID); err != nil {
			d.log.Warn("mark notification sent failed", "id", n.ID, "err", err)
		}
	}
	return nil
}

func renderNotification(n notify.Notification) string {
	var b strings.Builder
	if n.Title != "" {
		b.WriteString(bold(EscapeHTML(n.Title)))
		b.WriteString("\n\n")
	}
	b.WriteString(EscapeHTML(n.Body))
	return b.String()
}

// notificationKeyboard adds a single way back into the trip, which is the only
// action a notification ever needs.
func (d *Deliverer) notificationKeyboard(n notify.Notification) *InlineKeyboardMarkup {
	if n.TripID.IsZero() {
		return nil
	}
	compact := n.TripID.Compact()
	buttons := row(button("Open trip", "t:"+compact))
	if d.miniAppURL != "" {
		buttons = row(
			webAppButton("📱 Open", d.miniAppURL+"?startapp=trip_"+compact),
			button("Details", "t:"+compact),
		)
	}
	return rows(buttons)
}
