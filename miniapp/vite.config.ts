import { defineConfig, loadEnv } from "vite";
import react from "@vitejs/plugin-react";

// The Mini App is served as static files. In development it proxies /api to
// the Go backend so the browser sees one origin and CORS never enters the
// picture.
export default defineConfig(({ mode }) => {
  const env = loadEnv(mode, process.cwd(), "");
  return {
  plugins: [react()],
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
    outDir: "dist",
    // Telegram opens the app in a webview on a phone network; a smaller,
    // single-chunk bundle beats clever code splitting here.
    target: "es2022",
    sourcemap: false,
  },
  };
});
