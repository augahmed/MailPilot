import assert from "node:assert/strict";
import { test } from "node:test";
import {
  mkdtempSync,
  rmSync,
  writeFileSync,
  mkdirSync,
  symlinkSync,
  cpSync,
  existsSync,
  readFileSync,
} from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { spawnSync } from "node:child_process";
import { parseGoogleClient, readGoogleClient } from "./google-client.mjs";

import { personalFixture as fixture } from "./personal-fixture.mjs";

const bytes = (value) => Buffer.from(JSON.stringify(value));

test("only the minimal desktop configuration survives import", () => {
  assert.deepEqual(parseGoogleClient(bytes(fixture)), {
    client_id: fixture.installed.client_id,
    client_secret: fixture.installed.client_secret,
  });
});

test("wrong clients, token/key records, malformed and oversized JSON fail without disclosure", () => {
  const canary = "PRIVATE-INPUT-CANARY";
  for (const value of [
    null,
    [],
    {},
    { web: fixture.installed },
    { type: "service_account", private_key: canary },
    { access_token: canary },
    { refresh_token: canary },
    { api_key: canary },
    { ...fixture, refresh_token: canary },
    { installed: null },
    { installed: { client_id: fixture.installed.client_id } },
    { installed: { client_secret: fixture.installed.client_secret } },
    { installed: { ...fixture.installed, api_key: canary } },
    { installed: { ...fixture.installed, client_secret: "sk-synthetic-key" } },
    { installed: { ...fixture.installed, client_id: "" } },
    { installed: { ...fixture.installed, client_secret: "" } },
  ])
    assert.throws(
      () => parseGoogleClient(bytes(value)),
      (error) => !error.message.includes(canary),
    );
  for (const raw of [
    Buffer.alloc(0),
    Buffer.alloc(32769, 32),
    Buffer.from(`{"${canary}`),
    Buffer.from([0xff]),
  ]) {
    assert.throws(
      () => parseGoogleClient(raw),
      (error) => !error.message.includes(canary),
    );
  }
});

test("downloaded endpoints and redirects cannot replace trusted OAuth destinations", () => {
  for (const [key, value] of [
    ["auth_uri", "https://attacker.test/auth"],
    ["token_uri", "https://attacker.test/token"],
    ["auth_provider_x509_cert_url", "https://attacker.test/certs"],
    ["redirect_uris", ["https://attacker.test/callback"]],
  ]) {
    assert.throws(() =>
      parseGoogleClient(
        bytes({ installed: { ...fixture.installed, [key]: value } }),
      ),
    );
  }
});

test("file import requires an absolute regular bounded file outside the repository", () => {
  const directory = mkdtempSync(join(tmpdir(), "mailpilot-client-input-"));
  try {
    const repo = join(directory, "repo");
    mkdirSync(repo);
    const outside = join(directory, "client with spaces.json");
    writeFileSync(outside, bytes(fixture), { mode: 0o600 });
    assert.equal(
      readGoogleClient(outside, repo).client_id,
      fixture.installed.client_id,
    );
    const inside = join(repo, "client.json");
    writeFileSync(inside, bytes(fixture));
    const link = join(directory, "link.json");
    symlinkSync(inside, link);
    for (const path of [
      "relative.json",
      inside,
      link,
      join(directory, "missing.json"),
      directory,
    ])
      assert.throws(() => readGoogleClient(path, repo));
    writeFileSync(outside, Buffer.alloc(32769));
    assert.throws(() => readGoogleClient(outside, repo));
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});

test("the personal CLI rejects invalid input before launching a build", () => {
  for (const args of [
    [],
    ["build"],
    ["dev", "--google-client-json", "relative.json"],
    ["build", "--google-client-json", "/missing.json", "--config", "anything"],
  ]) {
    const run = spawnSync(
      process.execPath,
      [new URL("personal.mjs", import.meta.url).pathname, ...args],
      { encoding: "utf8" },
    );
    assert.equal(run.status, 1);
    assert.doesNotMatch(run.stdout, /Built .*Go sidecar/);
  }
});

test("child failure cleans owned input and an existing personal run is preserved", () => {
  const directory = mkdtempSync(join(tmpdir(), "mailpilot-personal-cleanup-"));
  try {
    const repo = join(directory, "repo");
    const scripts = join(repo, "scripts");
    const config = join(repo, "backend/internal/config");
    mkdirSync(scripts, { recursive: true });
    mkdirSync(config, { recursive: true });
    for (const name of ["personal.mjs", "google-client.mjs"])
      cpSync(new URL(name, import.meta.url), join(scripts, name));
    // A fixed child script in a disposable checkout avoids starting native tools.
    writeFileSync(
      join(scripts, "tauri.mjs"),
      `
      import { readFileSync } from 'node:fs';
      const input = JSON.parse(readFileSync(new URL('../backend/internal/config/personal-google-client.json', import.meta.url)));
      if (Object.keys(input).sort().join(',') !== 'client_id,client_secret' || process.env.MAILPILOT_GOOGLE_CLIENT_ID !== undefined || process.env.MAILPILOT_GOOGLE_CLIENT_SECRET !== undefined) process.exit(42);
      process.exit(17);
    `,
    );
    const input = join(directory, "client with spaces.json");
    writeFileSync(input, bytes(fixture));
    const resource = join(config, "personal-google-client.json");
    const run = () =>
      spawnSync(
        process.execPath,
        [join(scripts, "personal.mjs"), "dev", "--google-client-json", input],
        {
          encoding: "utf8",
          env: {
            ...process.env,
            MAILPILOT_GOOGLE_CLIENT_ID: "inherited.apps.googleusercontent.com",
            MAILPILOT_GOOGLE_CLIENT_SECRET: "synthetic-inherited",
          },
        },
      );
    const failed = run();
    assert.equal(failed.status, 17);
    assert.ok(
      !existsSync(resource),
      "Failed child left generated configuration.",
    );
    assert.ok(
      !(failed.stdout + failed.stderr).includes(
        fixture.installed.client_secret,
      ),
      "Diagnostics disclosed a client value.",
    );
    const existing = "synthetic-other-run";
    writeFileSync(resource, existing);
    assert.equal(run().status, 1);
    assert.ok(
      readFileSync(resource, "utf8") === existing,
      "Concurrent personal input was changed.",
    );
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
});
