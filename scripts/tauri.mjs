import { spawn } from "node:child_process";
import { createRequire } from "node:module";
import { rustEnvironment } from "./rust-environment.mjs";

const require = createRequire(import.meta.url);
try {
  const child = spawn(
    process.execPath,
    [require.resolve("@tauri-apps/cli/tauri.js"), ...process.argv.slice(2)],
    { env: rustEnvironment(), stdio: "inherit" },
  );
  for (const signal of ["SIGINT", "SIGTERM"]) {
    process.on(signal, () => {
      child.kill(signal);
    });
  }
  child.on("error", (error) => {
    console.error(`Unable to launch Tauri: ${error.message}`);
    process.exitCode = 1;
  });
  child.on("exit", (code, signal) => {
    process.exitCode = code ?? (signal === "SIGINT" ? 130 : 1);
  });
} catch (error) {
  console.error(error.message);
  process.exitCode = 1;
}
