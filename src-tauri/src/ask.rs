use serde::{Deserialize, Serialize};
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct AskRequest {
    pub operation: String,
    pub account_id: String,
    pub question: String,
    pub sender: String,
    pub from: Option<i64>,
    pub until: Option<i64>,
    pub preview_id: String,
    pub job_id: String,
    pub approved: bool,
}
impl AskRequest {
    pub fn valid(&self) -> bool {
        if [&self.account_id, &self.preview_id, &self.job_id]
            .iter()
            .any(|v| v.len() > 100 || v.contains(['\0', '\r', '\n']))
            || self.question.len() > 1000
            || self.sender.len() > 256
            || self.question.contains('\0')
            || self.sender.contains('\0')
            || [self.from, self.until]
                .into_iter()
                .flatten()
                .any(|v| !(0..=253402300800000).contains(&v))
            || matches!((self.from,self.until),(Some(a),Some(b)) if a>=b)
        {
            return false;
        }
        let selection = self.question.is_empty()
            && self.sender.is_empty()
            && self.from.is_none()
            && self.until.is_none();
        let control = self.preview_id.is_empty() && self.job_id.is_empty() && !self.approved;
        if self.operation == "status" {
            return selection && control;
        }
        if self.account_id.is_empty() {
            return false;
        }
        match self.operation.as_str() {
            "prepare" => !self.question.trim().is_empty() && control,
            "generate" => {
                selection && !self.preview_id.is_empty() && self.job_id.is_empty() && self.approved
            }
            "clear" => selection && control,
            "cancel" => {
                selection && !self.job_id.is_empty() && self.preview_id.is_empty() && !self.approved
            }
            _ => false,
        }
    }
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Source {
    pub r#ref: String,
    pub email_id: String,
    pub subject: String,
    pub sender: String,
    pub received_at: i64,
    pub evidence: String,
}
#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Claim {
    pub text: String,
    pub source: String,
    pub quote: String,
}
#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Answer {
    pub insufficient: bool,
    pub claims: Vec<Claim>,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct AskResponse {
    pub account_id: String,
    pub state: String,
    pub job_id: String,
    pub preview_id: String,
    pub expires_at: i64,
    pub provider: String,
    pub model: String,
    pub external: bool,
    pub context: String,
    pub instruction: String,
    pub policy: String,
    pub version: String,
    pub partial: bool,
    pub truncated: bool,
    pub sources: Vec<Source>,
    pub answer: Answer,
    pub code: String,
}
impl AskResponse {
    pub fn valid(&self) -> bool {
        [
            "idle",
            "retrieving",
            "preview",
            "generating",
            "answered",
            "error",
        ]
        .contains(&self.state.as_str())
            && [&self.account_id, &self.job_id, &self.preview_id]
                .iter()
                .all(|v| v.len() <= 100)
            && self.context.len() <= 8000
            && self.instruction.len() <= 1000
            && self.policy.len() <= 2000
            && self.provider.len() <= 64
            && self.model.len() <= 128
            && self.version == "ask-context-v1"
            && self.code.len() <= 64
            && self.expires_at >= 0
            && self.sources.len() <= 5
            && self.sources.iter().all(|s| {
                s.r#ref.len() <= 3
                    && s.email_id.len() <= 100
                    && s.subject.len() <= 256
                    && s.sender.len() <= 320
                    && s.evidence.len() <= 1400
            })
            && self.answer.claims.len() <= 6
            && (!self.answer.insufficient || self.answer.claims.is_empty())
            && self.answer.claims.iter().all(|c| {
                c.text.len() <= 1000
                    && (8..=700).contains(&c.quote.len())
                    && self
                        .sources
                        .iter()
                        .any(|s| s.r#ref == c.source && s.evidence.contains(&c.quote))
            })
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn contract_and_consent() {
        let fixture: serde_json::Value =
            serde_json::from_str(include_str!("../../contracts/ask-v1.json")).unwrap();
        let mut r: AskRequest = serde_json::from_value(fixture["request"].clone()).unwrap();
        assert!(r.valid());
        r.operation = "generate".into();
        r.question = "".into();
        r.preview_id = "preview".into();
        assert!(!r.valid());
        r.approved = true;
        assert!(r.valid());
        let response: AskResponse = serde_json::from_value(fixture["response"].clone()).unwrap();
        assert!(response.valid());
        assert_eq!(serde_json::to_value(response).unwrap(), fixture["response"]);
    }
}
