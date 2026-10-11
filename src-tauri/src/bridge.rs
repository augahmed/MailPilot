use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::{
    io::{BufRead, BufReader, Read, Write},
    path::Path,
    process::{Child, ChildStdin, Command, Stdio},
    sync::mpsc::{self, Receiver},
    thread,
    time::{Duration, Instant},
};

const MAX_FRAME: u64 = 64 * 1024;
const PROTOCOL_VERSION: u32 = 1;

#[derive(Debug, Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Health {
    pub name: String,
    pub version: String,
    pub protocol_version: u32,
    pub backend: String,
    pub database: String,
    pub schema_version: u32,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Response {
    jsonrpc: String,
    id: String,
    result: Option<Value>,
    error: Option<Value>,
}

pub struct Bridge {
    child: Child,
    input: Option<ChildStdin>,
    responses: Receiver<Vec<u8>>,
    next_id: u64,
    failed: bool,
}

fn read_frame(reader: &mut impl BufRead) -> std::io::Result<Option<Vec<u8>>> {
    let mut bytes = Vec::new();
    let size = reader.take(MAX_FRAME + 1).read_until(b'\n', &mut bytes)?;
    if size == 0 {
        return Ok(None);
    }
    if size as u64 > MAX_FRAME || bytes.last() != Some(&b'\n') {
        return Err(std::io::Error::new(
            std::io::ErrorKind::InvalidData,
            "invalid frame",
        ));
    }
    Ok(Some(bytes))
}

impl Bridge {
    pub fn start(data_dir: &Path) -> Result<Self, String> {
        let host = std::env::current_exe().map_err(|_| "Cannot locate desktop executable")?;
        let directory = host.parent().ok_or("Cannot locate application directory")?;
        let filename = if cfg!(windows) {
            "mailpilot-core.exe"
        } else {
            "mailpilot-core"
        };
        let mut command = Command::new(directory.join(filename));
        // The host supplies the native default. A user's explicit non-secret override
        // remains available to the core for isolated development/smoke-test profiles.
        if std::env::var_os("MAILPILOT_DATA_DIR").is_none() {
            command.arg("--data-dir").arg(data_dir);
        }
        let mut child = command
            .stdin(Stdio::piped())
            .stdout(Stdio::piped())
            .stderr(Stdio::inherit())
            .spawn()
            .map_err(|_| "Cannot start packaged Go backend")?;
        let input = child.stdin.take();
        let output = child.stdout.take().expect("piped stdout exists");
        let (sender, responses) = mpsc::sync_channel(1);
        thread::spawn(move || {
            let mut reader = BufReader::new(output);
            while let Ok(Some(frame)) = read_frame(&mut reader) {
                if sender.send(frame).is_err() {
                    break;
                }
            }
        });
        let mut bridge = Self {
            child,
            input,
            responses,
            next_id: 0,
            failed: false,
        };
        let result = bridge.request("initialize", json!({"protocolVersion": PROTOCOL_VERSION}))?;
        if result != json!({"protocolVersion": PROTOCOL_VERSION}) {
            return Err("Incompatible backend protocol".into());
        }
        Ok(bridge)
    }

    fn request(&mut self, method: &str, params: Value) -> Result<Value, String> {
        if self.failed {
            return Err("Backend disconnected; restart MailPilot".into());
        }
        self.next_id += 1;
        let id = self.next_id.to_string();
        let message = json!({"jsonrpc":"2.0","id":id,"method":method,"params":params});
        let outcome = (|| {
            let input = self.input.as_mut().ok_or("Backend disconnected")?;
            writeln!(input, "{message}").map_err(|_| "Backend disconnected")?;
            input.flush().map_err(|_| "Backend disconnected")?;
            let bytes = self
                .responses
                .recv_timeout(Duration::from_secs(7))
                .map_err(|_| "Backend did not respond")?;
            let response: Response =
                serde_json::from_slice(&bytes).map_err(|_| "Invalid backend response")?;
            if response.jsonrpc != "2.0" || response.id != id {
                return Err("Backend protocol mismatch");
            }
            if response.error.is_some() {
                return Err("Backend request failed");
            }
            response.result.ok_or("Missing backend result")
        })();
        if outcome.is_err() {
            self.failed = true;
            self.input.take();
            let _ = self.child.kill();
            let _ = self.child.wait();
        }
        outcome.map_err(str::to_owned)
    }

    pub fn health(&mut self) -> Result<Health, String> {
        let value = self.request("health", json!({}))?;
        let health: Health =
            serde_json::from_value(value).map_err(|_| "Invalid health response")?;
        if health.name != "MailPilot"
            || health.version != env!("CARGO_PKG_VERSION")
            || health.protocol_version != PROTOCOL_VERSION
            || health.backend != "connected"
            || !["connected", "disconnected"].contains(&health.database.as_str())
        {
            return Err("Incompatible backend health response".into());
        }
        Ok(health)
    }
}

impl Drop for Bridge {
    fn drop(&mut self) {
        // Closing the private pipe requests a graceful core shutdown (EOF).
        self.input.take();
        let deadline = Instant::now() + Duration::from_secs(1);
        while Instant::now() < deadline {
            if let Ok(Some(_)) = self.child.try_wait() {
                return;
            }
            thread::sleep(Duration::from_millis(20));
        }
        let _ = self.child.kill();
        let _ = self.child.wait();
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Cursor;

    #[test]
    fn frames_handle_fragmented_reads_and_eof() {
        let mut reader = BufReader::with_capacity(2, Cursor::new(b"{\"x\":1}\n{}\n"));
        assert_eq!(read_frame(&mut reader).unwrap().unwrap(), b"{\"x\":1}\n");
        assert_eq!(read_frame(&mut reader).unwrap().unwrap(), b"{}\n");
        assert!(read_frame(&mut reader).unwrap().is_none());
    }

    #[test]
    fn oversized_or_unterminated_frames_are_rejected() {
        assert!(read_frame(&mut Cursor::new(vec![b'x'; MAX_FRAME as usize + 1])).is_err());
        assert!(read_frame(&mut Cursor::new(b"{}")).is_err());
    }
}
