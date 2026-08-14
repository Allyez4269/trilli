import { defineConfig } from "vite";
import react from "@vitejs/plugin-react-swc";
import path from "path";

// Vite is build-time only. The Go binary embeds dist/ and serves both the
// SPA and the API at runtime on port 8081 — no Node process in production.
export default defineConfig({
  server: {
    host: "::",
    port: 5173,
    proxy: {
      "/api": { target: "http://localhost:8081", changeOrigin: true },
    },
  },
  build: {
    outDir: "../system/web/dist",
    emptyOutDir: true,
    rollupOptions: {
      output: {
        // Split large vendor libraries out of the main app bundle so the
        // initial chunk stays small and well under the 500 kB warning, and so
        // each vendor caches independently across deploys (app code changes
        // far more often than these libs). xlsx is already its own lazy chunk
        // via dynamic import() in SheetPreview, so it isn't bucketed here.
        manualChunks(id) {
          if (!id.includes("node_modules")) return;
          if (id.includes("@stripe")) return "stripe";
          if (id.includes("@tanstack")) return "tanstack";
          if (id.includes("@radix-ui")) return "radix";
          if (id.includes("lucide-react")) return "icons";
          if (
            id.includes("/react/") ||
            id.includes("/react-dom/") ||
            id.includes("/react-router") ||
            id.includes("/scheduler/")
          ) {
            return "react";
          }
        },
      },
    },
  },
  plugins: [react()],
  resolve: {
    alias: {
      "@": path.resolve(__dirname, "./src"),
    },
  },
});
