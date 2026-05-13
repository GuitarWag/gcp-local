import { defineConfig } from "vitest/config";

export default defineConfig({
  test: {
    globals: true,
    globalSetup: ["./setup.ts"],
    testTimeout: 20000,
    hookTimeout: 20000,
  },
});
