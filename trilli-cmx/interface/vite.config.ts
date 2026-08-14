import { defineConfig } from "vite";
import react from "@vitejs/plugin-react-swc";
import path from "path";

// Vite is build-time only. The Go binary embeds dist/ and serves both the SPA
// and the API at runtime on port 9260 — no Node process in production.
export default defineConfig({
  server: {
    host: "::",
    port: 5273,
    proxy: {
      "/api": { target: "http://localhost:9260", changeOrigin: true },
    },
  },
  build: {
    outDir: "../system/web/dist",
    emptyOutDir: true,
  },
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
});
