import assert from "node:assert/strict";
import { execFileSync, spawnSync } from "node:child_process";
import {
  readFileSync,
  statSync,
  copyFileSync,
  cpSync,
  mkdirSync,
  mkdtempSync,
  rmSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { libraryFingerprint, sha256 } from "./embedding-artifacts.mjs";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const app = join(root, "src-tauri/target/release/bundle/macos/MailPilot.app");
const resources = join(app, "Contents/Resources/embedding");
const binary = join(app, "Contents/MacOS/mailpilot-core");
const manifest = JSON.parse(
  readFileSync(
    join(root, "backend/internal/embedding/model/manifest.json"),
    "utf8",
  ),
);
assert.equal(
  sha256(readFileSync(join(resources, "model.onnx"))),
  manifest.modelSha256,
);
const library = join(resources, "libonnxruntime.dylib");
assert.equal(
  libraryFingerprint(readFileSync(library)),
  manifest.libraryCodeSha256,
);
for (const file of [
  "MINILM_LICENSE.txt",
  "ONNX_RUNTIME_LICENSE.txt",
  "ONNX_RUNTIME_THIRD_PARTY_NOTICES.txt",
  "TOKENIZER_NOTICE.md",
])
  assert.ok(statSync(join(resources, file)).size > 0);
execFileSync("/usr/bin/codesign", ["--verify", "--deep", "--strict", app]);
// Explicitly re-sign a copy to prove signature changes preserve the pinned code.
const temp = mkdtempSync(join(tmpdir(), "mailpilot-resign-"));
try {
  const signed = join(temp, "runtime.dylib");
  copyFileSync(library, signed);
  execFileSync(
    "/usr/bin/codesign",
    [
      "--force",
      "--sign",
      "-",
      "--identifier",
      "mailpilot.runtime.test",
      signed,
    ],
    { stdio: "pipe" },
  );
  assert.equal(
    libraryFingerprint(readFileSync(signed)),
    manifest.libraryCodeSha256,
  );
  // Exercise Tauri's copied sidecar layouts, not only the source binaries path
  // and installed .app. All layouts must load the same verified model offline.
  const development = join(temp, "src-tauri");
  cpSync(resources, join(development, "resources/embedding"), {
    recursive: true,
  });
  for (const profile of ["debug", "release"]) {
    const directory = join(development, "target", profile);
    mkdirSync(directory, { recursive: true });
    const copied = join(directory, "mailpilot-core");
    copyFileSync(binary, copied);
    offlineInference(copied);
  }
} finally {
  rmSync(temp, { recursive: true, force: true });
}
// Deny every network operation at OS level, not merely an HTTP proxy setting.
function offlineInference(executable) {
  const run = spawnSync(
    "/usr/bin/sandbox-exec",
    [
      "-p",
      "(version 1) (allow default) (deny network*)",
      executable,
      "--embedding-self-test",
    ],
    {
      encoding: "utf8",
      timeout: 60000,
      env: {
        ...process.env,
        HTTP_PROXY: "http://127.0.0.1:1",
        HTTPS_PROXY: "http://127.0.0.1:1",
        ALL_PROXY: "http://127.0.0.1:1",
      },
    },
  );
  if (run.status !== 0)
    throw new Error(`Offline inference failed (${run.status}): ${run.stderr}`);
  const result = JSON.parse(run.stdout);
  assert.equal(result.ok, true);
  assert.equal(result.descriptor.dimensions, 384);
  return result;
}
const result = offlineInference(binary);
console.log(
  JSON.stringify(
    {
      ...result,
      modelBytes: statSync(join(resources, "model.onnx")).size,
      runtimeBytes: statSync(library).size,
      verified: "signed-app-network-denied",
      developmentLayoutsVerified: ["debug", "release"],
    },
    null,
    2,
  ),
);
