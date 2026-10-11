import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [react()],
  clearScreen: false,
  build: { target: "es2022" },
  test: { environment: "jsdom", setupFiles: ["./src/test-setup.ts"] },
});
