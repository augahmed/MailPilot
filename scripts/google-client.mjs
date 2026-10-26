import {
  constants,
  openSync,
  fstatSync,
  readSync,
  closeSync,
  realpathSync,
} from "node:fs";
import { isAbsolute, relative, sep, resolve } from "node:path";

const maxBytes = 32 * 1024;
const invalid = () =>
  new Error(
    "Invalid Google Desktop app JSON. Download a Desktop app OAuth client; tokens and API keys are not supported.",
  );
const validID = (value) =>
  typeof value === "string" &&
  value.length <= 256 &&
  /^[A-Za-z0-9._-]+\.apps\.googleusercontent\.com$/.test(value);
const validSecret = (value) =>
  typeof value === "string" &&
  value.length > 0 &&
  value.length <= 256 &&
  /^[A-Za-z0-9_-]+$/.test(value) &&
  !value.startsWith("sk-") &&
  !value.startsWith("AIza");
const object = (value) =>
  value !== null && typeof value === "object" && !Array.isArray(value);

export function parseGoogleClient(raw) {
  try {
    if (raw.byteLength === 0 || raw.byteLength > maxBytes) throw invalid();
    const data = JSON.parse(
      new TextDecoder("utf-8", { fatal: true }).decode(raw),
    );
    if (
      !object(data) ||
      Object.keys(data).length !== 1 ||
      !object(data.installed)
    )
      throw invalid();
    const client = data.installed;
    const allowed = new Set([
      "client_id",
      "client_secret",
      "project_id",
      "auth_uri",
      "token_uri",
      "auth_provider_x509_cert_url",
      "redirect_uris",
    ]);
    if (
      Object.keys(client).some((key) => !allowed.has(key)) ||
      !validID(client.client_id) ||
      !validSecret(client.client_secret)
    )
      throw invalid();
    if (
      client.project_id !== undefined &&
      (typeof client.project_id !== "string" || client.project_id.length > 256)
    )
      throw invalid();
    for (const [key, values] of [
      [
        "auth_uri",
        [
          "https://accounts.google.com/o/oauth2/auth",
          "https://accounts.google.com/o/oauth2/v2/auth",
        ],
      ],
      ["token_uri", ["https://oauth2.googleapis.com/token"]],
      [
        "auth_provider_x509_cert_url",
        ["https://www.googleapis.com/oauth2/v1/certs"],
      ],
    ]) {
      if (client[key] !== undefined && !values.includes(client[key]))
        throw invalid();
    }
    if (client.redirect_uris !== undefined) {
      if (
        !Array.isArray(client.redirect_uris) ||
        client.redirect_uris.length > 16
      )
        throw invalid();
      for (const value of client.redirect_uris) {
        if (typeof value !== "string" || value.length > 2048) throw invalid();
        const url = new URL(value);
        if (
          url.protocol !== "http:" ||
          !["localhost", "127.0.0.1", "[::1]"].includes(url.hostname) ||
          url.username ||
          url.password ||
          url.search ||
          url.hash
        )
          throw invalid();
      }
    }
    // Deliberately discard all metadata, endpoints and redirects.
    return { client_id: client.client_id, client_secret: client.client_secret };
  } catch {
    throw invalid(); // JSON/UTF-8 errors must never echo input contents.
  }
}

const inside = (root, path) => {
  const part = relative(root, path);
  return (
    part === "" ||
    (!isAbsolute(part) && part !== ".." && !part.startsWith(`..${sep}`))
  );
};

export function readGoogleClient(path, root) {
  if (!path || !isAbsolute(path))
    throw new Error(
      "Provide --google-client-json with an absolute path outside this repository.",
    );
  let fd;
  try {
    const realRoot = realpathSync(root);
    const realPath = realpathSync(path);
    if (inside(realRoot, realPath) || inside(resolve(root), resolve(path)))
      throw invalid();
    fd = openSync(
      realPath,
      constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK,
    );
    const stat = fstatSync(fd);
    if (!stat.isFile() || stat.size < 1 || stat.size > maxBytes)
      throw invalid();
    const raw = Buffer.alloc(maxBytes + 1);
    let size = 0;
    while (size < raw.length) {
      const count = readSync(fd, raw, size, raw.length - size, null);
      if (count === 0) break;
      size += count;
    }
    return parseGoogleClient(raw.subarray(0, size));
  } catch {
    throw new Error(
      "Could not read a valid Google Desktop app JSON file outside this repository (maximum 32 KiB). The file was not printed or copied.",
    );
  } finally {
    if (fd !== undefined) closeSync(fd);
  }
}
