use serde::{Deserialize, Serialize};
fn bounded(value: &str, limit: usize) -> bool {
    value.len() <= limit && !value.contains(['\0', '\r', '\n'])
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct AISettingsRequest {
    pub operation: String,
    pub account_id: String,
    pub provider: String,
    pub model: String,
    pub api_key: String,
}
impl AISettingsRequest {
    pub fn valid(&self) -> bool {
        if !bounded(&self.account_id, 100)
            || !bounded(&self.provider, 64)
            || !bounded(&self.model, 128)
            || self.api_key.len() > 2048
        {
            return false;
        }
        match self.operation.as_str() {
            "status" => {
                self.provider.is_empty() && self.model.is_empty() && self.api_key.is_empty()
            }
            "disable" => {
                !self.account_id.is_empty()
                    && self.provider.is_empty()
                    && self.model.is_empty()
                    && self.api_key.is_empty()
            }
            "configure" => {
                !self.account_id.is_empty()
                    && !self.provider.is_empty()
                    && !self.model.is_empty()
                    && self.api_key.bytes().all(|c| (33..=126).contains(&c))
            }
            _ => false,
        }
    }
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ProviderOption {
    pub id: String,
    pub external: bool,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct AISettingsResponse {
    pub account_id: String,
    pub provider: String,
    pub model: String,
    pub enabled: bool,
    pub providers: Vec<ProviderOption>,
    pub code: String,
}
impl AISettingsResponse {
    pub fn valid(&self) -> bool {
        bounded(&self.account_id, 100)
            && bounded(&self.provider, 64)
            && bounded(&self.model, 128)
            && bounded(&self.code, 64)
            && self.providers.len() <= 20
            && self.providers.iter().all(|p| bounded(&p.id, 64))
    }
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct SummaryRequest {
    pub operation: String,
    pub account_id: String,
    pub email_id: String,
    pub preview_id: String,
    pub job_id: String,
    pub approved: bool,
}
impl SummaryRequest {
    pub fn valid(&self) -> bool {
        if self.account_id.is_empty()
            || self.email_id.is_empty()
            || [
                &self.account_id,
                &self.email_id,
                &self.preview_id,
                &self.job_id,
            ]
            .iter()
            .any(|v| !bounded(v, 100))
        {
            return false;
        }
        match self.operation.as_str() {
            "preview" | "status" | "clear" => {
                self.preview_id.is_empty() && self.job_id.is_empty() && !self.approved
            }
            "approve" => !self.preview_id.is_empty() && self.job_id.is_empty() && self.approved,
            "cancel" => self.preview_id.is_empty() && !self.job_id.is_empty() && !self.approved,
            _ => false,
        }
    }
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct SummaryResult {
    pub summary: String,
    pub requests: Vec<String>,
    pub deadlines: Vec<String>,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct SummaryResponse {
    pub account_id: String,
    pub email_id: String,
    pub state: String,
    pub job_id: String,
    pub preview_id: String,
    pub expires_at: i64,
    pub provider: String,
    pub model: String,
    pub external: bool,
    pub email: String,
    pub policy: String,
    pub truncated: bool,
    pub result: Option<SummaryResult>,
    pub code: String,
}
impl SummaryResponse {
    pub fn valid(&self) -> bool {
        ["idle", "preview", "running", "completed", "error"].contains(&self.state.as_str())
            && [
                &self.account_id,
                &self.email_id,
                &self.job_id,
                &self.preview_id,
            ]
            .iter()
            .all(|v| bounded(v, 100))
            && self.provider.len() <= 64
            && self.model.len() <= 128
            && self.email.len() <= 6000
            && self.policy.len() <= 2000
            && self.expires_at >= 0
            && self.code.len() <= 64
            && self.result.as_ref().is_none_or(|r| {
                r.summary.len() <= 4000
                    && r.requests.len() <= 10
                    && r.deadlines.len() <= 10
                    && r.requests
                        .iter()
                        .chain(&r.deadlines)
                        .all(|s| s.len() <= 500)
            })
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::{json, Value};
    #[test]
    fn summary_contract_is_one_selected_email_without_prompt_or_tools() {
        let fixture: Value =
            serde_json::from_str(include_str!("../../contracts/email-summary-v1.json")).unwrap();
        let request: SummaryRequest = serde_json::from_value(fixture["request"].clone()).unwrap();
        assert!(request.valid());
        let result: SummaryResponse = serde_json::from_value(fixture["response"].clone()).unwrap();
        assert!(result.valid());
        for field in [
            "prompt",
            "instruction",
            "includeThread",
            "emailIds",
            "tools",
            "model",
            "provider",
            "sql",
        ] {
            let mut value = fixture["request"].clone();
            value[field] = json!("forbidden");
            assert!(serde_json::from_value::<SummaryRequest>(value).is_err());
        }
        for operation in [
            "chat",
            "generate",
            "create",
            "summarize_thread",
            "answer_with_context",
            "classify",
        ] {
            let mut value = fixture["request"].clone();
            value["operation"] = json!(operation);
            assert!(!serde_json::from_value::<SummaryRequest>(value)
                .unwrap()
                .valid());
        }
    }
    #[test]
    fn settings_contract_never_returns_keys() {
        let fixture: Value =
            serde_json::from_str(include_str!("../../contracts/ai-settings-v1.json")).unwrap();
        assert!(
            serde_json::from_value::<AISettingsRequest>(fixture["request"].clone())
                .unwrap()
                .valid()
        );
        assert!(
            serde_json::from_value::<AISettingsResponse>(fixture["response"].clone())
                .unwrap()
                .valid()
        );
        let mut leaked = fixture["response"].clone();
        leaked["apiKey"] = json!("synthetic-secret");
        assert!(serde_json::from_value::<AISettingsResponse>(leaked).is_err());
    }
}
