# Security policy

MailPilot is a developer/portfolio desktop application. The currently qualified
target is Apple Silicon macOS; Windows/Linux and other architectures are not
security-qualified releases. There is no maintained release branch or promised
response deadline. Use a dedicated test Gmail account for demonstrations. Review
the [dated security findings](docs/EVALUATION.md#security-follow-up-2026-10-26)
before using real mail; unresolved reachable high-risk findings block a real-mail
release recommendation.

## Threat model and boundaries

Email, headers, HTML and model output are untrusted. MailPilot renders escaped
text, blocks remote mail content and does not execute attachments. React has only
typed, bounded desktop commands; Go owns account authorization, credentials,
SQLite, fixed network destinations and confirmation before Gmail label changes.
There is no general shell, SQL, file or network bridge exposed to the UI.

OAuth uses the system browser, PKCE/state and a bounded loopback callback. User
tokens and optional AI keys use OS credential storage and fail closed if it is
unavailable. They are not application configuration files. Logs/errors use safe
codes rather than credentials, email or raw provider errors. Local search,
indexing and classification use the packaged model without external embeddings.
Optional Summary and evidence answers transmit only the separately approved
bounded payload; approval is tied to the account/session, content, provider/model
and key identity. Output and citations can still be factually wrong.

## Known limits

- The SQLite cache, original mail, derived data and disk journals are **unencrypted
  by MailPilot**. Private Unix permissions reduce access by other OS users; they
  do not stop the same OS user, an administrator, malware or a compromised device.
  Windows ACL behavior remains unqualified.
- Disconnect retains cached mail, indexes, categories and recovery evidence.
  AI keys are separate and require the settings removal action. There is no
  in-app cache-removal control. Uninstalling can retain data.
- SQLite memory temporaries and `secure_delete` do not erase backups, snapshots,
  old free pages, WAL history, FTS remnants or swap. No forensic deletion claim is
  made. Protect the device and backups with appropriate OS controls.
- Intended Gmail traffic and explicitly approved external AI leave the device.
  Provider policies apply; cancellation cannot recall a dispatched request and
  `store:false` does not promise zero provider retention. No default telemetry or
  background external AI is added.
- Dependency scanners do not audit all native libraries or system frameworks.
  Developer ID signing, notarization and public OAuth verification remain
  unfinished. This policy is not an independent audit or compliance certification.

The [privacy disclosure](docs/PRIVACY.md) describes paths, retention and key
removal. [AI consent](docs/GENERATIVE_AI.md) and
[IPC contracts](contracts/README.md) describe the implementation boundaries.

## Reporting a suspected vulnerability

As checked on 2026-10-26, this repository has GitHub Issues enabled and private
vulnerability reporting disabled. To request a private contact route, open a
[new issue in augahmed/MailPilot](https://github.com/augahmed/MailPilot/issues/new)
titled **Security contact request**. Include only a request for a private channel;
wait for the maintainer to provide one before sharing sensitive details. This
initial issue is public. No private mailbox or response SLA is currently offered.

If GitHub private reporting is enabled later, use **Security → Report a
vulnerability** instead. Never post tokens, keys, real messages, databases,
screenshots of personal mail or exploit details in a public issue. Once a private
route is established, provide the affected revision/platform, a synthetic
reproduction, expected/actual behavior and likely impact. For exposed live
credentials, revoke them through the provider; deleting a Git file is insufficient.
