pub use crate::replies::{ReplyRequest, ReplyResponse};
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

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct CategoryRequest {
    pub operation: String,
    pub account_id: String,
    pub offset: u32,
    pub id: String,
    pub name: String,
    pub description: String,
    pub enabled: Option<bool>,
}
impl CategoryRequest {
    fn valid(&self) -> bool {
        if self.account_id.len() > 100 || self.id.len() > 100 || self.offset > 1_000_000 {
            return false;
        }
        match self.operation.as_str() {
            "list" => {
                self.id.is_empty()
                    && self.name.is_empty()
                    && self.description.is_empty()
                    && self.enabled.is_none()
            }
            "save" => {
                !self.account_id.is_empty()
                    && self.offset == 0
                    && self.enabled.is_some()
                    && !self.name.trim().is_empty()
                    && self.name.len() <= 100
                    && !self.description.trim().is_empty()
                    && self.description.len() <= 1000
            }
            "delete" => {
                !self.account_id.is_empty()
                    && !self.id.is_empty()
                    && self.offset == 0
                    && self.name.is_empty()
                    && self.description.is_empty()
                    && self.enabled.is_none()
            }
            _ => false,
        }
    }
}
#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Category {
    pub id: String,
    pub name: String,
    pub description: String,
    pub enabled: bool,
    pub created_at: i64,
    pub updated_at: i64,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct CategoryPage {
    pub account_id: String,
    pub rows: Vec<Category>,
    pub has_more: bool,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub code: Option<String>,
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ClassificationRequest {
    pub operation: String,
    pub account_id: String,
    pub offset: u32,
}
impl ClassificationRequest {
    fn valid(&self) -> bool {
        self.account_id.len() <= 100
            && self.offset <= 1_000_000
            && (self.operation == "list"
                || (self.operation == "run" && !self.account_id.is_empty()))
    }
}
#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ClassificationPrediction {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub score_kind: Option<String>,
    pub email_id: String,
    pub category_id: String,
    pub category_name: String,
    pub confidence: f64,
    pub classifier_name: String,
    pub classifier_version: String,
    pub reasons: Vec<String>,
    pub timestamp: i64,
}
#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ClassificationAssessment {
    pub used_classifier: String,
    pub reason: String,
    pub candidates: [(); 0],
}
#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ClassificationRow {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub assessment: Option<ClassificationAssessment>,
    pub email_id: String,
    pub sender: String,
    pub subject: String,
    #[serde(rename = "bodyState")]
    pub body_state: String,
    pub state: String,
    pub predictions: Vec<ClassificationPrediction>,
    pub timestamp: Option<i64>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub code: Option<String>,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ClassificationPage {
    pub account_id: String,
    pub rows: Vec<ClassificationRow>,
    pub has_more: bool,
    pub processed: u32,
    pub errors: u32,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub code: Option<String>,
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct OrganizationRequest {
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub source: String,
    pub operation: String,
    pub account_id: String,
    pub offset: u32,
    pub email_ids: Vec<String>,
    pub action_id: String,
    pub digest: String,
}
impl OrganizationRequest {
    fn valid(&self) -> bool {
        if !self.source.is_empty()
            && (self.source != "phishing"
                || !["list", "preview", "dry_run"].contains(&self.operation.as_str()))
        {
            return false;
        }
        if self.account_id.len() > 100
            || self.offset > 1_000_000
            || self.email_ids.len() > 5
            || self.action_id.len() > 100
            || self.digest.len() > 64
        {
            return false;
        }
        for (i, id) in self.email_ids.iter().enumerate() {
            if id.is_empty() || id.len() > 100 || self.email_ids[..i].contains(id) {
                return false;
            }
        }
        match self.operation.as_str() {
            "list" | "status" => self.email_ids.is_empty() && self.digest.is_empty(),
            "preview" | "dry_run" => {
                !self.account_id.is_empty()
                    && !self.email_ids.is_empty()
                    && self.action_id.is_empty()
                    && self.digest.is_empty()
            }
            "undo_preview" => {
                !self.account_id.is_empty()
                    && self.email_ids.is_empty()
                    && self.action_id.is_empty()
                    && self.digest.is_empty()
            }
            "apply" => {
                !self.account_id.is_empty()
                    && !self.action_id.is_empty()
                    && self.digest.len() == 64
                    && self.email_ids.is_empty()
            }
            "reconcile" | "cancel" => {
                !self.account_id.is_empty() && self.email_ids.is_empty() && self.digest.is_empty()
            }
            _ => false,
        }
    }
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct OrganizationRisk {
    pub score: u8,
    pub risk: String,
    pub version: String,
    pub reasons: Vec<RiskReason>,
    pub threshold: u8,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct OrganizationRow {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub phishing: Option<OrganizationRisk>,
    pub email_id: String,
    pub sender: String,
    pub subject: String,
    pub current_labels: Vec<String>,
    pub proposed_label: String,
    pub confidence: f64,
    pub change: String,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct OrganizationStep {
    pub kind: String,
    pub name: String,
    pub email_id: String,
    pub state: String,
    pub code: String,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct OrganizationResponse {
    pub account_id: String,
    pub state: String,
    pub code: String,
    pub id: String,
    pub digest: String,
    pub kind: String,
    pub expires_at: i64,
    pub dry_run: bool,
    pub rows: Vec<OrganizationRow>,
    pub steps: Vec<OrganizationStep>,
    pub has_more: bool,
}

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

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct AuthStatus {
    pub state: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub email: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub code: Option<String>,
}
#[derive(Deserialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
struct AuthBegin {
    status: AuthStatus,
    authorization_url: Option<String>,
}

fn validate_authorization_url(value: &str) -> Result<tauri::Url, String> {
    let url = tauri::Url::parse(value).map_err(|_| "Invalid authorization URL")?;
    if value.len() > 8192
        || url.scheme() != "https"
        || url.host_str() != Some("accounts.google.com")
        || url.path() != "/o/oauth2/v2/auth"
        || !url.username().is_empty()
        || url.password().is_some()
        || url.port().is_some()
        || url.fragment().is_some()
    {
        return Err("Invalid authorization URL".into());
    }
    Ok(url)
}

// Fixed system-browser launch. No arbitrary URLs, frontend arguments, or shell.
fn open_authorization(value: &str) -> Result<(), String> {
    let url = validate_authorization_url(value)?;
    #[cfg(target_os = "macos")]
    let mut command = Command::new("/usr/bin/open");
    #[cfg(target_os = "linux")]
    let mut command = Command::new("xdg-open");
    #[cfg(target_os = "windows")]
    let mut command = {
        let mut c = Command::new("rundll32.exe");
        c.arg("url.dll,FileProtocolHandler");
        c
    };
    let mut child = command
        .arg(url.as_str())
        .stdin(Stdio::null())
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .spawn()
        .map_err(|_| "Cannot open system browser")?;
    let deadline = Instant::now() + Duration::from_secs(2);
    loop {
        if let Some(status) = child.try_wait().map_err(|_| "Cannot open system browser")? {
            return if status.success() {
                Ok(())
            } else {
                Err("Cannot open system browser".into())
            };
        }
        if Instant::now() >= deadline {
            // Some Linux launchers stay alive with the browser. Reap separately;
            // authentication still requires a valid callback, never launch success.
            thread::spawn(move || {
                let _ = child.wait();
            });
            return Ok(());
        }
        thread::sleep(Duration::from_millis(20));
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Response {
    jsonrpc: String,
    id: String,
    result: Option<Value>,
    error: Option<Value>,
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct SyncProgress {
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub mode: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub removed: Option<u32>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub more: Option<bool>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub snapshot_limited: Option<bool>,
    pub state: String,
    pub account_id: String,
    pub limit: u32,
    pub discovered: u32,
    pub imported: u32,
    pub updated: u32,
    pub errors: u32,
    pub capped: bool,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub code: Option<String>,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct RiskReason {
    pub code: String,
    pub points: i32,
    pub explanation: String,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct RiskAssessment {
    pub score: u8,
    pub risk: String,
    pub version: String,
    pub reasons: Vec<RiskReason>,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct MailboxRow {
    pub id: String,
    pub sender: String,
    pub subject: String,
    pub received_at: i64,
    pub body_state: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub phishing: Option<RiskAssessment>,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct MailboxPage {
    pub account_id: String,
    pub rows: Vec<MailboxRow>,
    pub has_more: bool,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub code: Option<String>,
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ReadMailRequest {
    pub account_id: String,
    pub email_id: String,
    pub offset: usize,
    pub revision: String,
}
impl ReadMailRequest {
    fn valid(&self) -> bool {
        !self.account_id.is_empty()
            && self.account_id.len() <= 100
            && !self.email_id.is_empty()
            && self.email_id.len() <= 100
            && self.offset <= 1048576
            && ((self.revision.is_empty() && self.offset == 0)
                || (self.revision.len() == 64
                    && self.revision.bytes().all(|b| b.is_ascii_hexdigit())))
    }
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ReaderAddress {
    name: String,
    address: String,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ReaderAttachment {
    filename: String,
    mime_type: String,
    size_bytes: Option<i64>,
    inline: bool,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ReaderLabel {
    name: String,
    kind: String,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ReaderCategory {
    name: String,
    source: String,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ReaderMessage {
    subject: String,
    sender: ReaderAddress,
    recipients: Vec<ReaderAddress>,
    cc: Vec<ReaderAddress>,
    received_at: i64,
    cached_at: i64,
    attachments: Vec<ReaderAttachment>,
    labels: Vec<ReaderLabel>,
    categories: Vec<ReaderCategory>,
    category_state: String,
    phishing: Option<RiskAssessment>,
    body_state: String,
    body_format: String,
    body_error: bool,
    metadata_truncated: bool,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ReadMailResponse {
    account_id: String,
    email_id: String,
    revision: String,
    offset: usize,
    next_offset: usize,
    total_bytes: usize,
    body: String,
    message: Option<ReaderMessage>,
    pub code: String,
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
                .recv_timeout(Duration::from_secs(if method == "classification.review" {
                    32
                } else {
                    7
                }))
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

    pub fn category_vectors(
        &mut self,
        request: crate::category_vectors::CategoryVectorRequest,
    ) -> Result<crate::category_vectors::CategoryVectorResponse, String> {
        if !request.valid() {
            return Err("Invalid category vector request".into());
        }
        let account = request.account_id.clone();
        let category = request.category_id.clone();
        let response: crate::category_vectors::CategoryVectorResponse =
            serde_json::from_value(self.request(
                "categories.vectors",
                serde_json::to_value(request).map_err(|_| "Invalid category vector request")?,
            )?)
            .map_err(|_| "Invalid category vector response")?;
        if !response.valid()
            || (response.code.is_empty()
                && (response.account_id != account || response.category_id != category))
        {
            return Err("Invalid category vector response".into());
        }
        Ok(response)
    }
    pub fn vector_search(
        &mut self,
        request: crate::search::SearchRequest,
    ) -> Result<crate::search::SearchResponse, String> {
        if !request.valid() {
            return Err("Invalid vector search request".into());
        }
        let account = request.account_id.clone();
        let response: crate::search::SearchResponse = serde_json::from_value(self.request(
            "search.local",
            serde_json::to_value(request).map_err(|_| "Invalid vector search request")?,
        )?)
        .map_err(|_| "Invalid vector search response")?;
        if !response.valid()
            || (!account.is_empty()
                && !response.account_id.is_empty()
                && response.account_id != account)
        {
            return Err("Invalid vector search response".into());
        }
        Ok(response)
    }
    pub fn local_index(
        &mut self,
        request: crate::indexing::IndexRequest,
    ) -> Result<crate::indexing::IndexResponse, String> {
        if !request.valid() {
            return Err("Invalid local index request".into());
        }
        let account = request.account_id.clone();
        let response: crate::indexing::IndexResponse = serde_json::from_value(self.request(
            "index.local",
            serde_json::to_value(request).map_err(|_| "Invalid local index request")?,
        )?)
        .map_err(|_| "Invalid local index response")?;
        if !response.valid()
            || (!account.is_empty()
                && !response.account_id.is_empty()
                && response.account_id != account)
        {
            return Err("Invalid local index response".into());
        }
        Ok(response)
    }
    pub fn auth_status(&mut self, method: &str) -> Result<AuthStatus, String> {
        if ![
            "auth.status",
            "auth.check",
            "auth.disconnect",
            "auth.cancel",
        ]
        .contains(&method)
        {
            return Err("Invalid authentication command".into());
        }
        serde_json::from_value(self.request(method, json!({}))?)
            .map_err(|_| "Invalid authentication status".into())
    }
    pub fn auth_begin(&mut self) -> Result<AuthStatus, String> {
        let begin: AuthBegin = serde_json::from_value(self.request("auth.begin", json!({}))?)
            .map_err(|_| "Invalid authentication response")?;
        if let Some(url) = begin.authorization_url {
            if open_authorization(&url).is_err() {
                let _ = self.auth_status("auth.cancel");
                return Err("Cannot open system browser".into());
            }
        }
        Ok(begin.status)
    }

    pub fn cancel_sync(&mut self) -> Result<SyncProgress, String> {
        serde_json::from_value(self.request("sync.cancel", json!({}))?)
            .map_err(|_| "Invalid sync response".into())
    }
    pub fn sync(&mut self, start: bool) -> Result<SyncProgress, String> {
        let method = if start { "sync.start" } else { "sync.status" };
        serde_json::from_value(self.request(method, json!({}))?)
            .map_err(|_| "Invalid sync response".into())
    }
    pub fn organization(
        &mut self,
        request: OrganizationRequest,
    ) -> Result<OrganizationResponse, String> {
        if !request.valid() {
            return Err("Invalid organization request".into());
        }
        serde_json::from_value(self.request(
            "organization.manage",
            serde_json::to_value(request).map_err(|_| "Invalid organization request")?,
        )?)
        .map_err(|_| "Invalid organization response".into())
    }
    pub fn ai_settings(
        &mut self,
        request: crate::ai::AISettingsRequest,
    ) -> Result<crate::ai::AISettingsResponse, String> {
        if !request.valid() {
            return Err("Invalid AI settings request".into());
        }
        let result: crate::ai::AISettingsResponse = serde_json::from_value(self.request(
            "ai.settings",
            serde_json::to_value(request).map_err(|_| "Invalid AI settings request")?,
        )?)
        .map_err(|_| "Invalid AI settings response")?;
        if !result.valid() {
            return Err("Invalid AI settings response".into());
        }
        Ok(result)
    }
    pub fn summarize_email(
        &mut self,
        request: crate::ai::SummaryRequest,
    ) -> Result<crate::ai::SummaryResponse, String> {
        if !request.valid() {
            return Err("Invalid summary request".into());
        }
        let result: crate::ai::SummaryResponse = serde_json::from_value(self.request(
            "email.summarize",
            serde_json::to_value(request).map_err(|_| "Invalid summary request")?,
        )?)
        .map_err(|_| "Invalid summary response")?;
        if !result.valid() {
            return Err("Invalid summary response".into());
        }
        Ok(result)
    }
    pub fn reply(&mut self, request: ReplyRequest) -> Result<ReplyResponse, String> {
        if !request.valid() {
            return Err("Invalid reply request".into());
        }
        let result: ReplyResponse = serde_json::from_value(self.request(
            "reply.manage",
            serde_json::to_value(request).map_err(|_| "Invalid reply request")?,
        )?)
        .map_err(|_| "Invalid reply response")?;
        if !result.valid() {
            return Err("Invalid reply response".into());
        }
        Ok(result)
    }
    pub fn classifications(
        &mut self,
        request: ClassificationRequest,
    ) -> Result<ClassificationPage, String> {
        if !request.valid() {
            return Err("Invalid classification request".into());
        }
        serde_json::from_value(self.request(
            "classification.review",
            serde_json::to_value(request).map_err(|_| "Invalid classification request")?,
        )?)
        .map_err(|_| "Invalid classification response".into())
    }
    pub fn categories(&mut self, request: CategoryRequest) -> Result<CategoryPage, String> {
        if !request.valid() {
            return Err("Invalid category request".into());
        }
        serde_json::from_value(self.request(
            "categories.manage",
            serde_json::to_value(request).map_err(|_| "Invalid category request")?,
        )?)
        .map_err(|_| "Invalid category response".into())
    }
    pub fn read_mail(&mut self, request: ReadMailRequest) -> Result<ReadMailResponse, String> {
        if !request.valid() {
            return Err("Invalid reader request".into());
        }
        let response: ReadMailResponse = serde_json::from_value(self.request(
            "mail.read",
            serde_json::to_value(&request).map_err(|_| "Invalid reader request")?,
        )?)
        .map_err(|_| "Invalid reader response")?;
        if response.code.is_empty()
            && (response.message.is_none()
                || response.account_id != request.account_id
                || response.email_id != request.email_id
                || response.offset != request.offset
                || response.next_offset != response.offset + response.body.len()
                || response.next_offset > response.total_bytes
                || response.total_bytes > 1048576
                || response.body.len() > 16384
                || response.revision.len() != 64
                || (!request.revision.is_empty() && response.revision != request.revision))
        {
            return Err("Invalid reader response".into());
        }
        Ok(response)
    }
    pub fn mailbox(&mut self, offset: u32) -> Result<MailboxPage, String> {
        if offset > 1_000_000 {
            return Err("Invalid mailbox page".into());
        }
        serde_json::from_value(self.request("mail.list", json!({"offset":offset}))?)
            .map_err(|_| "Invalid mailbox response".into())
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
    fn browser_is_restricted_to_google_authorization() {
        assert!(validate_authorization_url(
            "https://accounts.google.com/o/oauth2/v2/auth?client_id=test"
        )
        .is_ok());
        for url in [
            "http://accounts.google.com/o/oauth2/v2/auth",
            "https://accounts.google.com.evil.test/o/oauth2/v2/auth",
            "https://evil.test/o/oauth2/v2/auth",
            "https://user@accounts.google.com/o/oauth2/v2/auth",
            "https://accounts.google.com:1234/o/oauth2/v2/auth",
            "https://accounts.google.com/other",
            "file:///tmp/test",
        ] {
            assert!(validate_authorization_url(url).is_err());
        }
    }

    #[test]
    fn authentication_dto_rejects_token_fields() {
        assert!(serde_json::from_value::<AuthStatus>(
            json!({"state":"connected","accessToken":"synthetic"})
        )
        .is_err());
    }

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

#[cfg(test)]
mod category_tests {
    use super::*;
    #[test]
    fn category_contract_and_validation() {
        let fixture: Value =
            serde_json::from_str(include_str!("../../contracts/categories-v1.json")).unwrap();
        let mut request: CategoryRequest =
            serde_json::from_value(fixture["request"].clone()).unwrap();
        assert!(request.valid());
        let response: CategoryPage = serde_json::from_value(fixture["response"].clone()).unwrap();
        assert_eq!(response.rows.len(), 1);
        request.operation = "classify".into();
        assert!(!request.valid());
        request.operation = "save".into();
        request.enabled = None;
        assert!(!request.valid());
        let mut malformed = fixture["request"].clone();
        malformed["unexpected"] = json!(true);
        assert!(serde_json::from_value::<CategoryRequest>(malformed).is_err());
    }
}

#[cfg(test)]
mod classification_tests {
    use super::*;
    #[test]
    fn active_review_rejects_retired_candidate_state() {
        let fixture: Value = serde_json::from_str(include_str!(
            "../../contracts/classification-semantic-v1.json"
        ))
        .unwrap();
        assert!(serde_json::from_value::<ClassificationPage>(fixture["response"].clone()).is_err());
    }

    #[test]
    fn classification_contract_and_strict_requests() {
        let fixture: Value =
            serde_json::from_str(include_str!("../../contracts/classification-v1.json")).unwrap();
        let mut request: ClassificationRequest =
            serde_json::from_value(fixture["request"].clone()).unwrap();
        assert!(request.valid());
        let response: ClassificationPage =
            serde_json::from_value(fixture["response"].clone()).unwrap();
        assert_eq!(response.rows.len(), 2);
        assert_eq!(response.rows[0].predictions[0].reasons.len(), 3);
        request.account_id.clear();
        assert!(!request.valid());
        request.operation = "list".into();
        assert!(request.valid());
        request.operation = "apply".into();
        assert!(!request.valid());
        let mut bad = fixture["request"].clone();
        bad["body"] = json!("not allowed");
        assert!(serde_json::from_value::<ClassificationRequest>(bad).is_err());
        bad = fixture["request"].clone();
        bad["offset"] = json!(-1);
        assert!(serde_json::from_value::<ClassificationRequest>(bad).is_err());
    }
}

#[cfg(test)]
mod organization_tests {
    use super::*;
    #[test]
    fn strict_organization_contract() {
        let fixture: Value =
            serde_json::from_str(include_str!("../../contracts/organization-v1.json")).unwrap();
        let mut request: OrganizationRequest =
            serde_json::from_value(fixture["request"].clone()).unwrap();
        assert!(request.valid());
        let response: OrganizationResponse =
            serde_json::from_value(fixture["response"].clone()).unwrap();
        assert_eq!(response.rows[0].proposed_label, "MailPilot/Receipts");
        assert_eq!(response.steps.len(), 2);
        request.operation = "delete".into();
        assert!(!request.valid());
        request.operation = "preview".into();
        request.email_ids.push(request.email_ids[0].clone());
        assert!(!request.valid());
        let mut bad = fixture["request"].clone();
        bad["shell"] = serde_json::json!("synthetic");
        assert!(serde_json::from_value::<OrganizationRequest>(bad).is_err());
    }
}

#[cfg(test)]
mod risk_tests {
    use super::*;
    #[test]
    fn phishing_contract_preserves_signed_reasons() {
        let fixture = include_str!("../../contracts/phishing-v1.json");
        let page: MailboxPage = serde_json::from_str(fixture).unwrap();
        let assessment = page.rows[0].phishing.as_ref().unwrap();
        assert_eq!(assessment.score, 7);
        assert_eq!(assessment.reasons[1].points, -8);
        let encoded = serde_json::to_value(page).unwrap();
        assert_eq!(
            encoded,
            serde_json::from_str::<serde_json::Value>(fixture).unwrap()
        );
    }
}

#[cfg(test)]
mod phishing_organization_tests {
    use super::*;
    #[test]
    fn reviewed_risk_contract_and_source_validation() {
        let fixture: Value = serde_json::from_str(include_str!(
            "../../contracts/organization-phishing-v1.json"
        ))
        .unwrap();
        let request: OrganizationRequest =
            serde_json::from_value(fixture["request"].clone()).unwrap();
        assert!(request.valid());
        let response: OrganizationResponse =
            serde_json::from_value(fixture["response"].clone()).unwrap();
        assert_eq!(response.rows[0].phishing.as_ref().unwrap().threshold, 60);
        let mut expected = fixture["response"].clone();
        expected["rows"][0]["confidence"] = serde_json::json!(0.0);
        assert_eq!(serde_json::to_value(response).unwrap(), expected);
        let mut invalid = request;
        invalid.source = "spam".into();
        assert!(!invalid.valid());
        invalid.source = "phishing".into();
        invalid.operation = "apply".into();
        invalid.email_ids.clear();
        invalid.action_id = "action".into();
        invalid.digest = "a".repeat(64);
        assert!(!invalid.valid());
    }
}

#[cfg(test)]
mod reader_tests {
    use super::*;
    #[test]
    fn cached_reader_contract_and_scope_validation() {
        let fixture = include_str!("../../contracts/mail-reader-v1.json");
        let response: ReadMailResponse = serde_json::from_str(fixture).unwrap();
        assert_eq!(response.body.len(), response.next_offset);
        let mut value: Value = serde_json::from_str(fixture).unwrap();
        value["message"]["html"] = json!("<script/>");
        assert!(serde_json::from_value::<ReadMailResponse>(value).is_err());
        let mut request = ReadMailRequest {
            account_id: "a".into(),
            email_id: "e".into(),
            offset: 0,
            revision: String::new(),
        };
        assert!(request.valid());
        request.offset = 1;
        assert!(!request.valid());
        request.revision = "a".repeat(64);
        assert!(request.valid());
        request.account_id.clear();
        assert!(!request.valid());
    }
}

#[cfg(test)]
mod incremental_sync_tests {
    use super::*;
    #[test]
    fn incremental_progress_contract() {
        let raw = include_str!("../../contracts/sync-incremental-v1.json");
        let status: SyncProgress = serde_json::from_str(raw).unwrap();
        assert_eq!(status.mode.as_deref(), Some("incremental"));
        assert_eq!(status.removed, Some(1));
        let mut value: Value = serde_json::from_str(raw).unwrap();
        value["historyId"] = json!("private-provider-cursor");
        assert!(serde_json::from_value::<SyncProgress>(value).is_err());
    }
}
