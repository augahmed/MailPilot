import { execFile, spawn } from "node:child_process";
import { cp, mkdir, mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const option = process.argv[2];
if (
  process.platform !== "darwin" ||
  process.arch !== "arm64" ||
  !(
    process.argv.length === 2 ||
    (process.argv.length === 3 &&
      [
        "--safety-only",
        "--corpus-only",
        "--search-only",
        "--resume-corpus",
        "--throughput-only",
      ].includes(option))
  )
) {
  throw new Error(
    "Run the offline 10,000-message evaluation on Apple Silicon macOS.",
  );
}
const exec = promisify(execFile);
await exec(process.execPath, ["scripts/prepare-embedding.mjs", "--offline"], {
  cwd: root,
});
const output = resolve(root, ".cache/evaluation");
await mkdir(output, { recursive: true });
const scratch = await mkdtemp(join(tmpdir(), "mailpilot-performance-"));
try {
  const contents = join(scratch, "MailPilot.app/Contents");
  await mkdir(join(contents, "MacOS"), { recursive: true });
  await cp(
    resolve(root, "src-tauri/resources/embedding"),
    join(contents, "Resources/embedding"),
    { recursive: true },
  );
  const binary = join(contents, "MacOS/mailpilot-core");
  await exec(
    "go",
    ["test", "-c", "-tags=index_native", "-o", binary, "./internal/app"],
    {
      cwd: resolve(root, "backend"),
      env: {
        ...process.env,
        CGO_ENABLED: "1",
        GOEXPERIMENT: "cgocheck2",
        GOPROXY: "off",
      },
      timeout: 180000,
      maxBuffer: 1024 * 1024,
    },
  );
  const child = spawn(
    "/usr/bin/sandbox-exec",
    [
      "-p",
      "(version 1) (allow default) (deny network*)",
      binary,
      option === "--safety-only"
        ? "-test.run=^TestNativePerformanceCancellation$"
        : option === "--search-only"
          ? "-test.run=^TestNativePerformanceSearch10K$"
          : option === "--throughput-only"
            ? "-test.run=^TestNativePerformanceThroughput$"
            : "-test.run=^TestNativePerformance10K$",
      "-test.v",
      "-test.timeout=3h",
      `-performance.output=${output}`,
      ...(option === "--corpus-only" ? ["-performance.corpus-only"] : []),
      ...(option === "--resume-corpus" ? ["-performance.reuse"] : []),
    ],
    {
      cwd: resolve(root, "backend/internal/app"),
      stdio: "inherit",
    },
  );
  const code = await new Promise((resolve, reject) => {
    child.on("error", reject);
    child.on("exit", resolve);
  });
  if (code !== 0)
    throw new Error(`Synthetic performance evaluation failed (${code}).`);
  console.log(`Evaluation artifacts: ${output}`);
} finally {
  await rm(scratch, { recursive: true, force: true });
}
