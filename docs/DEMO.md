# Credential-free synthetic demo

The demo uses 14 fictional messages and MailPilot's existing local database,
reader, chunking, retrieval and classification services. No Google client JSON,
Gmail account or OpenAI key is needed. A **Synthetic demo** banner stays visible
on every page. All senders use reserved `example.test` addresses.

## Run from source

Install the [normal prerequisites](../README.md#prerequisites), run `npm ci` once,
then launch from the repository root:

```sh
npm run dev:demo
```

Keep the terminal open; Ctrl+C stops development. The Vite server uses loopback.
The first developer build may need network for dependencies and fixed model
preparation. The installed demo has no runtime network requirement or model
download. It uses the same pinned MiniLM resources as the ordinary application.

## Build a demo application

On the qualified Apple Silicon macOS target:

```sh
npm run build:demo
codesign --force --deep --sign - "src-tauri/target/release/bundle/macos/MailPilot Demo.app"
open "src-tauri/target/release/bundle/macos/MailPilot Demo.app"
```

The separate **MailPilot Demo.app** identifier selects demo mode on Finder launch,
without flags, environment exports or credentials. It can coexist with ordinary
MailPilot. Quit an existing demo before opening a rebuilt copy. Ad-hoc signing is
for local testing; Developer ID signing, notarization and public distribution
remain separate work.

An ordinary bundle also accepts `--demo` through trusted native startup, after
quitting any running ordinary copy: `open -n MailPilot.app --args --demo`.
JavaScript has no mode/profile/path selection command.

## Walkthrough and reproducible screen states

1. **Your Emails:** inspect the two **FICTIONAL PHISHING DEMO** messages,
   “URGENT: account locked” and “Final notice”. Both score **HIGH, 92/100** under
   the current default engine: misleading URL (+25), IP destination (+15),
   credential language (+15), suspicious URL structure (+12), HTML form (+10),
   urgency (+8) and account-security language (+7). Authentication is unverified
   and contributes zero. Scores are computed from original synthetic plain/HTML
   mail, never injected. Only reserved documentation IPs/example domains appear;
   the escaped reader opens no link or form. These heuristics do not prove phishing.
   New IDs appear automatically after rebuilding/restarting an older demo;
   existing message edits and journals are preserved. Scroll below the feature
   cards and open the application acknowledgment, receipt or forwarded
   workshop message. The reader displays escaped text. Fictional tracking-image
   text loads nothing; external AI Summary is explicitly disabled.
2. **Email Search:** choose Keyword and search `MP-1042`, `Atlas` or `flight`.
   Results come from the shared SQLite passage index, without model inference.
   Open an excerpt to see the original cached sample.
3. **Categories & Classification:** inspect/edit the existing starter categories
   or run local classification. Before compatible vectors exist, the existing
   rule/abstention fallback applies. Suggestions/corrections are local decisions.
4. **Local Email Index:** choose Start indexing explicitly. The packaged MiniLM
   CPU provider computes actual vectors, category profiles and assessments.
   Startup starts no inference. Partial/unavailable states remain visible.
   Before an explicit indexing run, Hybrid uses keywords and Semantic reports
   unavailable; a query cannot silently initialize the model in demo mode.
5. **Email Search:** after indexing, choose Hybrid and try `flight reservation`
   or `project review`. Scores/timings are measured by the existing services, not
   supplied by fixtures. Hybrid keeps the normal keyword fallback when needed.
6. **Gmail Organization:** after classification, select an eligible sample such
   as the application acknowledgment and preview its label proposal. Sources are
   synthetic cached snapshots. Apply/remote recovery are disabled in the UI and
   rejected in Go. Preview journals are real local records, not Gmail changes.
7. Open **Privacy and local data** in the account menu. Ask/AI settings explain
   that external services are disabled; there is no API-key entry or paid call.

For screenshots, use only MailPilot Demo and retain the banner. Capture the app
window rather than the desktop. Reproduce dashboard/reader, keyword or hybrid search and
privacy states using the steps above; record whether indexing actually finished.
Do not substitute generated UI, invented scores or real mailbox screenshots.
See the [committed screenshots and capture instructions](assets/README.md).

## Isolation and sample restoration

The fixed macOS profile is
`~/Library/Application Support/com.mailpilot.desktop.demo-v1`. Other platforms
derive the same fixed suffix from the ordinary OS app-data root; their native
packaging is unqualified. Demo mode ignores `MAILPILOT_DATA_DIR` and refuses an
explicit `--data-dir`. It constructs no Google, credential-store, sync, external-AI
or historical remote-recovery adapters. A synthetic session authorizes only the
fixture account. Normal mode refuses marked demo profiles and demo-account
databases, including accidentally copied profiles.

Restarting preserves edits and does not duplicate samples. To restore bundled
sample messages, quit the demo first, then run:

```sh
npm run demo:reset
```

For the existing packaged demo, quit it and run:

```sh
open -n "src-tauri/target/release/bundle/macos/MailPilot Demo.app" --args --reset-demo
```

Restoration targets only known sample messages in the fixed marked profile.
Category edits, compatible indexes and immutable preview/recovery evidence are
retained. It does not wipe a database, remove keys, contact Gmail or accept a
caller-selected directory. No encryption or forensic-erasure claim is made.

## Qualification

Go tests exercise actual composition with a failing credential-store mock and a
transport that panics on network use: startup, restarts, restoration, keyword
search without model resources, local label previews, rejected external actions
and foreign-account reads. Profile tests reject unmarked directories, aliases
and incompatible account metadata. Frontend tests preserve the banner and gate
external controls.

After building/signing, run `npm run test:demo:native` on Apple Silicon macOS. It
denies all backend network access and ordinary app-data/keychain paths, then checks
real MiniLM indexing/hybrid retrieval and native demo presentation. Only the fixed
synthetic profile is used. It does not restore existing edits automatically; use
the sample-restore command first when necessary.
