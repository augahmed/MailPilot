import { execFile } from "node:child_process";
import { cp, mkdir, mkdtemp, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
const exec = promisify(execFile);
const root = dirname(dirname(fileURLToPath(import.meta.url)));
if (
  process.platform !== "darwin" ||
  process.arch !== "arm64" ||
  !(
    process.argv.length === 2 ||
    (process.argv.length === 3 && process.argv[2] === "--tune")
  )
) {
  throw new Error(
    "Run the fixed native classification qualification on Apple Silicon macOS.",
  );
}
await exec(process.execPath, ["scripts/prepare-embedding.mjs", "--offline"], {
  cwd: root,
});
const scratch = await mkdtemp(
  join(tmpdir(), "mailpilot-classification-native-"),
);
try {
  const contents = join(scratch, "MailPilot.app", "Contents");
  await mkdir(join(contents, "MacOS"), { recursive: true });
  await mkdir(join(contents, "Resources"), { recursive: true });
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
      env: { ...process.env, CGO_ENABLED: "1", GOEXPERIMENT: "cgocheck2" },
      timeout: 180000,
      maxBuffer: 1024 * 1024,
    },
  );
  const result = await exec(
    "/usr/bin/sandbox-exec",
    [
      "-p",
      "(version 1) (allow default) (deny network*)",
      binary,
      "-test.run=^TestNativeSemanticClassificationEvaluation$",
      "-test.v",
      "-test.timeout=120s",
      ...(process.argv[2] === "--tune" ? ["-classification.tune"] : []),
    ],
    {
      cwd: resolve(root, "backend/internal/app"),
      timeout: 150000,
      maxBuffer: 1024 * 1024,
    },
  );
  const match = result.stdout.match(
    /CLASSIFICATION_QUALIFICATION (\{[^\n]+\})/,
  );
  if (
    !match ||
    !result.stdout.includes(
      "--- PASS: TestNativeSemanticClassificationEvaluation",
    )
  )
    throw new Error("Missing native classification evidence");
  const report = {
    ...JSON.parse(match[1]),
    verification: "network-denied-native-classification",
    strictCgoChecks: true,
  };
  await mkdir(resolve(root, ".cache"), { recursive: true });
  await writeFile(
    resolve(root, ".cache/semantic-classification-qualification.json"),
    JSON.stringify(report, null, 2) + "\n",
  );
  if (process.argv[2] === "--tune") {
    await writeFile(
      resolve(root, "backend/internal/classification/semantic_policy.json"),
      JSON.stringify(report.policy, null, 2) + "\n",
    );
  }
  console.log(JSON.stringify(report, null, 2));
} finally {
  await rm(scratch, { recursive: true, force: true });
}
