// Package httpx contains the transport plumbing shared by every REST handler:
// JSON encoding, a single error shape, request decoding and path/query
// parsing. Handlers return errors; this package decides what the client sees.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/avarabyeu/tripops-bot/internal/core"
)

// MaxBodyBytes caps request bodies. Attachments are never uploaded through
// this API (Telegram holds the bytes), so 1 MiB is generous.
const MaxBodyBytes = 1 << 20

// ErrorBody is the only error shape the API emits.
type ErrorBody struct {
	Error struct {
		Code    core.ErrorCode    `json:"code"`
		Message string            `json:"message"`
		Fields  map[string]string `json:"fields,omitempty"`
	} `json:"error"`
}

// Handler is an http.HandlerFunc that may fail. Wrap turns it into one.
type Handler func(w http.ResponseWriter, r *http.Request) error

// Wrap adapts a Handler, translating domain errors into HTTP responses and
// logging the ones that indicate a bug.
func Wrap(log *slog.Logger, h Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := h(w, r); err != nil {
			WriteError(w, r, log, err)
		}
	}
}

// JSON writes v with the given status. A nil v writes no body.
func JSON(w http.ResponseWriter, status int, v any) error {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	if v == nil {
		w.WriteHeader(status)
		return nil
	}
	buf, err := json.Marshal(v)
	if err != nil {
		return core.Internal(fmt.Errorf("encode response: %w", err))
	}
	w.WriteHeader(status)
	_, _ = w.Write(buf)
	return nil
}

// NoContent answers 204.
func NoContent(w http.ResponseWriter) error {
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// WriteError renders err using the standard error body.
func WriteError(w http.ResponseWriter, r *http.Request, log *slog.Logger, err error) {
	domErr := core.AsError(err)
	status := StatusFor(domErr.Code)
	if status >= 500 && log != nil {
		log.ErrorContext(r.Context(), "request failed",
			"method", r.Method, "path", r.URL.Path, "err", err)
	}
	var body ErrorBody
	body.Error.Code = domErr.Code
	body.Error.Message = domErr.Message
	body.Error.Fields = domErr.Fields
	_ = JSON(w, status, body)
}

// StatusFor maps a domain error code to an HTTP status.
func StatusFor(code core.ErrorCode) int {
	switch code {
	case core.CodeInvalid:
		return http.StatusBadRequest
	case core.CodeUnauthorized:
		return http.StatusUnauthorized
	case core.CodeForbidden:
		return http.StatusForbidden
	case core.CodeNotFound:
		return http.StatusNotFound
	case core.CodeConflict:
		return http.StatusConflict
	default:
		return http.StatusInternalServerError
	}
}

// Decode reads a JSON body into v, rejecting unknown fields so a typo in the
// client surfaces immediately instead of being silently ignored.
func Decode[T any](r *http.Request, v *T) error {
	if r.Body == nil {
		return core.Invalid("request body is required")
	}
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, MaxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var maxErr *http.MaxBytesError
		switch {
		case errors.Is(err, io.EOF):
			return core.Invalid("request body is required")
		case errors.As(err, &maxErr):
			return core.Invalid("request body is too large")
		default:
			return core.Invalid("malformed JSON: %s", err.Error())
		}
	}
	if err := dec.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return core.Invalid("request body must contain a single JSON object")
	}
	return nil
}

// PathID reads a {name} path parameter as an ID.
func PathID(r *http.Request, name string) (core.ID, error) {
	raw := PathParam(r, name)
	if raw == "" {
		return core.Nil, core.Invalid("%s is required", name)
	}
	id, err := core.ParseID(raw)
	if err != nil {
		return core.Nil, core.Invalid("%s is not a valid id", name)
	}
	return id, nil
}

// PathParam reads a routing parameter. It asks chi first, since that is the
// router in use, and falls back to the standard library's pattern matching so
// a handler can still be exercised from a plain ServeMux in a test.
func PathParam(r *http.Request, name string) string {
	if v := chi.URLParam(r, name); v != "" {
		return v
	}
	return r.PathValue(name)
}

// QueryID reads an optional ?name= parameter as an ID.
func QueryID(r *http.Request, name string) (core.ID, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return core.Nil, nil
	}
	id, err := core.ParseID(raw)
	if err != nil {
		return core.Nil, core.Invalid("%s is not a valid id", name)
	}
	return id, nil
}

// QueryBool reads an optional boolean query parameter.
func QueryBool(r *http.Request, name string, def bool) bool {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return def
	}
	b, err := strconv.ParseBool(raw)
	if err != nil {
		return def
	}
	return b
}

// QueryInt reads an optional integer query parameter, clamped to [min, max].
func QueryInt(r *http.Request, name string, def, min, max int) int {
	raw := strings.TrimSpace(r.URL.Query().Get(name))
	if raw == "" {
		return def
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return def
	}
	return Clamp(n, min, max)
}

// Clamp bounds n to [min, max].
func Clamp(n, min, max int) int {
	if n < min {
		return min
	}
	if n > max {
		return max
	}
	return n
}

// Ptr is a convenience for optional JSON fields in tests and builders.
func Ptr[T any](v T) *T { return &v }

// RFC3339 formats a time for JSON output, rendering the zero time as null.
func RFC3339(t time.Time) *string {
	if t.IsZero() {
		return nil
	}
	s := t.UTC().Format(time.RFC3339)
	return &s
}
