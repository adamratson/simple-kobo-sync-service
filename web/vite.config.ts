import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// In dev, run the Go server on :8080 and `npm run dev`; API calls are proxied.
export default defineConfig({
  plugins: [react()],
  server: {
    proxy: { "/api": "http://localhost:8080" },
  },
});
