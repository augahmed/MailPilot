# Gmail setup and personal macOS builds

MailPilot uses Google’s **Desktop app** authorization-code flow with PKCE and a temporary loopback callback. Sign-in takes place in your system browser. Never enter a Gmail password into MailPilot or paste tokens into configuration.

Connecting verifies authorization and retrieves `users/me/profile` to identify the account. Selecting **Sync inbox** then explicitly downloads a bounded Inbox snapshot (default 500 messages; configure `MAILPILOT_SYNC_LIMIT=1..10000`). It does not apply labels, create drafts, or send mail. See the README for cache and synchronization limitations.

## Create a Google OAuth client

1. Open the [Google Cloud Console](https://console.cloud.google.com/) and select or create a development project.
2. In **APIs & Services → Library**, find **Gmail API** and enable it for this project.
3. Open **Google Auth Platform** (or the OAuth consent-screen section). Configure the app branding, user support email, and developer contact information. Choose an appropriate audience. For personal development, use **External**, keep the app in **Testing**, and add your dedicated Gmail test account under **Test users**. Workspace Internal apps require an eligible organization.
4. In **Data Access**, add `https://www.googleapis.com/auth/gmail.modify`. This is the narrowest Gmail scope permitting both email-body reads and applying/removing labels on messages. `gmail.labels` only manages label definitions. Google bundles sending and other modifications into `gmail.modify`; MailPilot exposes no sending/deletion operation. Do not add `https://mail.google.com/`, Contacts, Drive, or unrelated scopes.
5. Under **Clients → Create client**, select **Desktop app**, give it a development name, and create it. Do not choose Web application or configure a public callback. Google’s Desktop client supports an ephemeral `http://127.0.0.1:<port>/oauth/callback`; MailPilot selects the port at runtime.
6. Download the client JSON and keep it **outside this repository**, for example at `/Users/YOUR_NAME/Private/MailPilot/google-desktop-client.json`. The file must contain an `installed` Desktop app client with `client_id` and `client_secret`. Keep it separate from token files and OpenAI keys. Do not paste its contents into source, a shell command, a screenshot or an issue.

See Google’s [desktop OAuth flow](https://developers.google.com/identity/protocols/oauth2/native-app), [Gmail scope definitions](https://developers.google.com/workspace/gmail/api/auth/scopes), and [Gmail profile endpoint](https://developers.google.com/workspace/gmail/api/reference/rest/v1/users/getProfile). Restricted-scope verification and any applicable assessment must be resolved before public distribution. Testing-mode authorizations using Gmail scopes normally expire after seven days; reconnect when prompted. See [Google’s token expiration guidance](https://developers.google.com/identity/protocols/oauth2#expiration).

## Run development with your downloaded JSON

Install the [README prerequisites](../README.md#prerequisites) and run `npm ci` once.
From the repository root:

```sh
npm run dev:personal -- --google-client-json "/Users/YOUR_NAME/Private/MailPilot/google-desktop-client.json"
```

The helper validates the file, compiles the Go sidecar with only the desktop
client ID/secret, and starts the existing Tauri/Vite development workflow. No two
manual environment exports are needed. Connect Gmail in the system browser using
your configured test account. Stop development with Ctrl+C before another build.
The original downloaded file is never modified. The helper prints neither its
contents nor client values and does not put them into frontend/VITE variables.

The input must be an absolute path outside the repository, a regular UTF-8 JSON
file no larger than **32 KiB**. Web/service-account credentials, token/API-key
records, unexpected fields, missing required fields and conflicting endpoint
metadata are rejected. The helper discards project metadata and downloaded URLs.
Go still selects its fixed Google endpoints, Gmail scope and runtime loopback
callback; JSON cannot replace those destinations or PKCE/state behavior.

## Build a personal application for Finder

The packaged personal workflow currently targets **Apple Silicon macOS**. From
the repository root:

```sh
npm run build:personal -- --google-client-json "/Users/YOUR_NAME/Private/MailPilot/google-desktop-client.json"
codesign --force --deep --sign - .local/personal/MailPilot.app
codesign --verify --deep --strict .local/personal/MailPilot.app
open .local/personal/MailPilot.app
```

The helper uses the existing build pipeline and fixed verified MiniLM resources.
It moves the completed bundle to **`.local/personal/MailPilot.app`**, outside the
ordinary release bundle path. The containing personal directory is private and
ignored by Git. Build one desktop app at a time; a new personal build replaces
that personal output. You can open it from Finder without a terminal or Google
environment exports. You may copy the whole app into your own Applications folder;
installed use needs no Go, Node, Rust or model download. Keep the whole bundle
intact. Moving the bundle does not change its signature.
Quit any running MailPilot copy before opening a rebuilt app; the single-instance
host otherwise focuses the existing process.

The signing commands above create an **ad-hoc local developer signature**.
Developer ID signing/notarization and public OAuth verification remain unfinished.
Configuration is compiled before signing; the workflow never injects configuration
into an already signed app. `--no-sign` is available on `build:personal` for
synthetic/local checks before applying the ad-hoc signature yourself.

Desktop OAuth client IDs and desktop `client_secret` values are **extractable
public-client configuration**, not user credentials. PKCE, browser consent and
OS-stored user tokens remain essential. The personal app is for its owner; do not
upload it to GitHub Releases or advertise it as a verified consumer download.
It is not a verification bypass. Never package access/refresh tokens, a Gmail
password, an OpenAI key or signing credentials. There is no new production API key
issued by this helper.

## Configuration precedence and standard builds

- Personal helpers use the explicit JSON for their child processes and remove
  inherited Google client environment overrides. They generate a temporary,
  ignored, mode-`0600` `backend/internal/config/personal-google-client.json`
  containing only the two desktop client fields. The original JSON stays outside
  the repository. The `personal_google` build tag includes this default in Go;
  runtime Go validation rejects a malformed packaged default.
- A directly launched app/core accepts the existing **pair** of
  `MAILPILOT_GOOGLE_CLIENT_ID` and `MAILPILOT_GOOGLE_CLIENT_SECRET` as an override.
  A partial pair fails configuration instead of mixing clients. Setting both
  explicitly empty disables OAuth for isolated smoke checks. With neither set,
  a personal app uses its compiled default; a standard app shows setup required.
- `npm run dev`, `npm run build` and `npm run build:sidecar` explicitly exclude the
  personal tag, even if inherited `GOFLAGS` contains it or a stale generated input
  remains on disk. Standard bundles contain no personal default. Runtime manual
  overrides remain available. `.env` files are not automatically loaded.
- Normal exit, build failure and handled interruption remove the generated input.
  An abrupt kill can leave it behind. After stopping every personal run, remove
  only that ignored generated file before retrying. Compiled personal executables
  and the Go build cache can retain the extractable client configuration; cleanup
  is not forensic erasure. Check staged files before committing.

The existing keyring namespace still uses the same client ID and data directory;
changing the JSON file's location or rebuilding with the same client preserves
that namespace. Changing the client ID or data directory isolates the credential
slot. Follow the disconnect guidance before intentionally changing either.

## Optional OpenAI setup

Open **Optional AI settings**, select OpenAI and a supported model ID, and submit
your own OpenAI API key there. MailPilot stores it only in the OS credential store
and clears the submitted input. No key belongs in Google JSON, environment exports,
build inputs, source, SQLite or screenshots. Saving settings and launching the
app make no paid AI validation request. Ordinary local features need no OpenAI
account/key; Summary and evidence answers each require a separate exact-payload
approval. See [AI consent and settings](GENERATIVE_AI.md) and
[privacy/key removal](PRIVACY.md).

## Connect and disconnect

- Click **Connect Gmail**, sign in to Google in your browser, select your dedicated test account, and review consent. The session expires after three minutes. The callback browser page only confirms receipt; MailPilot reports the final outcome.
- **Gmail connected** and the address appear only after successful OAuth, profile verification, account persistence, and secure refresh-token storage.
- Access tokens remain in Go memory. Refresh tokens use macOS Keychain, Windows Credential Manager, or Linux Secret Service through the `TokenStore` adapter. The keyring namespace includes the OAuth client ID and local data directory. Use the same values after restarting to restore that connection. No plaintext development fallback exists; the in-memory store is test-only.
- On restart or **Check connection**, MailPilot refreshes authorization and verifies the profile. While open, an access token nearing expiry triggers one serialized refresh through status polling. An omitted replacement refresh token preserves the existing one; rotations are persisted. Network failure does not erase stored authorization or claim connectivity. Use **Check connection** after restoring network access.
- **Disconnect** cancels pending work, clears memory, removes the local keyring credential, clears its SQLite reference, and attempts Google revocation. It never deletes cached email or remote mail. If local deletion fails, the app reports an error rather than claiming disconnection. If Google revocation fails offline, the app reports local disconnection with an explicit warning; remove the grant at [Google Account connections](https://myaccount.google.com/connections). Revocation can affect other grants associated with the same OAuth project.

The OS store may show its own access prompt. Unlock/authorize the store before retrying. Linux needs a working Secret Service session and an unlocked `login` collection. Native keyring calls are serialized but the underlying library cannot cancel an OS prompt; pending disconnect waits for that call to return. A process exit still terminates the local worker. Windows/Linux packaging and native behavior need platform qualification.

Changing a Workspace email address currently requires reconnecting; profile email is the Gmail account identity available through the selected endpoint. Moving the local data directory or changing client ID creates a different keyring slot: disconnect before doing so to avoid leaving an old credential behind. Local cache removal and fully durable disconnect recovery across process crashes are later work. If the app is terminated between credential deletion and revocation, remove the grant manually in Google Account connections.

## Troubleshooting

- **Setup required / invalid client:** verify the Desktop client ID, client secret, project, Gmail API enablement, and downloaded JSON/personal build (or complete runtime override pair). Never print the whole environment or credentials to troubleshoot.
- **Access blocked:** confirm the account is a test user, inspect project consent configuration, and check Workspace administrator restrictions. Do not bypass Google warnings for an unknown project.
- **Consent denied:** no connection is created; retry only if you want to grant access.
- **Permission missing:** reconnect and review Gmail consent. Partial/insufficient grants do not count as connected.
- **Sign-in timeout:** retry; the previous listener/state/verifier are discarded. Firewalls must permit the local loopback callback.
- **Reconnection required:** Google rejected the refresh token (expired/revoked/testing limits); connect again.
- **Credential store unavailable:** unlock the OS store and allow access. MailPilot fails closed instead of writing tokens to a file or SQLite.

## Verification boundaries

Normal tests use synthetic HTTP responses and in-memory credentials; they never access a personal mailbox, launch Google consent, or touch the real keyring. The ordinary packaged desktop smoke test explicitly disables OAuth configuration.
`npm run test:personal` exercises bounded JSON import and safe failures.
`npm run test:personal:package` builds with synthetic client canaries, checks Go's
packaged default and clean-environment native launch, verifies signed/offline model
resources, checks frontend exclusion, then rebuilds a standard app with stale
personal input and an inherited tag to verify exclusion. Its personal smoke
accepts only the fixed synthetic client and a fresh temporary profile: it may read
that nonexistent OS-keyring slot, but never signs into Google or loads a live
account. It leaves a standard locally signed app and removes its synthetic personal
bundle. Move any existing personal output aside before this qualification command;
it deliberately refuses to replace it. Native tests require Apple Silicon macOS
and local GUI/keyring access.

An optional OS-keyring smoke test writes, reads, and deletes **one synthetic credential** under a unique temporary-profile namespace:

```sh
cd backend
go test -tags keyring_integration ./internal/credentials -run '^TestNativeKeyring$' -count=1
```

Run it alone: other credential tests intentionally replace the keyring backend with an in-memory fake. A successful automated test does not prove real Google authorization. To check that, use your own configured Desktop client and dedicated test account through the app, test denial and consent, restart to test refresh, then disconnect. Do not commit credentials or authentication screenshots containing private data.
