import { spawn } from "node:child_process";
import {
  openSync,
  writeFileSync,
  closeSync,
  unlinkSync,
  mkdirSync,
  chmodSync,
  rmSync,
  renameSync,
} from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { readGoogleClient } from "./google-client.mjs";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const resource = join(
  root,
  "backend/internal/config/personal-google-client.json",
);
let owned = false;
try {
  const [mode, flag, path, ...extra] = process.argv.slice(2);
  if (
    !["dev", "build"].includes(mode) ||
    flag !== "--google-client-json" ||
    !path ||
    extra.some((arg) => arg !== "--no-sign") ||
    extra.length > 1 ||
    (mode === "dev" && extra.length)
  ) {
    throw new Error(
      "Usage: npm run dev:personal (or build:personal) -- --google-client-json /absolute/path/client.json [--no-sign for builds]",
    );
  }
  const client = readGoogleClient(path, root);
  if (
    mode === "build" &&
    (process.platform !== "darwin" || process.arch !== "arm64")
  )
    throw new Error(
      "Personal bundle builds currently require Apple Silicon macOS.",
    );
  let fd;
  try {
    fd = openSync(resource, "wx", 0o600);
    owned = true;
    writeFileSync(fd, JSON.stringify(client));
  } catch {
    throw new Error(
      "Cannot prepare personal build input. Stop other personal builds; if an interrupted run left backend/internal/config/personal-google-client.json, remove that ignored file before retrying.",
    );
  } finally {
    if (fd !== undefined) closeSync(fd);
  }
  const command = mode === "dev" ? "beforeDevCommand" : "beforeBuildCommand";
  const tail =
    mode === "dev" ? "npm run dev:frontend" : "npm run build:frontend";
  // This command is fixed trusted text. No input path or client value enters it.
  const overlay = {
    build: {
      [command]: `npm run build:icons && node scripts/build-sidecar.mjs --personal-google && ${tail}`,
    },
  };
  const env = { ...process.env };
  delete env.MAILPILOT_GOOGLE_CLIENT_ID;
  delete env.MAILPILOT_GOOGLE_CLIENT_SECRET;
  const args = [
    join(root, "scripts/tauri.mjs"),
    mode,
    "--config",
    JSON.stringify(overlay),
  ];
  if (mode === "build" && process.platform === "darwin")
    args.push("--bundles", "app");
  args.push(...extra);
  console.log(
    "Using validated desktop-client configuration for this personal run. Keep its resulting application private.",
  );
  const child = spawn(process.execPath, args, {
    cwd: root,
    env,
    stdio: "inherit",
  });
  const forward = (signal) => child.kill(signal);
  const onInt = () => forward("SIGINT");
  const onTerm = () => forward("SIGTERM");
  process.on("SIGINT", onInt);
  process.on("SIGTERM", onTerm);
  try {
    process.exitCode = await new Promise((resolve, reject) => {
      child.on("error", () =>
        reject(new Error("Unable to launch the personal desktop workflow.")),
      );
      child.on("exit", (code, signal) =>
        resolve(code ?? (signal === "SIGINT" ? 130 : 1)),
      );
    });
    if (mode === "build" && process.exitCode === 0) {
      const directory = join(root, ".local/personal");
      mkdirSync(directory, { recursive: true, mode: 0o700 });
      chmodSync(directory, 0o700);
      const destination = join(directory, "MailPilot.app");
      rmSync(destination, { recursive: true, force: true });
      // Moving the completed bundle preserves its signature; no post-signing
      // configuration is injected. It is removed from the ordinary release path.
      renameSync(
        join(root, "src-tauri/target/release/bundle/macos/MailPilot.app"),
        destination,
      );
      console.log(
        "Personal application: .local/personal/MailPilot.app (owner use only)",
      );
    }
  } finally {
    process.off("SIGINT", onInt);
    process.off("SIGTERM", onTerm);
  }
} catch (error) {
  console.error(error.message);
  process.exitCode = 1;
} finally {
  if (owned) {
    try {
      unlinkSync(resource);
    } catch {
      console.error(
        "Remove the ignored personal build input before another personal run.",
      );
      process.exitCode = 1;
    }
  }
}
