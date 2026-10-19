use serde::{Deserialize, Serialize};

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ReplyRequest {
    pub operation: String,
    pub account_id: String,
    pub job_id: String,
    pub action_id: String,
    pub approved: bool,
}
impl ReplyRequest {
    pub fn valid(&self) -> bool {
        if [&self.account_id, &self.job_id, &self.action_id]
            .iter()
            .any(|v| v.len() > 100 || v.contains(['\0', '\r', '\n']))
        {
            return false;
        }
        match self.operation.as_str() {
            "status" => self.job_id.is_empty() && self.action_id.is_empty() && !self.approved,
            "reconcile" => {
                !self.account_id.is_empty()
                    && self.job_id.is_empty()
                    && !self.action_id.is_empty()
                    && !self.approved
            }
            "resolve" => {
                !self.account_id.is_empty()
                    && self.job_id.is_empty()
                    && !self.action_id.is_empty()
                    && self.approved
            }
            "cancel" => {
                !self.account_id.is_empty()
                    && !self.job_id.is_empty()
                    && self.action_id.is_empty()
                    && !self.approved
            }
            _ => false,
        }
    }
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ReplyResponse {
    pub account_id: String,
    pub state: String,
    pub job_id: String,
    pub to: String,
    pub subject: String,
    pub action_id: String,
    pub draft_id: String,
    pub reconciled: bool,
    pub code: String,
}
impl ReplyResponse {
    pub fn valid(&self) -> bool {
        [
            "idle",
            "draft_preview",
            "reconciling",
            "created",
            "uncertain",
            "conflict",
            "resolved",
            "error",
        ]
        .contains(&self.state.as_str())
            && self.to.len() <= 320
            && self.subject.len() <= 512
            && self.code.len() <= 64
            && self.draft_id.len() <= 1024
            && [&self.account_id, &self.job_id, &self.action_id]
                .iter()
                .all(|v| v.len() <= 100)
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    #[test]
    fn recovery_exposes_no_ai_settings_or_creation() {
        let base =
            json!({"operation":"status","accountId":"a","jobId":"","actionId":"","approved":false});
        assert!(serde_json::from_value::<ReplyRequest>(base.clone())
            .unwrap()
            .valid());
        for operation in [
            "configure",
            "disable",
            "prepare",
            "generate",
            "preview_draft",
            "create",
            "summarize_thread",
            "chat",
            "classify",
        ] {
            let mut value = base.clone();
            value["operation"] = json!(operation);
            assert!(!serde_json::from_value::<ReplyRequest>(value)
                .unwrap()
                .valid());
        }
        for field in [
            "model",
            "apiKey",
            "instruction",
            "includeThread",
            "body",
            "emailId",
            "previewId",
        ] {
            let mut value = base.clone();
            value[field] = json!("forbidden");
            assert!(serde_json::from_value::<ReplyRequest>(value).is_err());
        }
    }
}
