// Package webapp serves the built Mini App out of the binary.
//
// There used to be a second container for this: nginx, with a config doing
// four jobs — static files, cache headers, a single-page fallback, and a
// reverse proxy back to this process. Three of them are a few lines of Go.
// The fourth disappears entirely once the app and the API share a process,
// and with it the private network, the second image on every deploy, and the
// possibility of shipping a new front end against an old backend.
package webapp

import (
	"embed"
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// dist is the Vite build output. Vite writes straight into this directory —
// see miniapp/vite.config.ts — so there is no copy step to forget.
//
// The `all:` prefix matters: it makes the pattern match a directory holding
// nothing but the tracked .gitkeep. A fresh clone and CI have never run npm,
// and `go build ./...` still has to work for them.
//
//go:embed all:dist
var dist embed.FS

const (
	// Vite hashes every asset filename, so a URL under /assets/ can only ever
	// name one build of one file.
	immutable = "public, max-age=31536000, immutable"
	// index.html is how a new build reaches a phone that already has the old
	// one open, so it must never be held anywhere.
	noStore = "no-store, must-revalidate"
)

// Built reports whether a real bundle is embedded, for a line in the log at
// startup. A binary without one still runs: the bot and the API work, and the
// Mini App explains itself.
func Built() bool {
	_, err := fs.Stat(root(), "index.html")
	return err == nil
}

func root() fs.FS {
	sub, err := fs.Sub(dist, "dist")
	if err != nil {
		// Unreachable: the directory is embedded above, so it exists.
		panic("webapp: " + err.Error())
	}
	return sub
}

// Handler serves the app, falling back to index.html for client-side routes.
func Handler() http.Handler {
	files := root()
	index, err := fs.ReadFile(files, "index.html")
	if err != nil {
		return http.HandlerFunc(explain)
	}
	static := http.FileServerFS(files)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		harden(w)

		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		asset := strings.HasPrefix(r.URL.Path, "/assets/")

		if info, err := fs.Stat(files, name); err == nil && !info.IsDir() {
			if asset {
				w.Header().Set("Cache-Control", immutable)
			} else {
				w.Header().Set("Cache-Control", noStore)
			}
			static.ServeHTTP(w, r)
			return
		}
		// A missing asset is a broken build, not a route. Answering it with
		// the app shell and a 200 turns that into a blank screen with no clue
		// in the network tab.
		if asset {
			http.NotFound(w, r)
			return
		}

		// Everything else is a path the client-side router owns.
		w.Header().Set("Cache-Control", noStore)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		if r.Method == http.MethodGet {
			_, _ = w.Write(index)
		}
	})
}

// harden sets what the nginx config used to. Framing stays allowed on
// purpose: Telegram opens a Mini App inside an iframe.
func harden(w http.ResponseWriter) {
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
}

// explain stands in when the binary was built without a bundle. It says what
// to do rather than 404ing, because the two ways to reach it are a developer
// on the wrong port and a release built in the wrong order.
func explain(w http.ResponseWriter, r *http.Request) {
	harden(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusServiceUnavailable)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = w.Write([]byte(`<!doctype html><meta charset="utf-8">` +
		`<title>TripOps — no bundle</title>` +
		`<body style="font:15px/1.5 system-ui;margin:3rem auto;max-width:34rem;padding:0 1rem">` +
		`<h1 style="font-size:1.2rem">The Mini App is not in this binary</h1>` +
		`<p>The API and the bot are running. The front end was not built before ` +
		`this binary was compiled.</p>` +
		`<p>In development, use the Vite dev server instead — <code>task app:dev</code>, ` +
		`on port 5173 — which proxies the API here.</p>` +
		`<p>To embed it, run <code>task app:build</code> and rebuild.</p>`))
}
