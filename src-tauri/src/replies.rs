use serde::{Deserialize, Serialize};

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ReplyRequest {
    pub operation: String,
    pub account_id: String,
    pub email_id: String,
    pub include_thread: bool,
    pub instruction: String,
    pub tone: String,
    pub preview_id: String,
    pub job_id: String,
    pub action_id: String,
    pub approved: bool,
    pub body: String,
    pub model: String,
    pub api_key: String,
}
impl ReplyRequest {
    pub fn valid(&self) -> bool {
        if [
            &self.account_id,
            &self.email_id,
            &self.preview_id,
            &self.job_id,
            &self.action_id,
        ]
        .iter()
        .any(|v| v.len() > 100 || v.contains(['\0', '\r', '\n']))
            || self.instruction.len() > 1000
            || self.body.len() > 6000
            || self.model.len() > 128
            || self.api_key.len() > 2048
            || [&self.instruction, &self.body, &self.model]
                .iter()
                .any(|v| v.contains('\0'))
        {
            return false;
        }
        let selection = self.email_id.is_empty()
            && !self.include_thread
            && self.instruction.is_empty()
            && self.tone.is_empty();
        let control = self.preview_id.is_empty()
            && self.job_id.is_empty()
            && self.action_id.is_empty()
            && !self.approved
            && self.body.is_empty();
        let config = self.model.is_empty() && self.api_key.is_empty();
        if self.operation == "status" {
            return selection && control && config;
        }
        if self.account_id.is_empty() {
            return false;
        }
        match self.operation.as_str() {
            "configure" => selection && control && !self.model.is_empty(),
            "disable" => selection && control && config,
            "prepare" => {
                !self.email_id.is_empty()
                    && ["Professional", "Friendly", "Concise", "Formal"]
                        .contains(&self.tone.as_str())
                    && control
                    && config
            }
            "generate" => {
                selection
                    && config
                    && !self.preview_id.is_empty()
                    && self.approved
                    && self.job_id.is_empty()
                    && self.action_id.is_empty()
                    && self.body.is_empty()
            }
            "preview_draft" => {
                selection
                    && config
                    && !self.preview_id.is_empty()
                    && !self.approved
                    && self.job_id.is_empty()
                    && self.action_id.is_empty()
                    && !self.body.trim().is_empty()
            }
            "create" => {
                selection
                    && config
                    && !self.preview_id.is_empty()
                    && !self.action_id.is_empty()
                    && self.approved
                    && self.job_id.is_empty()
                    && self.body.is_empty()
            }
            "reconcile" | "undo_preview" => {
                selection
                    && config
                    && !self.action_id.is_empty()
                    && self.preview_id.is_empty()
                    && self.job_id.is_empty()
                    && !self.approved
                    && self.body.is_empty()
            }
            "resolve" => {
                selection
                    && config
                    && !self.action_id.is_empty()
                    && self.preview_id.is_empty()
                    && self.job_id.is_empty()
                    && self.approved
                    && self.body.is_empty()
            }
            "cancel" => {
                selection
                    && config
                    && !self.job_id.is_empty()
                    && self.preview_id.is_empty()
                    && self.action_id.is_empty()
                    && !self.approved
                    && self.body.is_empty()
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
    pub configured: bool,
    pub provider: String,
    pub model: String,
    pub external: bool,
    pub job_id: String,
    pub preview_id: String,
    pub review_id: String,
    pub expires_at: i64,
    pub context: String,
    pub instruction: String,
    pub policy: String,
    pub to: String,
    pub subject: String,
    pub body: String,
    pub source_count: u32,
    pub truncated: bool,
    pub action_id: String,
    pub draft_id: String,
    pub reconciled: bool,
    pub undo_available: bool,
    pub code: String,
}
impl ReplyResponse {
    pub fn valid(&self) -> bool {
        [
            "idle",
            "configuring",
            "ai_preview",
            "generating",
            "editing",
            "draft_preview",
            "creating",
            "reconciling",
            "created",
            "uncertain",
            "conflict",
            "resolved",
            "error",
        ]
        .contains(&self.state.as_str())
            && self.context.len() <= 6000
            && self.instruction.len() <= 1500
            && self.policy.len() <= 1000
            && self.body.len() <= 6000
            && self.subject.len() <= 512
            && self.to.len() <= 320
            && self.source_count <= 5
            && !self.undo_available
            && self.expires_at >= 0
            && self.code.len() <= 64
            && self.model.len() <= 128
            && self.provider.len() <= 64
            && [
                &self.account_id,
                &self.job_id,
                &self.preview_id,
                &self.review_id,
                &self.action_id,
            ]
            .iter()
            .all(|v| v.len() <= 100)
            && self.draft_id.len() <= 1024
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::{json, Value};
    #[test]
    fn reply_contract_rejects_secret_responses_and_unapproved_writes() {
        let fixture: Value =
            serde_json::from_str(include_str!("../../contracts/reply-drafting-v1.json")).unwrap();
        let req: ReplyRequest = serde_json::from_value(fixture["request"].clone()).unwrap();
        assert!(req.valid());
        let result: ReplyResponse = serde_json::from_value(fixture["response"].clone()).unwrap();
        assert!(result.valid());
        assert_eq!(serde_json::to_value(result).unwrap(), fixture["response"]);
        for change in [
            json!({"operation":"send"}),
            json!({"tone":"Rude"}),
            json!({"accountId":""}),
            json!({"apiKey":"synthetic-secret"}),
            json!({"body":"changed body"}),
        ] {
            let mut bad = fixture["request"].clone();
            for (k, v) in change.as_object().unwrap() {
                bad[k] = v.clone();
            }
            let req: ReplyRequest = serde_json::from_value(bad).unwrap();
            assert!(!req.valid());
        }
        let mut bad = fixture["response"].clone();
        bad["apiKey"] = json!("synthetic");
        assert!(serde_json::from_value::<ReplyResponse>(bad).is_err());
        let mut create = fixture["request"].clone();
        create["operation"] = json!("create");
        create["emailId"] = json!("");
        create["tone"] = json!("");
        create["actionId"] = json!("a");
        create["previewId"] = json!("digest");
        create["approved"] = json!(true);
        assert!(serde_json::from_value::<ReplyRequest>(create.clone())
            .unwrap()
            .valid());
        create["approved"] = json!(false);
        assert!(!serde_json::from_value::<ReplyRequest>(create)
            .unwrap()
            .valid());
    }
}
