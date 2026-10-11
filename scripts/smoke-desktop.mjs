import { spawn } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const defaultExecutable =
  process.platform === "darwin"
    ? "src-tauri/target/release/bundle/macos/MailPilot.app/Contents/MacOS/mailpilot"
    : `src-tauri/target/release/mailpilot${process.platform === "win32" ? ".exe" : ""}`;
const executable = resolve(root, process.argv[2] || defaultExecutable);
const dataDir = await mkdtemp(join(tmpdir(), "mailpilot-smoke-"));
let diagnostics = "";
try {
  const child = spawn(executable, ["--smoke-test"], {
    env: { ...process.env, MAILPILOT_DATA_DIR: dataDir },
    stdio: ["ignore", "ignore", "pipe"],
  });
  const deadline = setTimeout(() => {
    child.kill("SIGTERM");
  }, 30000);
  try {
    child.stderr.on("data", (chunk) => {
      diagnostics = (diagnostics + chunk.toString()).slice(-65536);
    });
    const code = await new Promise((resolve, reject) => {
      child.on("error", reject);
      child.on("exit", resolve);
    });
    if (
      code !== 0 ||
      !diagnostics.includes('"event":"frontend_health_ok"') ||
      !diagnostics.includes('"msg":"core stopped"')
    ) {
      throw new Error(
        `Native smoke test failed (exit ${code}).\n${diagnostics}`,
      );
    }
    console.log(
      "Native WebView → Tauri → Go → SQLite health check passed; core shut down cleanly.",
    );
  } finally {
    clearTimeout(deadline);
  }
} finally {
  await rm(dataDir, { recursive: true, force: true });
}
