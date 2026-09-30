import { writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { defineConfig, loadEnv, type Plugin } from "vite";
import react from "@vitejs/plugin-react";

// Where the bundle goes: straight into the Go package that embeds it, so the
// binary carries the app and there is no second image and no copy step
// between the two builds to forget.
const OUT_DIR = "../internal/webapp/dist";

/**
 * Vite empties the output directory, which takes the tracked `.gitkeep` with
 * it — and `go:embed all:dist` will not compile against a directory with no
 * files at all. A clone that has never run npm has exactly that, so the
 * placeholder has to survive every build, not just the ones run through Task.
 */
function keepEmbedDirTracked(): Plugin {
  return {
    name: "tripops-keep-embed-dir",
    closeBundle() {
      writeFileSync(resolve(import.meta.dirname, OUT_DIR, ".gitkeep"), "");
    },
  };
}

// In development this serves the app itself and proxies /api to the Go
// backend, so the browser sees one origin and CORS never enters the picture.
// In production there is no dev server and no nginx: the Go binary serves
// both from one process.
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "");
  return {
    plugins: [react(), keepEmbedDirTracked()],
    server: {
      port: 5173,
      proxy: {
        "/api": {
          target: env.BACKEND_URL || "http://localhost:8080",
          changeOrigin: true,
        },
      },
    },
    build: {
      outDir: OUT_DIR,
      // The directory is outside the Vite root, so it refuses to clear it
      // without being told. Stale hashed assets would otherwise pile up and
      // be embedded into the binary forever.
      emptyOutDir: true,
      // Telegram opens the app in a webview on a phone network; a smaller,
      // single-chunk bundle beats clever code splitting here.
      target: "es2022",
      sourcemap: false,
    },
  };
});
