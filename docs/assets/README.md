# Visual asset sources and reproduction

The SVG diagrams are editable, source-controlled text. They have a fixed light
background, accessible title/description and no external resources, fonts,
scripts or raster dependencies. Edit the SVG directly and review its rendering
when a contract changes:

- [Runtime/trust boundaries](runtime-boundaries.svg) — React presentation,
  narrow Rust host, private pipes, Go authority, local stores and explicit
  Google/OpenAI boundaries.
- [Shared processing pipeline](shared-pipeline.svg) — preserved original mail,
  readable sections, chunks, FTS, explicit native vectors, shared retrieval,
  classification and approved external answers.

Ownership comes from [architecture](../ARCHITECTURE.md), storage/reuse from the
[vector design](../VECTOR_ARCHITECTURE_PLAN.md), and approval rules from the
[AI](../GENERATIVE_AI.md) / [evidence-answer](../EMAIL_QUESTIONS.md) contracts.
Diagrams summarize those contracts; they do not grant new capabilities.

## Genuine app captures

Captured **2026-10-27** from the actual native **MailPilot Demo.app** on Apple
Silicon macOS. Only the app window was captured. The `pilot@example.test` account
and every message are fictional; no Gmail account, credentials, provider console,
AI-generated UI or mocked screenshot was used. Screenshots show the running
application and its real local SQLite services. They are PNGs without added
annotations or substituted scores/text.

| File                                             | Reproduce in the demo                                                                                    |
| ------------------------------------------------ | -------------------------------------------------------------------------------------------------------- |
| [Dashboard](screenshots/demo-dashboard.png)      | Open the dashboard; show the synthetic banner and offline account                                        |
| [Phishing reader](screenshots/demo-phishing.png) | In Your Emails, open “URGENT: account locked — confirm your password”; expose the HIGH score and reasons |
| [Hybrid search](screenshots/demo-search.png)     | Email Search → Hybrid → `MP-1042` → Search locally; scroll to receipt/delivery excerpts                  |
| [Privacy](screenshots/demo-privacy.png)          | Account menu → Privacy and local data; show the cache/credentials/external-AI disclosures                |

Build/launch and sample restoration commands are in [DEMO.md](../DEMO.md).
A fresh demo has 14 samples. The two high-risk examples use reserved documentation
IP addresses and mismatched example-domain HTML anchors/forms. The ordinary
extractor/engine computes **92/100** for each under the current default policy;
there are no fixture scores. The reader renders escaped text and loads nothing.

This capture profile completed real, explicitly requested local indexing for all
14 messages during the network-denied native qualification before capture. Search
reported 14/14 keyword and semantic coverage with 34 chunks searched. Timings and
ranks are observations from the actual app, not a new benchmark. Keyword search
and phishing analysis do not require embeddings.
Category assignments in an existing profile may reflect retained local reviews.
Sample reset restores known messages but preserves category edits, compatible
vectors and immutable journals. UI layout can vary with window size and platform.

## Capture a replacement

1. Build and open only the separate **MailPilot Demo** bundle; verify the
   **Synthetic demo** banner and fictional account before capturing.
2. Restore the known samples if necessary, then navigate using the table above.
   For screenshots of semantic results, explicitly finish indexing and record
   actual coverage; do not imply startup performs inference.
3. Size the window to keep relevant controls and the synthetic banner visible.
   Use macOS Shift–Command–4, then Space, and select only the demo window, or
   `screencapture -x -l <DEMO_WINDOW_ID> <OUTPUT.png>` with its verified window ID.
   Screen Recording permission controls capture; Accessibility is needed only
   for optional automation of navigation.
4. Inspect every PNG for private mail, keys, OS-account details, consoles and
   unrelated windows. Keep only genuine synthetic content and descriptive alt
   text/captions in the README. Do not record Google consent or API-key entry.
5. Commit only the reviewed PNGs and these editable diagrams. Model weights,
   native libraries, recordings, databases and bulk build artifacts stay ignored.
