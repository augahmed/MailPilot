# Local semantic classification

MailPilot reviews cached email with a versioned evidence-aware `HybridClassifier`. The semantic-only gate remains cosine **0.45**, with a lead strictly greater than **0.05**. The shipped rule scores remain **0.55–0.65**; they represent heuristic evidence strength, not estimated correctness probabilities.

The policy is:

1. Conflicting specific rule matches abstain. A semantic score cannot erase that conflict.
2. Without a configured local model, an unambiguous rule at **0.60** or above can select a category. The broader two-condition 0.55 newsletter rule needs corroboration; three-condition rules can work offline without a model.
3. With a model, an explicit rule exclusion vetoes the leading semantic category. There is no fallback to a runner-up after a veto.
4. A rule at **0.55** or above and the same leading semantic category can reinforce one another when cosine is at least **0.35** and the lead exceeds **0.05**. Disagreement or a near tie abstains. Rules and cosine scores are never summed or averaged.
5. An unambiguous rule at **0.60** can stand alone when semantic scores are below the corroboration gate. Otherwise semantic-only selection still requires **0.45 / 0.05**. Model errors remain explicit errors, not a fabricated low score or silent rule fallback.

These separate evidence gates fix the former 0.85 rule-threshold mismatch without globally lowering the semantic minimum. Thresholds remain trusted Go configuration, not email-controlled input. Per-category thresholds and probability calibration are deferred: the available synthetic data is too small and correlated to justify fitting them. Default categories and user descriptions are unchanged. [Measured results and remaining failures](SEMANTIC_EVALUATION.md) include the original corpus, expanded development set and reserved validation set.

Rules and embeddings now use the same bounded quote/signature-trimmed body, including HTML-to-text fallback. Positive rule phrases reject a small set of immediate English negations; exclusions still match literally. This is not a general negation parser. Reply/forward subject prefixes continue to suppress starter rule matching while allowing semantic review.

The review persists the hybrid engine/version, actual selected engine, decision explanation, selected category if any, score kind and top semantic candidates. Corroborated decisions use `used_classifier: HybridClassifier`, preserve the **raw cosine** in the existing `cosine_similarity` field and append the rule strength, matched conditions and decision gates to reasons. Direct rule decisions retain `rule_confidence` for wire/storage compatibility but the UI displays **Rule strength**, not a percentage. No new schema, migration, dependency or wire field is needed. Abstentions also retain metadata and explanations. Rule configuration contents and algorithm revisions are fingerprinted, so forgetting to update a human version label cannot leave changed rules looking current. Model/policy changes invalidate previous reviews and organization previews.

Cosines remain ranked deterministically with category-ID tie breaks; negative candidates are retained. At least two candidates are considered internally even when presentation is configured for only one. Default presentation is three. Disabled categories cannot be selected. Classification suggestions alone never assign categories or modify Gmail; organization still requires its separate preview/confirm/journal/reconcile/undo workflow.

Review responses are capped at 48 KiB before framing. Oversized evidence remains persisted but is reported as an explicit display error. Reviews use local cached data; the optional model and no-model paths need no external AI provider or key.

## Development setup

The Go adapter in [minilm](../backend/internal/embedding/minilm/provider.go) implements the unchanged four-method `EmbeddingProvider`. It runs a fixed, embedded Python worker over private pipes for CPU-only ONNX inference. Python handles model execution, tokenization, masked mean pooling and L2 normalization; Go owns preprocessing, account checks, cosine ranking, decisions and SQLite. No LLM, HTTP inference server, external API or runtime download is used.

Install Python 3.12 and run from the repository root:

```sh
python3.12 -m venv .local/minilm-py312
.local/minilm-py312/bin/python -m pip install -r backend/internal/embedding/minilm/requirements.txt
.local/minilm-py312/bin/python scripts/download-minilm.py .local/models/all-MiniLM-L6-v2
```

The explicit download fetches two public artifacts from the pinned upstream revision `10dbd2f06a8baf40ad285b037edda610c1b9a57c`: `onnx/model.onnx` (90,405,214 bytes) and `tokenizer.json` (466,247 bytes). SHA-256 checksums and expected sizes are checked before installation. The provider verifies both checksums again at construction. No mail is sent during setup. Models and virtual environments stay under ignored `.local/`; do not commit them. Installing another model requires another isolated adapter, not changes to classifier code.

Configure the native application with absolute, non-secret paths, then launch it:

```sh
export MAILPILOT_EMBEDDING_PYTHON="$PWD/.local/minilm-py312/bin/python"
export MAILPILOT_EMBEDDING_MODEL_DIR="$PWD/.local/models/all-MiniLM-L6-v2"
npm run dev
```

The core also supports `--embedding-python` and `--embedding-model-dir`. Paths must be supplied together. This trusted developer configuration cannot be changed by JavaScript or email content. It selects a local interpreter, not an arbitrary worker script: worker code is compiled into Go. Incorrect/missing model artifacts fail initialization; inference failures show an unavailable/error row and do not replace saved assessments. Omit both paths to run without embeddings.

The worker accepts at most 32 texts of 16 KiB each. MiniLM truncates inputs to 256 wordpieces and returns 384-dimensional normalized vectors. ONNX uses at most two intra-operation threads and one inter-operation thread. Worker output is capped at 1 MiB; stderr is discarded, errors are fixed strings, and email text travels only over stdin. Each cache-miss batch starts a bounded worker and releases it afterwards; cached reviews never start it. This avoids a resident model process but incurs cold-start cost. Inference honors cancellation by terminating the worker. Classification review has a 30-second Go deadline and 32-second native response deadline; other commands retain their previous deadlines. The UI remains responsive, but the sequential core bridge may delay other requests during a review.

This development implementation depends on an explicitly configured Python environment. A portable interpreter/runtime is not yet bundled in public installers; platform packaging of that optional inference runtime remains release work. The normal desktop shell remains packaged and works with embeddings unconfigured.

## Caching and integrity

Email vectors reuse the existing cleaned-text hash cache. Category vectors use the normalized description hash, category/account IDs, immutable provider/model identity, dimension and preprocessing version. Repeated classification with unchanged inputs invokes no model. Changed category descriptions or enabled state purge their vectors transactionally; category deletion cascades its vectors. Category save rejects stale, disabled and cross-account snapshots. No database transaction spans inference.

Migration 9 adds `category_embeddings` and classification assessment metadata. Chosen categories remain under the existing composite foreign keys; ranked candidate IDs are validated against enabled same-account categories before save. Assessment fingerprints cover sender, subject, body/completeness, HTML fallback, labels and all category definitions. Changed inputs reject late saves and hide old results; model/policy versions also make old reviews stale. This fingerprint change intentionally requires re-reviewing previous rule assessments. Category definitions are not automatically rewritten for model performance.

Vectors and assessments are sensitive local mail data in SQLite, WAL and backups. They are not encrypted and deletion is not forensic erasure. Worker telemetry is disabled before imports and explicitly through ONNX Runtime; local-file tokenizer loading never calls Hub download APIs. The subprocess receives a minimal environment without parent OAuth credentials or AI keys.

## Dependencies and verification

The optional [locked Python requirements](../backend/internal/embedding/minilm/requirements.txt) pin all resolved dependencies. No Go, frontend or Cargo dependency was added. Primary runtime dependencies are ONNX Runtime 1.23.2 (MIT), Tokenizers 0.23.3 (Apache-2.0), and NumPy 2.5.4 (BSD-3-Clause plus bundled notices). The worker checks these exact runtime versions. ONNX 1.23.2 has CPU wheels for macOS 13 ARM64/x64 and common Linux/Windows targets; newer wheels require macOS 14, so this implementation uses the compatible version verified on the current macOS 13 machine. Other native targets remain unqualified.

Official sources consulted: [ONNX CPU installation](https://onnxruntime.ai/docs/get-started/with-python), [ONNX releases/security](https://github.com/microsoft/onnxruntime/releases), [ONNX security policy](https://github.com/microsoft/onnxruntime/security), [Tokenizers releases](https://github.com/huggingface/tokenizers/releases), [Tokenizers license](https://github.com/huggingface/tokenizers), [NumPy package metadata](https://pypi.org/project/numpy/), [MiniLM model card](https://huggingface.co/sentence-transformers/all-MiniLM-L6-v2), and [ONNX pooling guidance](https://github.com/huggingface/sentence-transformers/blob/main/docs/sentence_transformer/usage/efficiency.rst). The published Tokenizers BPE-builder advisory [CVE-2026-85670](https://github.com/advisories/GHSA-jrf5-f6jp-7vxc) was reviewed; this adapter accepts only the checksum-pinned WordPiece tokenizer, never supplied tokenizer files. Official PyPI advisory metadata was checked for all 24 pinned dependencies and reported no package advisories for those versions. An advisory check is not a claim of complete vulnerability coverage.

Unit tests use fake providers and require no Python/model downloads. To run the real local model smoke test and synthetic evaluation after setup:

```sh
cd backend
MAILPILOT_TEST_EMBEDDING_PYTHON="$PWD/../.local/minilm-py312/bin/python" \
MAILPILOT_TEST_EMBEDDING_MODEL_DIR="$PWD/../.local/models/all-MiniLM-L6-v2" \
  go test -count=1 -v ./internal/embedding/minilm ./internal/store/sqlite -run TestOptIn
```

See [the evaluation report](SEMANTIC_EVALUATION.md) for measured behavior and limitations. Local semantic search is implemented separately; see [search usage](SEMANTIC_SEARCH.md).
