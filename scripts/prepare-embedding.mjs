// Build-time acquisition only. Installed MailPilot never downloads these assets.
import { execFileSync } from "node:child_process";
import {
  copyFileSync,
  existsSync,
  lstatSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  renameSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { libraryFingerprint, sha256 } from "./embedding-artifacts.mjs";

const root = dirname(dirname(fileURLToPath(import.meta.url)));
const manifestPath = join(
  root,
  "backend/internal/embedding/model/manifest.json",
);
const manifest = JSON.parse(readFileSync(manifestPath, "utf8"));
if (process.argv.slice(2).some((arg) => arg !== "--offline"))
  throw new Error("Only --offline is supported");
if (process.platform !== "darwin" || process.arch !== "arm64")
  throw new Error("Embedding packaging currently targets Apple Silicon macOS");
const offline = process.argv.includes("--offline");
const output = join(root, "src-tauri/resources/embedding");
const cache = join(root, ".cache/embedding");
mkdirSync(output, { recursive: true });
mkdirSync(cache, { recursive: true });
const model = join(output, "model.onnx");
const library = join(output, "libonnxruntime.dylib");
function regular(path) {
  return (
    existsSync(path) &&
    lstatSync(path).isFile() &&
    !lstatSync(path).isSymbolicLink()
  );
}
function ready() {
  try {
    return (
      [
        "ONNX_RUNTIME_LICENSE.txt",
        "ONNX_RUNTIME_THIRD_PARTY_NOTICES.txt",
        "ONNX_RUNTIME_PRIVACY.md",
      ].every((name) => regular(join(output, name))) &&
      regular(model) &&
      regular(library) &&
      lstatSync(library).size >= manifest.libraryBytes - 1024 * 1024 &&
      lstatSync(library).size <= manifest.libraryBytes + 1024 * 1024 &&
      lstatSync(model).size === manifest.modelBytes &&
      sha256(readFileSync(model)) === manifest.modelSha256 &&
      libraryFingerprint(readFileSync(library)) === manifest.libraryCodeSha256
    );
  } catch {
    return false;
  }
}
if (!ready()) {
  if (offline)
    throw new Error(
      "Missing or corrupt bundled embedding assets; prepare them during a connected build",
    );
  function download(name, url, digest, size) {
    const target = join(cache, name);
    if (
      regular(target) &&
      lstatSync(target).size === size &&
      sha256(readFileSync(target)) === digest
    )
      return target;
    const partial = target + ".partial";
    try {
      // Fixed public origins and pinned content, never caller-controlled URLs.
      execFileSync(
        "/usr/bin/curl",
        [
          "--fail",
          "--location",
          "--proto",
          "=https",
          "--proto-redir",
          "=https",
          "--max-time",
          "300",
          "--max-filesize",
          String(size),
          "--silent",
          "--show-error",
          url,
          "-o",
          partial,
        ],
        { stdio: "inherit" },
      );
      if (
        lstatSync(partial).size !== size ||
        sha256(readFileSync(partial)) !== digest
      )
        throw new Error("Embedding artifact checksum mismatch");
      renameSync(partial, target);
    } finally {
      rmSync(partial, { force: true });
    }
    return target;
  }
  const downloadedModel = download(
    "model.onnx",
    manifest.modelUrl,
    manifest.modelSha256,
    manifest.modelBytes,
  );
  const archive = download(
    "runtime.tgz",
    manifest.runtimeUrl,
    manifest.runtimeArchiveSha256,
    manifest.runtimeArchiveBytes,
  );
  const staging = mkdtempSync(join(tmpdir(), "mailpilot-runtime-"));
  try {
    const base = `onnxruntime-osx-arm64-${manifest.runtimeVersion}`;
    const members = [
      `${base}/lib/libonnxruntime.${manifest.runtimeVersion}.dylib`,
      `${base}/LICENSE`,
      `${base}/ThirdPartyNotices.txt`,
      `${base}/Privacy.md`,
    ];
    execFileSync("/usr/bin/tar", ["-xzf", archive, "-C", staging, ...members]);
    const runtime = join(staging, members[0]);
    if (
      sha256(readFileSync(runtime)) !== manifest.librarySha256 ||
      libraryFingerprint(readFileSync(runtime)) !== manifest.libraryCodeSha256
    )
      throw new Error("Unexpected runtime contents");
    copyFileSync(downloadedModel, model);
    copyFileSync(runtime, library);
    for (const [source, name] of [
      ["LICENSE", "ONNX_RUNTIME_LICENSE.txt"],
      ["ThirdPartyNotices.txt", "ONNX_RUNTIME_THIRD_PARTY_NOTICES.txt"],
      ["Privacy.md", "ONNX_RUNTIME_PRIVACY.md"],
    ])
      copyFileSync(join(staging, base, source), join(output, name));
  } finally {
    rmSync(staging, { recursive: true, force: true });
  }
}
copyFileSync(manifestPath, join(output, "manifest.json"));
copyFileSync(
  join(root, "backend/internal/mail/wordpiece/assets/LICENSE.txt"),
  join(output, "MINILM_LICENSE.txt"),
);
copyFileSync(
  join(root, "backend/internal/mail/wordpiece/assets/NOTICE.md"),
  join(output, "TOKENIZER_NOTICE.md"),
);
writeFileSync(
  join(output, "PACKAGED.txt"),
  "Pinned CPU embedding assets. No runtime downloads. Tokenizer/configuration are embedded in the Go core.\n",
);
if (!ready()) throw new Error("Embedding preparation failed verification");
console.log(
  `Embedding assets verified: ${manifest.modelBytes} model bytes; ${lstatSync(library).size} runtime bytes; offline=${offline}`,
);
