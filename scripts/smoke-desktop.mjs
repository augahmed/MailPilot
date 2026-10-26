import { spawn } from "node:child_process";
import { mkdtemp, rm, readFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { personalFixture } from "./personal-fixture.mjs";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const defaultExecutable =
  process.platform === "darwin"
    ? "src-tauri/target/release/bundle/macos/MailPilot.app/Contents/MacOS/mailpilot"
    : `src-tauri/target/release/mailpilot${process.platform === "win32" ? ".exe" : ""}`;
const personal = process.argv.includes("--personal");
const options = process.argv.slice(2).filter((arg) => arg !== "--personal");
const executable = resolve(root, options[0] || defaultExecutable);
if (personal) {
  // Never load a real configured client or credential slot in this smoke mode.
  const binary = await readFile(join(dirname(executable), "mailpilot-core"));
  if (
    !binary.includes(Buffer.from(personalFixture.installed.client_id)) ||
    !binary.includes(Buffer.from(personalFixture.installed.client_secret))
  )
    throw new Error(
      "Personal smoke requires the synthetic-client fixture build.",
    );
}
const dataDir = await mkdtemp(join(tmpdir(), "mailpilot-smoke-"));
let diagnostics = "";
try {
  const env = {
    ...process.env,
    MAILPILOT_DATA_DIR: dataDir,
    MAILPILOT_GOOGLE_CLIENT_ID: "",
    MAILPILOT_GOOGLE_CLIENT_SECRET: "",
  };
  if (personal) {
    delete env.MAILPILOT_GOOGLE_CLIENT_ID;
    delete env.MAILPILOT_GOOGLE_CLIENT_SECRET;
  }
  const child = spawn(
    executable,
    ["--smoke-test", ...(personal ? ["--smoke-personal-config"] : [])],
    {
      env,
      stdio: ["ignore", "ignore", "pipe"],
    },
  );
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
      !diagnostics.includes('"event":"frontend_auth_ok"') ||
      !diagnostics.includes('"event":"frontend_mailbox_ok"') ||
      !diagnostics.includes('"event":"frontend_reader_ok"') ||
      !diagnostics.includes('"event":"frontend_categories_ok"') ||
      !diagnostics.includes('"event":"frontend_classification_ok"') ||
      !diagnostics.includes('"event":"frontend_organization_ok"') ||
      !diagnostics.includes('"event":"frontend_reply_ok"') ||
      !diagnostics.includes('"event":"frontend_ai_settings_ok"') ||
      !diagnostics.includes('"event":"frontend_summary_ok"') ||
      !diagnostics.includes('"event":"frontend_index_ok"') ||
      !diagnostics.includes('"event":"frontend_search_ok"') ||
      !diagnostics.includes('"event":"frontend_answer_ok"') ||
      !diagnostics.includes('"msg":"core stopped"')
    ) {
      throw new Error(
        `Native smoke test failed (exit ${code}).\n${diagnostics}`,
      );
    }
    console.log(
      "Native WebView → Tauri → Go → SQLite health, Gmail status, mailbox, cached reader, category, classification, organization, AI settings, selected-email summary, local index, vector search and maintenance authorization checks passed; core shut down cleanly.",
    );
  } finally {
    clearTimeout(deadline);
  }
} finally {
  await rm(dataDir, { recursive: true, force: true });
}
