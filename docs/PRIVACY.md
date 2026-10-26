# Current privacy and local storage

Reviewed 2026-10-26. This describes implemented behavior and its limits; it is
not a claim of Google verification, database encryption or forensic erasure.
The application exposes the same disclosures before Google sign-in under
**Privacy and local data**, and in the signed-in account menu.

## Local data and protection

One Go-owned SQLite database stores original cached mail and metadata, derived
chunks/vectors/search indexes, categories, settings and recovery evidence. The
reader does not load remote images or execute attachments. Local indexing,
classification and search do not upload email or request external embeddings.

On macOS/Linux, startup restricts the dedicated application directory to `0700`
and the database and existing SQLite WAL, SHM and rollback journal to `0600`.
Before opening SQLite, all four fixed files are checked for symlinks and
nonregular files; macOS/Linux also reject hardlinks. Validation and permission
repair use an opened directory root and file handles. New SQLite WAL/SHM files
inherit the private database mode. These controls do not protect against an
administrator, another application running as the same OS user, or malicious
replacement by that user after initialization. Windows ACL protection and native
behavior still require platform qualification; Unix modes are not a Windows ACL
guarantee.

Each physical SQLite connection enables `temp_store=MEMORY` to keep temporary
tables and query/sort material in memory, plus `secure_delete=ON` to reduce
ordinary deleted-row remnants. WAL, SHM and transaction journals still use disk.
These settings preserve foreign keys, WAL and FULL synchronization. They do not
retroactively erase existing free pages, FTS shadow data, old journals, snapshots,
backups or OS swap, and are not secure deletion or encryption.

The mail cache is **not encrypted by MailPilot**, including derived data and
SQLite sidecars. Device full-disk encryption, a screen lock and private OS-account
access help protect it. Uninstalling the application may retain its data. Never
publish app-data directories, databases, backups or mailbox-bearing logs.
See [the README](../README.md#configuration-and-local-storage) for default paths.

## Credentials and optional external processing

Google OAuth runs in the system browser with PKCE/state and a temporary local
callback. Refresh tokens and optional provider API keys use provider/profile-scoped
OS credential storage with no file/SQL/environment fallback. Access tokens remain
in backend memory. Developer desktop OAuth client configuration is separate from
user credentials.

Gmail sync and explicitly confirmed label operations contact Google. Selected-mail
Summary and email questions optionally contact the selected external AI provider
only after their separate exact-request approval. The shipped functional provider
is OpenAI. Provider charges and retention policies apply; `store:false` is not a
zero-retention promise. Cancellation cannot recall already submitted content.
The app adds no background external AI, default telemetry, or persistent answer
history. See [AI consent](GENERATIVE_AI.md) and [email questions](EMAIL_QUESTIONS.md).

## Disconnect and key removal

Disconnect removes the Gmail connection's OS-stored credential, clears session
access and attempts Google revocation. Failures remain visible. It retains cached
mail, indexes, categories and immutable recovery evidence, and it does not remove
AI provider keys. Disconnect, local data removal and Gmail deletion are distinct.

Before disconnecting, use **Optional AI settings → Disable AI and remove selected
provider key** to remove the saved provider's key. If OS-store deletion fails, AI
remains disabled and removal can be retried after unlocking the credential store;
the UI allows removal even while AI is disabled. The action targets the saved
provider, not an unsaved selection, and preserves other providers' keys.

There is no in-app cache-removal control. Application-level encrypted storage and
a separately designed removal workflow remain release work. Preserve historical
recovery evidence and do not equate these disclosures with compliance approval.

Technical references: [SQLite pragmas](https://www.sqlite.org/pragma.html),
[SQLite temporary files](https://www.sqlite.org/tempfiles.html), and
[Google Workspace data policy](https://developers.google.com/workspace/workspace-api-user-data-developer-policy).
