import { spawn } from "node:child_process";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const [mode, ...extra] = process.argv.slice(2);
if (
  !["dev", "build"].includes(mode) ||
  extra.some((arg) => arg !== "--reset-demo") ||
  extra.length > 1 ||
  (mode === "build" && extra.length)
) {
  console.error("Usage: demo.mjs dev [--reset-demo] or demo.mjs build");
  process.exit(1);
}
const config = {
  identifier: "com.mailpilot.desktop.demo",
  productName: "MailPilot Demo",
};
const args = [
  join(root, "scripts/tauri.mjs"),
  mode,
  "--config",
  JSON.stringify(config),
];
// Tauri's first separator selects runner args; the second selects app args.
if (mode === "dev") args.push("--", "--", "--demo", ...extra);
else if (process.platform === "darwin") args.push("--bundles", "app");
const env = { ...process.env };
delete env.MAILPILOT_GOOGLE_CLIENT_ID;
delete env.MAILPILOT_GOOGLE_CLIENT_SECRET;
delete env.MAILPILOT_DATA_DIR;
const child = spawn(process.execPath, args, {
  cwd: root,
  env,
  stdio: "inherit",
});
for (const signal of ["SIGINT", "SIGTERM"])
  process.on(signal, () => child.kill(signal));
child.on("error", () => {
  console.error("Unable to launch demo workflow");
  process.exitCode = 1;
});
child.on("exit", (code, signal) => {
  process.exitCode = code ?? (signal === "SIGINT" ? 130 : 1);
});
