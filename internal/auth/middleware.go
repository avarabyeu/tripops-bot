package auth

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/httpx"
	"github.com/avarabyeu/tripops-bot/internal/users"
)

type ctxKey int

const principalKey ctxKey = iota

// Principal is the authenticated caller of a request.
type Principal struct {
	User       users.User
	StartParam string
}

// WithPrincipal is exported for tests and for the Telegram adapter, which
// authenticates through Telegram itself rather than through initData.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey, p)
}

// PrincipalFrom returns the authenticated caller, or false when the request
// never passed through the authentication middleware.
func PrincipalFrom(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey).(Principal)
	return p, ok
}

// MustPrincipal returns the caller or an unauthorized error.
func MustPrincipal(ctx context.Context) (Principal, error) {
	p, ok := PrincipalFrom(ctx)
	if !ok {
		return Principal{}, core.Unauthorized("authentication required")
	}
	return p, nil
}

// Options configures the authentication middleware.
type Options struct {
	BotToken string
	TTL      time.Duration
	// DevTelegramID, when non-zero, lets unauthenticated requests through as
	// that Telegram user. It is only ever set in development so the Mini App
	// can be opened in a desktop browser.
	DevTelegramID int64
	Log           *slog.Logger
}

// Middleware authenticates every request from the `Authorization: tma <initData>`
// header, creating or refreshing the user record as a side effect.
func Middleware(svc *users.Service, opts Options) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			raw := extractInitData(r)

			if raw == "" && opts.DevTelegramID != 0 {
				user, err := svc.EnsureUser(r.Context(), users.Identity{
					TelegramID: opts.DevTelegramID,
					FirstName:  "Dev",
					Username:   "dev",
				})
				if err != nil {
					httpx.WriteError(w, r, opts.Log, err)
					return
				}
				next.ServeHTTP(w, r.WithContext(WithPrincipal(r.Context(), Principal{User: user})))
				return
			}

			data, err := ValidateInitData(raw, opts.BotToken, opts.TTL, time.Now())
			if err != nil {
				httpx.WriteError(w, r, opts.Log, err)
				return
			}
			user, err := svc.EnsureUser(r.Context(), data.User)
			if err != nil {
				httpx.WriteError(w, r, opts.Log, err)
				return
			}
			ctx := WithPrincipal(r.Context(), Principal{User: user, StartParam: data.StartParam})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// extractInitData accepts the conventional `tma` Authorization scheme and a
// plain header, which is what most Mini App clients send.
func extractInitData(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		scheme, value, found := strings.Cut(h, " ")
		if found && strings.EqualFold(scheme, "tma") {
			return strings.TrimSpace(value)
		}
	}
	return strings.TrimSpace(r.Header.Get("X-Telegram-Init-Data"))
}
