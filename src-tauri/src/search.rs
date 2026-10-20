use serde::{Deserialize, Serialize};

#[derive(Default, Deserialize, Serialize, PartialEq)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct SearchFilter {
    pub sender: String,
    pub after: u64,
    pub before: u64,
    pub category_id: String,
}
impl SearchFilter {
    fn valid(&self) -> bool {
        self.sender.len() <= 200
            && !self.sender.contains('\0')
            && self.category_id.len() <= 100
            && !self.category_id.contains('\0')
            && self.after <= 253402300799999
            && self.before <= 253402300799999
            && (self.before == 0 || self.after < self.before)
    }
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct SearchRequest {
    pub operation: String,
    pub account_id: String,
    pub job_id: String,
    pub query: String,
    pub limit: u32,
    pub filter: SearchFilter,
}
impl SearchRequest {
    pub fn valid(&self) -> bool {
        self.account_id.len() <= 100
            && self.job_id.len() <= 32
            && match self.operation.as_str() {
                "start" => {
                    !self.account_id.is_empty()
                        && self.job_id.is_empty()
                        && !self.query.trim().is_empty()
                        && self.query.len() <= 2048
                        && !self.query.contains('\0')
                        && (1..=20).contains(&self.limit)
                        && self.filter.valid()
                }
                "status" | "cancel" => {
                    self.query.is_empty()
                        && self.limit == 0
                        && self.filter == SearchFilter::default()
                        && (self.job_id.is_empty() || self.job_id.len() == 32)
                        && (self.operation == "status"
                            || (!self.account_id.is_empty() && !self.job_id.is_empty()))
                }
                _ => false,
            }
    }
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Evidence {
    pub chunk_id: String,
    pub score: f64,
    pub excerpt: String,
    pub source_revision: String,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct EmailResult {
    pub reader_revision: String,
    pub email_id: String,
    pub subject: String,
    pub sender: String,
    pub received_at: i64,
    pub evidence: Vec<Evidence>,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct SearchResponse {
    pub account_id: String,
    pub job_id: String,
    pub status: String,
    pub code: String,
    pub emails: Vec<EmailResult>,
    pub scanned_chunks: u64,
    pub elapsed_ms: f64,
    pub searchable_messages: u64,
    pub eligible_messages: u64,
}
impl SearchResponse {
    pub fn valid(&self) -> bool {
        self.account_id.len() <= 100
            && (self.job_id.is_empty() || self.job_id.len() == 32)
            && ["idle", "running", "complete", "cancelled", "error"].contains(&self.status.as_str())
            && [
                "",
                "unauthorized",
                "validation",
                "unavailable",
                "busy",
                "storage_error",
                "model_unavailable",
                "inference_failed",
                "stale",
                "cancelled",
                "timeout",
                "too_large",
            ]
            .contains(&self.code.as_str())
            && self.elapsed_ms.is_finite()
            && self.elapsed_ms >= 0.0
            && self.searchable_messages <= self.eligible_messages
            && self.emails.len() <= 20
            && (self.emails.is_empty()
                || (self.status == "complete"
                    && self.code.is_empty()
                    && !self.account_id.is_empty()))
            && self.emails.iter().all(|e| {
                e.reader_revision.len() == 64
                    && e.reader_revision
                        .bytes()
                        .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
                    && !e.email_id.is_empty()
                    && e.email_id.len() <= 100
                    && e.subject.len() <= 240
                    && e.sender.len() <= 240
                    && !e.evidence.is_empty()
                    && e.evidence.len() <= 3
                    && e.evidence.iter().all(|v| {
                        !v.chunk_id.is_empty()
                            && v.chunk_id.len() <= 128
                            && v.score.is_finite()
                            && (-1.0..=1.0).contains(&v.score)
                            && !v.excerpt.is_empty()
                            && v.excerpt.len() <= 600
                            && v.source_revision.len() == 64
                            && v.source_revision
                                .bytes()
                                .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
                    })
            })
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn shared_search_contract() {
        let fixture: serde_json::Value =
            serde_json::from_str(include_str!("../../contracts/vector-search-v1.json")).unwrap();
        let request: SearchRequest = serde_json::from_value(fixture["request"].clone()).unwrap();
        let response: SearchResponse = serde_json::from_value(fixture["response"].clone()).unwrap();
        assert!(request.valid());
        assert!(response.valid());
        let mut bad = fixture["request"].clone();
        bad["modelPath"] = serde_json::json!("/tmp/no");
        assert!(serde_json::from_value::<SearchRequest>(bad).is_err());
        let mut response = response;
        response.emails[0].evidence[0].score = f64::NAN;
        assert!(!response.valid());
    }
}
