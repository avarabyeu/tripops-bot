// Package api exposes the REST surface the Mini App talks to.
//
// Handlers are thin on purpose: parse, read the already-resolved trips.Access,
// call one service method, encode. Every rule that matters lives in a domain
// module, so the bot and the API can never disagree about what is allowed.
package api

import (
	"context"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/avarabyeu/tripops-bot/internal/accommodation"
	"github.com/avarabyeu/tripops-bot/internal/activity"
	"github.com/avarabyeu/tripops-bot/internal/attachments"
	"github.com/avarabyeu/tripops-bot/internal/auth"
	"github.com/avarabyeu/tripops-bot/internal/checklists"
	"github.com/avarabyeu/tripops-bot/internal/config"
	"github.com/avarabyeu/tripops-bot/internal/core"
	"github.com/avarabyeu/tripops-bot/internal/dashboard"
	"github.com/avarabyeu/tripops-bot/internal/decisions"
	"github.com/avarabyeu/tripops-bot/internal/events"
	"github.com/avarabyeu/tripops-bot/internal/expenses"
	"github.com/avarabyeu/tripops-bot/internal/httpx"
	"github.com/avarabyeu/tripops-bot/internal/logistics"
	"github.com/avarabyeu/tripops-bot/internal/notify"
	"github.com/avarabyeu/tripops-bot/internal/trips"
	"github.com/avarabyeu/tripops-bot/internal/users"
)

// Services is every domain service the API needs, wired in main.
type Services struct {
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
}

type Server struct {
	cfg   config.Config
	log   *slog.Logger
	svc   Services
	ready func(context.Context) error
}

// New builds the API server. ready is the readiness probe (a database ping).
func New(cfg config.Config, log *slog.Logger, svc Services, ready func(context.Context) error) *Server {
	return &Server{cfg: cfg, log: log, svc: svc, ready: ready}
}

// Handler returns the fully wired HTTP handler.
func (s *Server) Handler() http.Handler {
	r := chi.NewRouter()
	r.Use(httpx.Recoverer(s.log))
	r.Use(httpx.RequestLogger(s.log))
	r.Use(httpx.CORS(s.cfg.CORSOrigins))
	s.routes(r)
	return r
}

// authenticate validates Telegram init data and loads the calling user.
func (s *Server) authenticate() func(http.Handler) http.Handler {
	return auth.Middleware(s.svc.Users, auth.Options{
		BotToken:      s.cfg.BotToken,
		TTL:           s.cfg.InitDataTTL,
		DevTelegramID: s.devTelegramID(),
		Log:           s.log,
	})
}

// devTelegramID returns the development bypass id, and zero unless the process
// is explicitly running in development.
func (s *Server) devTelegramID() int64 {
	if !s.cfg.IsDevelopment() {
		return 0
	}
	return s.cfg.DevUserTelegramID
}

type ctxKey int

const accessKey ctxKey = iota

// tripAccess resolves {tripID} and the caller's membership before any
// trip-scoped handler runs.
//
// Mounting it on the /trips/{tripID} subrouter is what guarantees the three
// checks the spec demands — authenticated, member of this trip, sufficient
// role — can never be forgotten in an individual handler: a route that does
// not go through here has no Access to read.
func (s *Server) tripAccess(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal, err := auth.MustPrincipal(r.Context())
		if err != nil {
			httpx.WriteError(w, r, s.log, err)
			return
		}
		tripID, err := core.ParseID(chi.URLParam(r, "tripID"))
		if err != nil {
			httpx.WriteError(w, r, s.log, core.Invalid("tripID is not a valid id"))
			return
		}
		access, err := s.svc.Trips.Access(r.Context(), tripID, principal.User.ID)
		if err != nil {
			httpx.WriteError(w, r, s.log, err)
			return
		}
		next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), accessKey, access)))
	})
}

// tripHandler is a handler for a route that has already resolved trip access.
type tripHandler func(w http.ResponseWriter, r *http.Request, access trips.Access) error

// withAccess adapts a tripHandler, pulling the Access the middleware resolved.
func (s *Server) withAccess(h tripHandler) http.HandlerFunc {
	return httpx.Wrap(s.log, func(w http.ResponseWriter, r *http.Request) error {
		access, ok := r.Context().Value(accessKey).(trips.Access)
		if !ok {
			return core.Internal(errMissingAccess)
		}
		return h(w, r, access)
	})
}

// handle adapts a plain handler that only needs the authenticated user.
func (s *Server) handle(h httpx.Handler) http.HandlerFunc { return httpx.Wrap(s.log, h) }

// principal is a small helper for non-trip routes.
func principal(r *http.Request) (auth.Principal, error) {
	return auth.MustPrincipal(r.Context())
}

// memberIDParam reads an optional member id from a request body, defaulting to
// the caller. Used by the "answer for yourself or for someone else" endpoints.
func memberIDParam(raw string, self core.ID) (core.ID, error) {
	if raw == "" {
		return self, nil
	}
	id, err := core.ParseID(raw)
	if err != nil {
		return core.Nil, core.Invalid("member_id is not a valid id")
	}
	return id, nil
}

// errMissingAccess means a trip-scoped handler was mounted outside the
// tripAccess middleware, which is a wiring bug rather than a request problem.
var errMissingAccess = errorString("api: trip handler mounted without tripAccess middleware")

type errorString string

func (e errorString) Error() string { return string(e) }
