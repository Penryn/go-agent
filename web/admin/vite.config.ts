import { fileURLToPath } from "node:url";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

const root = fileURLToPath(new URL(".", import.meta.url));

export default defineConfig({
  base: "/admin/",
  plugins: [react()],
  build: {
    outDir: fileURLToPath(new URL("../../internal/app/admin-dist", import.meta.url)),
    emptyOutDir: true,
  },
  server: {
    host: "127.0.0.1",
    port: 5173,
    proxy: {
      "/admin/api": "http://127.0.0.1:8088",
    },
  },
});
