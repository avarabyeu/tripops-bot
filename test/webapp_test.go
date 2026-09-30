package test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/avarabyeu/tripops-bot/internal/testsupport"
	"github.com/avarabyeu/tripops-bot/internal/webapp"
)

// TestMiniAppIsServedByTheAPI covers the contract that replaced nginx. The
// rules are small and each one was a line of nginx config that is now a line
// of Go, which is exactly the kind of thing that rots unnoticed.
func TestMiniAppIsServedByTheAPI(t *testing.T) {
	app := testsupport.NewApp(t)
	handler := app.API.Handler()

	get := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		return rec
	}

	// An unknown path under /api must read as an API error, not as the app.
	// Without the subrouter's own NotFound it would fall through below and
	// answer a fetch with an HTML page.
	api := get("/api/v1/definitely-not-a-route")
	if ct := api.Header().Get("Content-Type"); !strings.Contains(ct, "application/json") {
		t.Errorf("unknown API path answered %q, want JSON", ct)
	}
	if api.Code == http.StatusOK {
		t.Errorf("unknown API path returned 200")
	}

	// Health checks are not the app either.
	if health := get("/healthz"); health.Code != http.StatusOK ||
		!strings.Contains(health.Body.String(), `"ok"`) {
		t.Errorf("healthz = %d %q", health.Code, health.Body.String())
	}

	if !webapp.Built() {
		// A backend-only working copy: `go build` deliberately still works
		// without the bundle, so the rest of this test has nothing to assert.
		t.Skip("no bundle embedded — run `task app:build` to cover the rest")
	}

	// A client-side route is the app shell, and must never be cached: it is
	// how a new build reaches a phone that already has the old one open.
	root := get("/trips/whatever")
	if root.Code != http.StatusOK {
		t.Fatalf("client route = %d, want 200", root.Code)
	}
	if ct := root.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("client route served %q", ct)
	}
	if cc := root.Header().Get("Cache-Control"); !strings.Contains(cc, "no-store") {
		t.Errorf("app shell Cache-Control = %q, want no-store", cc)
	}
	if root.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("the security headers nginx used to set are missing")
	}

	// Hashed assets can be held forever.
	name := assetPath(t, root.Body.String())
	asset := get(name)
	if asset.Code != http.StatusOK {
		t.Fatalf("%s = %d", name, asset.Code)
	}
	if cc := asset.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("%s Cache-Control = %q, want immutable", name, cc)
	}

	// A missing asset is a broken build, not a route. Answering it with the
	// shell and a 200 turns that into a blank screen with no clue.
	if missing := get("/assets/nope-12345678.js"); missing.Code != http.StatusNotFound {
		t.Errorf("missing asset = %d, want 404", missing.Code)
	}
}

// assetPath pulls the first hashed asset out of the served index.html.
func assetPath(t *testing.T, html string) string {
	t.Helper()
	i := strings.Index(html, "/assets/")
	if i < 0 {
		t.Fatalf("index.html references no assets:\n%s", html)
	}
	rest := html[i:]
	end := strings.IndexAny(rest, `"'`)
	if end < 0 {
		t.Fatalf("unterminated asset reference in index.html")
	}
	return rest[:end]
}
