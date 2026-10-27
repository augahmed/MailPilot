import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { createInterface } from "node:readline";
import { dirname, join } from "node:path";
import { homedir } from "node:os";
import { fileURLToPath } from "node:url";

if (process.platform !== "darwin" || process.arch !== "arm64")
  throw new Error(
    "Native demo qualification currently requires Apple Silicon macOS.",
  );
const root = dirname(dirname(fileURLToPath(import.meta.url)));
const app = join(
  root,
  "src-tauri/target/release/bundle/macos/MailPilot Demo.app",
);
const accountId = "mailpilot-synthetic-demo-v1";
const env = {
  ...process.env,
  MAILPILOT_GOOGLE_CLIENT_ID: "invalid-ignored",
  MAILPILOT_GOOGLE_CLIENT_SECRET: "ignored",
};
delete env.MAILPILOT_DATA_DIR;
const forbidden = [
  join(homedir(), "Library/Application Support/com.mailpilot.desktop"),
  join(homedir(), "Library/Keychains"),
];
const policy = `(version 1) (allow default) (deny network*) ${forbidden.map((path) => `(deny file-read* file-write* (subpath ${JSON.stringify(path)}))`).join(" ")}`;
const child = spawn(
  "/usr/bin/sandbox-exec",
  ["-p", policy, join(app, "Contents/MacOS/mailpilot-core"), "--demo"],
  { env, stdio: ["pipe", "pipe", "pipe"] },
);
const exited = new Promise((resolve, reject) => {
  child.on("exit", resolve);
  child.on("error", reject);
});
let diagnostics = "";
child.stderr.on("data", (b) => {
  diagnostics = (diagnostics + b).slice(-16000);
});
const lines = createInterface({ input: child.stdout })[Symbol.asyncIterator]();
let id = 0;
const timer = setTimeout(() => child.kill("SIGTERM"), 120000);
async function call(method, params = {}) {
  child.stdin.write(
    JSON.stringify({ jsonrpc: "2.0", id: String(++id), method, params }) + "\n",
  );
  const line = await lines.next();
  assert.ok(!line.done, "Demo core stopped before responding");
  const reply = JSON.parse(line.value);
  assert.ok(!reply.error, `Demo RPC failed: ${method}`);
  return reply.result;
}
const poll = async (method, params, field) => {
  let r;
  for (let i = 0; i < 2000; i++) {
    r = await call(method, params);
    if (r[field] !== "running") return r;
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
  throw new Error("Demo work did not finish");
};
try {
  await call("initialize", { protocolVersion: 1 });
  const auth = await call("auth.status");
  assert.equal(auth.demo, true);
  assert.equal(auth.state, "connected");
  const blocked = await call("auth.begin");
  assert.equal(blocked.status.code, "demo_mode");
  assert.ok(!blocked.authorizationUrl);
  const inbox = await call("mail.list", { offset: 0 });
  assert.equal(inbox.rows.length, 10);
  assert.equal((await call("mail.list", { offset: 10 })).rows.length, 2);
  assert.equal((await call("sync.start")).state, "error");
  assert.equal(
    (
      await call("ai.settings", {
        operation: "configure",
        accountId,
        provider: "openai",
        model: "synthetic",
        apiKey: "synthetic-unused-key",
      })
    ).code,
    "ai_disabled",
  );
  const before = await call("index.local", { operation: "status", accountId });
  assert.notEqual(before.status, "running");
  await call("search.local", {
    operation: "start",
    accountId,
    query: "MP-1042",
    limit: 10,
    mode: "keyword",
  });
  let result = await poll(
    "search.local",
    { operation: "status", accountId },
    "status",
  );
  assert.equal(result.code, "");
  assert.ok(result.emails.length > 0);
  assert.equal(
    (
      await call("mail.read", {
        accountId: "foreign",
        emailId: "demo-receipt",
        offset: 0,
        revision: "",
      })
    ).code,
    "unauthorized",
  );
  const start = await call("index.local", { operation: "start", accountId });
  assert.equal(start.code, "", "Index start failed: " + JSON.stringify(start));
  let indexed;
  for (let i = 0; i < 3000; i++) {
    indexed = await call("index.local", { operation: "status", accountId });
    if (indexed.status !== "running") break;
    await new Promise((resolve) => setTimeout(resolve, 20));
  }
  assert.equal(
    indexed.status,
    "complete",
    "Index failed: " + JSON.stringify(indexed),
  );
  assert.equal(indexed.indexedMessages, 12);
  await call("search.local", {
    operation: "start",
    accountId,
    query: "flight reservation",
    limit: 10,
    mode: "hybrid",
  });
  result = await poll(
    "search.local",
    { operation: "status", accountId },
    "status",
  );
  assert.equal(result.code, "");
  assert.equal(result.mode, "hybrid");
  assert.ok(result.emails.some((row) => row.emailId === "demo-flight"));
  await call("organization.manage", {
    operation: "preview",
    accountId,
    emailIds: ["demo-application"],
  });
  const plan = await poll(
    "organization.manage",
    { operation: "status", accountId },
    "state",
  );
  assert.equal(plan.code, "");
  assert.equal(plan.state, "preview");
  assert.equal(
    (
      await call("organization.manage", {
        operation: "apply",
        accountId,
        actionId: plan.id,
        digest: plan.digest,
      })
    ).code,
    "unsupported",
  );
  await call("shutdown");
  child.stdin.end();
  assert.equal(await exited, 0, "Demo core did not shut down cleanly");
  console.log(
    "Demo core passed with all network access and real profile/keychain paths denied: seeded mail, keyword retrieval, explicit real MiniLM indexing, hybrid results, label preview and rejected writes/AI/cross-profile reads.",
  );
} finally {
  clearTimeout(timer);
  child.stdin.end();
  if (child.exitCode === null) child.kill("SIGTERM");
}

const desktop = spawn(join(app, "Contents/MacOS/mailpilot"), ["--smoke-demo"], {
  env,
  stdio: ["ignore", "ignore", "pipe"],
});
let desktopLog = "";
desktop.stderr.on("data", (b) => {
  desktopLog = (desktopLog + b).slice(-16000);
});
const deadline = setTimeout(() => desktop.kill("SIGTERM"), 30000);
try {
  const code = await new Promise((resolve, reject) => {
    desktop.on("error", reject);
    desktop.on("exit", resolve);
  });
  assert.ok(
    code === 0 && desktopLog.includes('"event":"frontend_demo_ok"'),
    "Native demo presentation smoke failed: " + desktopLog,
  );
  console.log(
    "Packaged demo launched without credentials: persistent synthetic banner, offline mailbox, navigation and disabled external-service presentation passed.",
  );
} finally {
  clearTimeout(deadline);
}
