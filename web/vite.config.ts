import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

export default defineConfig({
  plugins: [react()],
  build: {
    outDir: "../internal/gateway/assets",
    emptyOutDir: true,
    sourcemap: false,
    assetsInlineLimit: 4096
  }
});
