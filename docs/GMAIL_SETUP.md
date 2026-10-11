# Gmail sign-in for local development

MailPilot uses Google’s **Desktop app** authorization-code flow with PKCE and a temporary loopback callback. Sign-in takes place in your system browser. Never enter a Gmail password into MailPilot or paste tokens into configuration.

Connecting verifies authorization and retrieves `users/me/profile` to identify the account. Selecting **Sync inbox** then explicitly downloads a bounded Inbox snapshot (default 500 messages; configure `MAILPILOT_SYNC_LIMIT=1..10000`). It does not apply labels, create drafts, or send mail. See the README for cache and synchronization limitations.

## Create a Google OAuth client

1. Open the [Google Cloud Console](https://console.cloud.google.com/) and select or create a development project.
2. In **APIs & Services → Library**, find **Gmail API** and enable it for this project.
3. Open **Google Auth Platform** (or the OAuth consent-screen section). Configure the app branding, user support email, and developer contact information. Choose an appropriate audience. For personal development, use **External**, keep the app in **Testing**, and add your dedicated Gmail test account under **Test users**. Workspace Internal apps require an eligible organization.
4. In **Data Access**, add `https://www.googleapis.com/auth/gmail.modify`. This is the narrowest Gmail scope permitting both email-body reads and applying/removing labels on messages. `gmail.labels` only manages label definitions. Google bundles sending and other modifications into `gmail.modify`; MailPilot exposes no sending/deletion operation. Do not add `https://mail.google.com/`, Contacts, Drive, or unrelated scopes.
5. Under **Clients → Create client**, select **Desktop app**, give it a development name, and create it. Do not choose Web application or configure a public callback. Google’s Desktop client supports an ephemeral `http://127.0.0.1:<port>/oauth/callback`; MailPilot selects the port at runtime.
6. Copy the client ID and desktop client secret into your local runtime configuration. If downloading the JSON, keep it outside the repository. MailPilot does not automatically load this JSON or `.env` files. The client ID/secret identify your OAuth application; they are not a Gmail password or a user refresh token. A distributed desktop client cannot keep a client secret confidential; PKCE protects the authorization-code exchange.

See Google’s [desktop OAuth flow](https://developers.google.com/identity/protocols/oauth2/native-app), [Gmail scope definitions](https://developers.google.com/workspace/gmail/api/auth/scopes), and [Gmail profile endpoint](https://developers.google.com/workspace/gmail/api/reference/rest/v1/users/getProfile). Restricted-scope verification and any applicable assessment must be resolved before public distribution. Testing-mode authorizations using Gmail scopes normally expire after seven days; reconnect when prompted. See [Google’s token expiration guidance](https://developers.google.com/identity/protocols/oauth2#expiration).

## Run with local configuration

From the repository root, in a terminal with Go, Node and Rust available:

```sh
export MAILPILOT_GOOGLE_CLIENT_ID='YOUR_CLIENT_ID.apps.googleusercontent.com'
# zsh (the macOS default): prompt silently, avoiding the value in shell history.
read -rs 'MAILPILOT_GOOGLE_CLIENT_SECRET?Google desktop client secret: '
export MAILPILOT_GOOGLE_CLIENT_SECRET
npm run dev
```

In Bash, replace the `read` command with:

```sh
read -rsp 'Google desktop client secret: ' MAILPILOT_GOOGLE_CLIENT_SECRET
```

The native host passes runtime environment configuration to the Go sidecar. No `VITE_` configuration, client secret, access token, or refresh token belongs in frontend code. Changing configuration requires restarting the app. With no client ID, the local application still works and shows **Gmail setup required**.

For a packaged macOS app, use the same terminal environment and launch its executable directly:

```sh
src-tauri/target/release/bundle/macos/MailPilot.app/Contents/MacOS/mailpilot
```

Finder launches do not inherit your terminal’s environment. Client configuration is not embedded in the bundle. After development, unset it from the shell:

```sh
unset MAILPILOT_GOOGLE_CLIENT_ID MAILPILOT_GOOGLE_CLIENT_SECRET
```

`.env*`, downloaded `client_secret*.json`, `credentials.json`, token files, and OAuth-client JSON patterns are ignored by Git. Inspect staged changes before committing; arbitrary filenames are not automatically safe. Never configure access or refresh tokens through environment variables.

## Connect and disconnect

- Click **Connect Gmail**, sign in to Google in your browser, select your dedicated test account, and review consent. The session expires after three minutes. The callback browser page only confirms receipt; MailPilot reports the final outcome.
- **Gmail connected** and the address appear only after successful OAuth, profile verification, account persistence, and secure refresh-token storage.
- Access tokens remain in Go memory. Refresh tokens use macOS Keychain, Windows Credential Manager, or Linux Secret Service through the `TokenStore` adapter. The keyring namespace includes the OAuth client ID and local data directory. Use the same values after restarting to restore that connection. No plaintext development fallback exists; the in-memory store is test-only.
- On restart or **Check connection**, MailPilot refreshes authorization and verifies the profile. While open, an access token nearing expiry triggers one serialized refresh through status polling. An omitted replacement refresh token preserves the existing one; rotations are persisted. Network failure does not erase stored authorization or claim connectivity. Use **Check connection** after restoring network access.
- **Disconnect** cancels pending work, clears memory, removes the local keyring credential, clears its SQLite reference, and attempts Google revocation. It never deletes cached email or remote mail. If local deletion fails, the app reports an error rather than claiming disconnection. If Google revocation fails offline, the app reports local disconnection with an explicit warning; remove the grant at [Google Account connections](https://myaccount.google.com/connections). Revocation can affect other grants associated with the same OAuth project.

The OS store may show its own access prompt. Unlock/authorize the store before retrying. Linux needs a working Secret Service session and an unlocked `login` collection. Native keyring calls are serialized but the underlying library cannot cancel an OS prompt; pending disconnect waits for that call to return. A process exit still terminates the local worker. Windows/Linux packaging and native behavior need platform qualification.

Changing a Workspace email address currently requires reconnecting; profile email is the Gmail account identity available through the selected endpoint. Moving the local data directory or changing client ID creates a different keyring slot: disconnect before doing so to avoid leaving an old credential behind. Local cache removal and fully durable disconnect recovery across process crashes are later work. If the app is terminated between credential deletion and revocation, remove the grant manually in Google Account connections.

## Troubleshooting

- **Setup required / invalid client:** verify the Desktop client ID, client secret, project, Gmail API enablement, and launch environment. Never print the whole environment or credentials to troubleshoot.
- **Access blocked:** confirm the account is a test user, inspect project consent configuration, and check Workspace administrator restrictions. Do not bypass Google warnings for an unknown project.
- **Consent denied:** no connection is created; retry only if you want to grant access.
- **Permission missing:** reconnect and review Gmail consent. Partial/insufficient grants do not count as connected.
- **Sign-in timeout:** retry; the previous listener/state/verifier are discarded. Firewalls must permit the local loopback callback.
- **Reconnection required:** Google rejected the refresh token (expired/revoked/testing limits); connect again.
- **Credential store unavailable:** unlock the OS store and allow access. MailPilot fails closed instead of writing tokens to a file or SQLite.

## Verification boundaries

Normal tests use synthetic HTTP responses and in-memory credentials; they never access a personal mailbox, launch Google consent, or touch the real keyring. The packaged desktop smoke test explicitly disables OAuth configuration.

An optional OS-keyring smoke test writes, reads, and deletes **one synthetic credential** under a unique temporary-profile namespace:

```sh
cd backend
go test -tags keyring_integration ./internal/credentials -run '^TestNativeKeyring$' -count=1
```

Run it alone: other credential tests intentionally replace the keyring backend with an in-memory fake. A successful automated test does not prove real Google authorization. To check that, use your own configured Desktop client and dedicated test account through the app, test denial and consent, restart to test refresh, then disconnect. Do not commit credentials or authentication screenshots containing private data.
