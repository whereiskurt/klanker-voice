import { defineConfig } from "vitest/config";
import { fileURLToPath } from "node:url";

/**
 * Vitest config for the klanker-voice auth webapp.
 *
 * Wires the `@/*` path alias defined in tsconfig.json (paths → src/*) so
 * unit tests can import the same way as Next.js runtime code without
 * pulling in vite-tsconfig-paths as an extra dep.
 *
 * Ported verbatim from run.auth/webapp/vitest.config.ts (D-08/D-09).
 *
 * We deliberately do NOT include a jsdom environment yet — the current suite
 * only tests pure server-side modules / route handlers. Add environment:
 * "jsdom" + a dom dep when a future test needs the DOM.
 */
export default defineConfig({
  test: {
    environment: "node",
    include: [
      "src/__tests__/**/*.test.{ts,tsx}",
      "src/**/__tests__/**/*.test.{ts,tsx}",
    ],
  },
  resolve: {
    alias: {
      "@": fileURLToPath(new URL("./src", import.meta.url)),
    },
  },
});
