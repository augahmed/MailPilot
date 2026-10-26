import assert from "node:assert/strict";
import { execFileSync } from "node:child_process";
import {
  mkdtempSync,
  rmSync,
  readFileSync,
  writeFileSync,
  existsSync,
  unlinkSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { personalFixture } from "./personal-fixture.mjs";
import { parseGoogleClient } from "./google-client.mjs";

if (process.platform !== "darwin" || process.arch !== "arm64")
  throw new Error(
    "Personal package qualification currently requires Apple Silicon macOS.",
  );
const root = dirname(dirname(fileURLToPath(import.meta.url)));
const resource = join(
  root,
  "backend/internal/config/personal-google-client.json",
);
assert.ok(
  !existsSync(resource),
  "Stop personal runs and remove any interrupted generated input before this check.",
);
const temp = mkdtempSync(join(tmpdir(), "mailpilot-personal-"));
const input = join(temp, "synthetic client.json");
const app = join(root, "src-tauri/target/release/bundle/macos/MailPilot.app");
const personalApp = join(root, ".local/personal/MailPilot.app");
assert.ok(
  !existsSync(personalApp),
  "Move your existing personal app before running synthetic package qualification.",
);
const raw = JSON.stringify(personalFixture);
const env = { ...process.env };
delete env.MAILPILOT_GOOGLE_CLIENT_ID;
delete env.MAILPILOT_GOOGLE_CLIENT_SECRET;
delete env.GOFLAGS;
const run = (command, args, extra = {}) =>
  execFileSync(command, args, {
    cwd: root,
    env,
    stdio: "inherit",
    timeout: 600000,
    ...extra,
  });
const node = (script, args = [], extra = {}) =>
  run(process.execPath, [join(root, "scripts", script), ...args], extra);
const signAndSmoke = (personal) => {
  const bundle = personal ? personalApp : app;
  run("/usr/bin/codesign", ["--force", "--deep", "--sign", "-", bundle]);
  run("/usr/bin/codesign", ["--verify", "--deep", "--strict", bundle]);
  node("smoke-embedding.mjs", [bundle]);
  node("smoke-desktop.mjs", [
    join(bundle, "Contents/MacOS/mailpilot"),
    ...(personal ? ["--personal"] : []),
  ]);
};
let owned = false;
try {
  writeFileSync(input, raw, { mode: 0o600 });
  node("personal.mjs", ["build", "--google-client-json", input, "--no-sign"]);
  assert.ok(
    !existsSync(resource),
    "Personal helper did not clean its build input.",
  );
  const personalBytes = readFileSync(
    join(personalApp, "Contents/MacOS/mailpilot-core"),
  );
  for (const value of [
    personalFixture.installed.client_id,
    personalFixture.installed.client_secret,
  ])
    assert.ok(
      personalBytes.includes(Buffer.from(value)),
      "Personal executable omitted intended client configuration.",
    );
  assert.ok(
    !personalBytes.includes(Buffer.from(personalFixture.installed.project_id)),
    "Personal executable included discarded metadata.",
  );
  const frontend = join(root, "frontend/dist/assets");
  // Also qualify the frontend bundle: no client ID/secret may enter JavaScript.
  const { readdirSync } = await import("node:fs");
  for (const name of readdirSync(frontend)) {
    const contents = readFileSync(join(frontend, name));
    for (const value of [
      personalFixture.installed.client_id,
      personalFixture.installed.client_secret,
    ])
      assert.ok(
        !contents.includes(Buffer.from(value)),
        "Frontend contained desktop client configuration.",
      );
  }
  writeFileSync(resource, JSON.stringify(parseGoogleClient(Buffer.from(raw))), {
    flag: "wx",
    mode: 0o600,
  });
  owned = true;
  run("go", ["test", "-tags=personal_google", "./internal/config"], {
    cwd: join(root, "backend"),
  });
  signAndSmoke(true);
  // Deliberately leave the generated input and an inherited personal GOFLAGS
  // tag in place. The ordinary build must still exclude the personal default.
  node("tauri.mjs", ["build", "--bundles", "app", "--no-sign"], {
    env: { ...env, GOFLAGS: "-tags=personal_google" },
  });
  const standardBytes = readFileSync(
    join(app, "Contents/MacOS/mailpilot-core"),
  );
  for (const value of [
    personalFixture.installed.client_id,
    personalFixture.installed.client_secret,
  ])
    assert.ok(
      !standardBytes.includes(Buffer.from(value)),
      "Standard executable inherited personal client configuration.",
    );
  signAndSmoke(false);
  console.log(
    "Personal client defaults, clean-environment launch, frontend exclusion, signed offline resources and clean standard rebuild passed using synthetic configuration.",
  );
} finally {
  if (owned) unlinkSync(resource);
  rmSync(temp, { recursive: true, force: true });
  rmSync(personalApp, { recursive: true, force: true });
}
