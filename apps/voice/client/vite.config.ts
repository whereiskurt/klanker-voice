/// <reference types="vitest/config" />
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";

// klanker-voice client — served by the voice FastAPI app's StaticFiles mount
// (apps/voice/server.py CLIENT_DIST_DIR = apps/voice/client/dist). base "/" and
// outDir "dist" match that mount exactly (D-01/02/03).
export default defineConfig({
  plugins: [react()],
  base: "/",
  build: {
    outDir: "dist",
    emptyOutDir: true,
  },
  test: {
    environment: "jsdom",
    include: ["src/**/*.test.ts", "src/**/*.test.tsx"],
  },
});
