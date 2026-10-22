import { spawn } from "node:child_process";
import { mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { performance } from "node:perf_hooks";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
if (
  process.platform !== "darwin" ||
  process.arch !== "arm64" ||
  process.argv.length !== 2
) {
  throw new Error(
    "Run the fixed packaged startup evaluation on Apple Silicon macOS.",
  );
}
const executable = join(
  root,
  "src-tauri/target/release/bundle/macos/MailPilot.app/Contents/MacOS/mailpilot",
);
const samples = [];
for (let i = 0; i < 10; i++) {
  const dataDir = await mkdtemp(join(tmpdir(), "mailpilot-startup-"));
  try {
    const start = performance.now();
    const child = spawn(executable, ["--smoke-test"], {
      env: {
        ...process.env,
        MAILPILOT_DATA_DIR: dataDir,
        MAILPILOT_GOOGLE_CLIENT_ID: "",
        MAILPILOT_GOOGLE_CLIENT_SECRET: "",
      },
      stdio: ["ignore", "ignore", "pipe"],
    });
    let diagnostics = "";
    let healthMs;
    const deadline = setTimeout(() => child.kill("SIGTERM"), 30000);
    try {
      child.stderr.on("data", (chunk) => {
        diagnostics = (diagnostics + chunk.toString()).slice(-65536);
        if (
          healthMs === undefined &&
          diagnostics.includes('"event":"frontend_health_ok"')
        )
          healthMs = performance.now() - start;
      });
      const code = await new Promise((resolve, reject) => {
        child.on("error", reject);
        child.on("exit", resolve);
      });
      if (
        code !== 0 ||
        healthMs === undefined ||
        !diagnostics.includes('"msg":"core stopped"')
      )
        throw new Error(
          `Packaged startup smoke failed (${code}): ${diagnostics}`,
        );
      samples.push({ healthMs, lifecycleMs: performance.now() - start });
    } finally {
      clearTimeout(deadline);
    }
  } finally {
    await rm(dataDir, { recursive: true, force: true });
  }
}
const quantiles = (key) => {
  const values = samples.map((s) => s[key]).sort((a, b) => a - b);
  return { p50Ms: values[4], p95Ms: values[9], maxMs: values[9] };
};
const report = {
  samples,
  startupToFrontendHealth: quantiles("healthMs"),
  launchThroughCleanShutdown: quantiles("lifecycleMs"),
  database: "fresh synthetic empty database per process",
  oauthConfigured: false,
  note: "Fresh processes with warm filesystem caches; no signed-in Gmail restoration or inference.",
};
await mkdir(join(root, ".cache/evaluation"), { recursive: true });
await writeFile(
  join(root, ".cache/evaluation/startup.json"),
  JSON.stringify(report, null, 2) + "\n",
);
console.log(JSON.stringify(report, null, 2));
