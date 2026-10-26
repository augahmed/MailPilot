import { execFileSync } from "node:child_process";
import { mkdirSync, readFileSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { rustEnvironment } from "./rust-environment.mjs";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const env = rustEnvironment();
const args = process.argv.slice(2);
if (args.length > 1 || (args.length === 1 && args[0] !== "--personal-google"))
  throw new Error("Unsupported sidecar build option");
const personal = args[0] === "--personal-google";
const rustInfo = execFileSync("rustc", ["-vV"], { encoding: "utf8", env });
const host = /^host: (.+)$/m.exec(rustInfo)?.[1];
const target = process.env.TAURI_ENV_TARGET_TRIPLE || host;
if (!host || target !== host)
  throw new Error(
    "Cross-compilation is not configured; build on the target OS/architecture.",
  );
if (process.platform === "darwin" && process.arch === "arm64") {
  execFileSync(
    process.execPath,
    [join(root, "scripts/prepare-embedding.mjs")],
    { env, stdio: "inherit" },
  );
}
const version = JSON.parse(
  readFileSync(join(root, "package.json"), "utf8"),
).version;
if (!/^\d+\.\d+\.\d+$/.test(version))
  throw new Error("Invalid package version");
const outDir = join(root, "src-tauri", "binaries");
mkdirSync(outDir, { recursive: true });
const suffix = process.platform === "win32" ? ".exe" : "";
const output = join(outDir, `mailpilot-core-${target}${suffix}`);
execFileSync(
  "go",
  [
    "build",
    // Explicitly override inherited GOFLAGS tags: ordinary builds exclude the
    // personal resource even when an interrupted run left its input on disk.
    `-tags=${personal ? "personal_google" : ""}`,
    "-trimpath",
    "-ldflags",
    `-s -w -X main.version=${version}`,
    "-o",
    output,
    "./cmd/mailpilot-core",
  ],
  {
    cwd: join(root, "backend"),
    stdio: "inherit",
    env: {
      ...env,
      CGO_ENABLED:
        process.platform === "darwin" && process.arch === "arm64" ? "1" : "0",
      MACOSX_DEPLOYMENT_TARGET: "13.0",
    },
  },
);
console.log(
  `Built ${personal ? "personal" : "standard"} Go sidecar for ${target}`,
);
