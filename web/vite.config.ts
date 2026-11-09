import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// 构建产物直接落到 Go 的 embed 目录，发布时只有一个二进制。
export default defineConfig({
  plugins: [react()],
  build: { outDir: "../server/internal/webui/dist", emptyOutDir: true, assetsInlineLimit: 0, chunkSizeWarningLimit: 800 },
  server: { port: 5173, proxy: { "/api": "http://127.0.0.1:8787", "/mcp": "http://127.0.0.1:8787" } },
});
