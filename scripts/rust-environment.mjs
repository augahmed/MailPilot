import { spawnSync } from "node:child_process";
import { homedir } from "node:os";
import { delimiter, join } from "node:path";

// Preserve an existing toolchain. Fall back to rustup's per-user bin directory
// for terminals that have not loaded ~/.cargo/env (or a custom CARGO_HOME).
export function rustEnvironment() {
  const env = { ...process.env };
  const pathKey =
    Object.keys(env).find((key) => key.toLowerCase() === "path") || "PATH";
  const available = (command) =>
    spawnSync(command, ["--version"], { env, stdio: "ignore", timeout: 10000 })
      .status === 0;

  if (!available("cargo") || !available("rustc")) {
    const cargoBin = join(env.CARGO_HOME || join(homedir(), ".cargo"), "bin");
    env[pathKey] = [env[pathKey], cargoBin].filter(Boolean).join(delimiter);
  }
  if (!available("cargo") || !available("rustc")) {
    throw new Error(
      "Cargo/Rust is unavailable. Install a Rust toolchain from https://rustup.rs/ and verify cargo --version and rustc --version. If using a custom installation, add its bin directory to PATH.",
    );
  }
  return env;
}
