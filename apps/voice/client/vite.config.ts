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
    // happy-dom instead of jsdom: jsdom@29's transitive @exodus/bytes is
    // ESM-only and jsdom require()s it, which fails under Node 22.1.0
    // (require(ESM) only became default in 22.12+/23). happy-dom avoids that
    // chain entirely and runs the same component tests.
    environment: "happy-dom",
    include: ["src/**/*.test.ts", "src/**/*.test.tsx"],
  },
});
