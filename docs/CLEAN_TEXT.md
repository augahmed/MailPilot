# Shared readable email text

The cached reader and local chunker now use the same deterministic text projection:

```text
Original cached text/HTML (preserved)
  → bounded visible-text extraction and artifact cleanup
  → authored/quoted/forwarded/signature sections
  → reader text or token-aware chunks
  → explicitly started local inference and SQLite vectors
```

`internal/mail/readable.go` owns artifact cleanup and `ReadableBody`;
`mail.Sections` owns source selection, section metadata and cleaned projection
mapping. `Chunker.Build` already consumes these sections, and `app.LocalIndex`
embeds only canonical `mail.ModelInput` strings from the persisted current chunks.
There is no frontend-only cleanup or second embedding/preprocessing pipeline.

## Cleanup behavior

- Standalone `[image: …]` transport placeholders become readable image descriptions.
  Keep offer dates, discount amounts, codes and brand names; omit empty/spacer/pixel
  descriptions and decorative logo suffixes. HTML `img` alt text supplies meaningful
  image-only information without loading any image. Literal inline syntax such as
  `Use [image: icon] in your template` remains authored text.
- Remove nearby duplicate image-origin descriptions, including descriptions already
  present as visible text. Preserve repeated authored prose and separate quote/
  forward/signature sections; this is local structural deduplication, not semantic
  rewriting or removal of an entire quoted/forwarded chain.
- Omit standalone opaque tracking redirects on recognizable tracking subdomains
  when readable text accompanies them. Inline redirects retain a compact destination;
  URL-only mail retains a destination rather than becoming empty. Ordinary URLs
  keep meaningful paths/query identifiers/fragments, with known analytics parameters
  such as `utm_*`, `gclid` and `fbclid` removed. No redirects are followed or decoded
  into new network requests. This is a conservative heuristic, not a URL reputation
  or phishing verdict; unfamiliar links remain literal text.
- Recognize multiline generated `[Blog URL]`, `[GitHub URL]` and similar link
  annotations when comparing MIME alternatives. Their visible labels match HTML
  anchor labels, so brackets and wrapping cannot cause the entire body to be
  appended again. Keep a compact destination within annotated tracking links
  during cleanup so matching remains possible after opaque payload removal.
- Remove runs of invisible preview padding (figure spaces with combining grapheme
  joiners, zero-width padding and discretionary soft hyphens). Remove discretionary
  soft hyphens within words so they remain searchable. Preserve significant joiners
  within words and emoji; never globally erase arbitrary Unicode formatting.
- Compact the specific `email`/tracking-host `/e/c/<long opaque payload>/<64-hex>`
  redirect pattern without decoding its payload or following it. Normal document
  URLs, reservation IDs and unmatched paths remain intact. The original full URLs
  stay in cached plain text/HTML for source inspection and later link extraction;
  opaque tracking payloads do not improve semantic matching and are excluded.
- Remove generated Gmail-local footnote link targets while retaining footnote text.
  Compact repeated blank lines and preserve paragraph structure and Unicode.
- HTML extraction retains anchor text, meaningful image descriptions and structural
  quote/signature hints. It omits script/style/head/template/iframe/object/noscript
  contents and explicitly hidden/aria-hidden or inline display:none,
  visibility:hidden and mso-hide:all containers. No CSS is evaluated or loaded.
- Prefer cleaned visible HTML as the primary reading/chunking representation when
  available. Compare plain-text paragraphs against that representation while ignoring
  transport-only emphasis, soft wrapping and generated HTTP/mailto annotations.
  Append only additional plain-text evidence, scoped by authored/quoted/forwarded/
  signature kind. Preserve intentional repetition within the primary copy and
  unique URL-only passages or identifiers; this is not global prose deduplication.
  When cached plain text matches the historical HTML
  fallback, prefer cleaned visible HTML so hidden preheaders do not reappear and
  useful image descriptions are retained. Genuine distinct plain alternatives keep
  their unique evidence. Originals, full URLs and headers remain cached for
  risk analysis, historical hashes and recovery. Existing rule/Summary cleanup and
  deterministic phishing logic are unchanged.

The result is escaped plain text, not executable HTML. The reader continues to
block active links, remote resources and attachments. Cleanup is bounded to the
existing body limits plus 32,768 source lines, 100,000 HTML tokens and 128 nesting
levels; context cancellation is checked throughout. Unsupported/oversized content
is reported rather than silently truncated. Image labels and structural markers
are untrusted heuristics, not proof of authorship or completeness.

## Versions, offsets and existing data

`TextProjectionVersion` is **readable-v4**. It is included in the chunk preprocessing
version (`email-sections-readable-v4-wp-07eced375cec-256-32-…`). Offsets address the
named **cleaned** `body_text` / `body_html` projection, not raw MIME or HTML bytes;
reprocessing an unchanged source with the same version reproduces those offsets.
The original cached message and its content hash are never overwritten.

The `readable-v4` update fixes bracketed-link alternative duplication and preview
padding. Reader cleanup takes effect after restarting the rebuilt app. Existing
`readable-v3` vectors are excluded; Resume indexing rebuilds them from the cache.
Opening a message performs no inference, reimport or original-source mutation.

No schema migration or model replacement is required. Model/tokenizer identity
stays unchanged; prior preprocessing generations are excluded from current index
coverage. The Local Email Index card reports `preprocessing_changed` and offers
Resume. Start/Resume/Rebuild regenerates compatible chunks and embeddings from the
existing cache without requiring a Gmail reimport. Subsequent unchanged runs reuse
those vectors. Inference does not start just because an email is opened or the app
is restarted. Historical vector tables and recovery journals remain intact.

Reader pagination uses an opaque SHA-256 of projection version plus original source
hash. Changing source or cleanup rules therefore invalidates old page cursors;
no caller may assume the reader revision equals the source hash. The wire remains
protocol 1 with the same reader shape and one additive index error code.

Regression tests cover synthetic newsletter clutter, image-only offers, HTML and
MIME alternatives, hidden content, normal URL identifiers, quoted/forwarded content,
Unicode, stable offsets, paging, original-source preservation and old-index refresh.
The offline native indexing qualification additionally verifies bracketed-link multipart newsletters and cleaned newsletter
inputs through real MiniLM inference, SQLite vector persistence and cached reading.
