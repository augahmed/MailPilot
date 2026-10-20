# Local embedding provider

Implemented provider scope: one CPU MiniLM provider, independent of SQLite,
mailbox enumeration, classification, retrieval, external AI and credentials.
The separate [local indexing coordinator](LOCAL_INDEXING.md) now invokes this
provider after explicit Start/Resume and persists bounded batches. The provider
itself owns no scheduler/search/SQLite or external AI consent.

## Model and native runtime

Keep `sentence-transformers/all-MiniLM-L6-v2` at revision
`1110a243fdf4706b3f48f1d95db1a4f5529b4d41`. Use its unquantized FP32 ONNX export,
attention-mask mean pooling and L2 normalization to 384 dimensions. It matches the
existing pinned WordPiece tokenizer and the English-first product target. No
replacement model, alternate quantized variant or second tokenizer is introduced.
Synthetic relevance/batch tests qualify correctness here, not mailbox search quality.
[Official model](https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2/tree/1110a243fdf4706b3f48f1d95db1a4f5529b4d41),
[pooling guidance](https://www.sbert.net/docs/sentence_transformer/usage/efficiency.html).

[manifest.json](../backend/internal/embedding/model/manifest.json) pins model bytes,
source revision, SHA-256, runtime archive/library fingerprints, dimension, pooling,
minimum OS and binding version. Go embeds this manifest; the runtime ignores editable
resource manifests. Downloaded weights/libraries stay ignored by Git.

- Model: **90,405,214 bytes** (86.22 MiB), Apache-2.0.
- Native library: **35,138,784 bytes** (33.51 MiB) before release signing; ONNX Runtime
  **1.23.2**, official arm64 archive, MIT plus bundled third-party notices.
- Tokenizer: the existing embedded 231,508-byte vocabulary/configuration/notices.
- Go binding: `github.com/yalue/onnxruntime_go v1.23.0`, pinned in go.mod/go.sum.
  Its C API 1.22 headers work with this qualified runtime's backward-compatible API;
  do not assume arbitrary binding/runtime versions are interchangeable.
- This runtime requires **Apple Silicon macOS 13.4+**. The app retains its 13.0
  minimum; embeddings fail with `ErrUnavailable` on 13.0–13.3. Other OS/architecture
  or CGO-disabled builds retain ordinary features with an unavailable native adapter.
- The provider enables CPU only, two intra-op threads, one inter-op thread,
  sequential execution, no spinning, no CPU arena/memory-pattern cache. It does not
  append CoreML/GPU/plugin providers. The upstream dylib still links Apple system
  CoreML/Metal frameworks; no extra downloaded frameworks are needed. Telemetry is
  explicitly disabled immediately after environment creation.

## Internal API and lifecycle

[embedding.Provider](../backend/internal/embedding/provider.go) exposes:

```go
provider, err := embedding.New() // tokenizer only; no model/library load
// handle err
identity := provider.Descriptor()
vectors, err := provider.EmbedDocument(ctx, []string{mail.ModelInput(chunk)})
query, err := provider.EmbedQuery(ctx, "When is the project meeting?")
err = provider.Close(shutdownContext)
```

The provider receives canonical document strings from the caller. It adds only
CLS/SEP and padding, never another email context prefix. MiniLM is symmetric:
queries use the same encoder/pooling without invented query instructions. Reuse
`mail.ModelInput` and the one tokenizer rather than building another preprocessing
or persistence path. The shared pure-Go `embedding/model` identity is also consumed
by `mail.ChunkModel`; native code imports neither mail business logic nor repositories.

Descriptor fields explicitly include model ID/version, dimension, model/tokenizer
SHA-256, runtime version, symmetric-query support and a SHA-256 encoding fingerprint.
That fingerprint includes artifacts, Unicode normalization tables, tokenizer, input
policy, pooling/normalization and runtime version. The new FP32 artifact version
replaces the earlier preliminary pending-vector version; a subsequent manual cache
pass updates model state, retaining old model-versioned rows. No migration, original
mail, historical vector or journal format is rewritten.

A batch contains **1–4 inputs**, each at most **256 tokens including CLS/SEP** and
65 KiB including context. Oversized/blank/unsupported inputs fail before model
initialization; there is no silent truncation. Padding is masked during pooling.
Outputs must have the exact batch/sequence/384 shape; finite, nonzero pooled vectors
are L2-normalized and checked within 1e-4 of unit squared norm. Safe sentinels replace
native diagnostics; do not log mail, tokens or raw native errors.

Initialization is lazy and single-flight; calls serialize through one bounded
native session. A second simultaneous native provider is unavailable rather than
loading another copy. Each request has a maximum 30-second context and respects an
earlier caller deadline while waiting, tokenizing and running. Each native run uses
its own `OrtRunOptions`; cancellation sets ORT's terminate flag, and its callback is
joined before releasing options/tensors. Go tensor backing arrays are explicitly
pinned until their native values are released. A canceled run does not poison later runs.

`Close(ctx)` revokes new/waiting calls, cancels active runs, joins initialization and
work, then releases session/environment once. If its context expires, cleanup
continues and a later Close can join it. **ORT session construction itself is not
preemptible.** A caller can return cancellation while lazy construction continues;
shutdown waits for native construction to return before releasing it. Bounded inputs
and disabled arenas reduce resource use; this is not a hard process memory limit or
an absolute native shutdown-time guarantee.

## Resource and signing boundary

There is no exported model path, executable path, endpoint or download parameter.
Production resources resolve only relative to the running fixed Go executable:

- Installed: `Contents/MacOS/mailpilot-core` → `Contents/Resources/embedding`.
- Native development: `src-tauri/binaries/mailpilot-core-aarch64-apple-darwin` →
  `src-tauri/resources/embedding`.

Working directory, data directory, environment variables and JavaScript cannot select
models or libraries. Symlinks and unexpected files/lengths are rejected. Model bytes
are SHA-256 verified and passed to ORT in memory; no caller-controlled ONNX path or
custom-op registration exists. Only the fixed verified local dylib is loaded.

Apple signing modifies Mach-O signatures. The provider verifies a pinned SHA-256 of
all pre-signature bytes, normalizing only signature length and `__LINKEDIT` sizes
changed by codesign. The signature offset remains hashed. Code/data/load-command
changes fail verification; actual signatures are independently checked by macOS.
Both Go/Node tests cover tampering; the packaging smoke re-signs a copy and verifies
that the fingerprint is stable. The pinned upstream archive/full-library hashes are
also checked during build preparation.

## Reproducible build and offline verification

A developer needs Go, Xcode command-line tools/CGO, Node/Tauri/Rust and network access
only for the initial build preparation. Installed users need no Python, runtime
installation or first-use download. Build scripts acquire fixed public HTTPS URLs,
verify pinned checksums and bundle notices. No model/runtime binaries are committed.

```sh
npm run prepare:embedding
node scripts/prepare-embedding.mjs --offline
node --test scripts/embedding-artifacts.test.mjs
cd backend
go test ./...
GOEXPERIMENT=cgocheck2 go test -race -tags embedding_native ./internal/embedding/...
go vet ./...
cd ..
npm run build -- --bundles app --no-sign
codesign --force --deep --sign - src-tauri/target/release/bundle/macos/MailPilot.app
npm run test:embedding:package
npm run test:desktop
```

`build:sidecar` prepares/verifies cached resources and enables CGO only on Apple
Silicon macOS; existing non-native builds use the unavailable adapter. The Tauri
resource mapping packages weights, dylib and licenses under the fixed `embedding`
directory. Native runtime resolution needs no environment library search path.

The core's exact `--embedding-self-test` flag accepts no extra arguments or input.
It uses only fixed synthetic strings, opens no database/authentication session and
prints bounded descriptor/timing/memory data. It is not available through WebView
IPC. `test:embedding:package` runs it from the signed .app under macOS sandbox-exec
with **all network operations denied**, verifies artifact fingerprints/licenses,
unit vectors, synthetic relevance and native re-signing compatibility.

## Qualification results and limits

Measured on Apple Silicon arm64/macOS 13.4, optimized native .app, CPU execution,
with all network denied by macOS sandbox-exec. One synthetic run on 2026-10-19:

| Measurement                                      | Observed                                                |
| ------------------------------------------------ | ------------------------------------------------------- |
| Packaged app                                     | 143.98 MiB (Tauri reported, ad-hoc local bundle)        |
| Model / native library                           | 90,405,214 / 35,138,784 bytes                           |
| Baseline peak RSS before model load              | 17,317,888 bytes (16.52 MiB)                            |
| Peak RSS through cold/warm/max batch             | **387,465,216 bytes (369.52 MiB)**                      |
| Go heap after forced diagnostic GC               | 4,173,336 bytes (3.98 MiB); excludes native allocations |
| Cold two-document call, including initialization | 240.426 ms                                              |
| Warm short query, mean of five calls             | 1.748 ms                                                |
| Maximum 4 × 256-token call                       | 200.904 ms                                              |
| Synthetic related/unrelated cosine               | 0.7073 / 0.0513                                         |

RSS is the process high-water mark, including transient model verification/loading
and native working buffers; it is not a steady-state allocation or a memory cap.
The Go heap number alone does not represent inference memory. Timings are single-run
observations under concurrent local validation, not percentile guarantees. Cold
means a fresh process/session; artifact verification has warmed filesystem caches. Native
real-model tests also passed under Go's race detector; fake-model lifecycle/error
checks, full Go regression/race/vet/build, package signature checks and the ordinary
WebView → Tauri → Go → SQLite desktop smoke passed.
Only local ad-hoc signing is qualified; no Developer ID certificate, notarization,
Gatekeeper distribution approval or Intel/other-platform inference is claimed.
CPU memory/latency measurements are single-host synthetic observations, not a
10,000-message throughput benchmark or a held-out retrieval/classification evaluation.
