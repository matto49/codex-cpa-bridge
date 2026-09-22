import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  // Tauri serves bundled assets from its own protocol, so root-relative URLs
  // (for example /assets/index.js) do not reliably resolve in the WebView.
  base: "./",
  plugins: [react()],
  clearScreen: false,
  server: {
    port: 1420,
    strictPort: true,
  },
});
