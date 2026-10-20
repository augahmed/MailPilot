# Bundled local embedding resources

Run `npm run prepare:embedding` before a native Apple Silicon build. The preparation
script verifies pinned MiniLM ONNX and ONNX Runtime hashes and packages license
notices. Model/runtime binaries remain ignored by Git. The installed app performs
no download and uses only these fixed sibling resources. `--offline` verifies
already prepared assets without network access. See `docs/LOCAL_EMBEDDING.md`.
