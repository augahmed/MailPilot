use serde::{Deserialize, Serialize};

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct IndexRequest {
    pub operation: String,
    pub account_id: String,
}
impl IndexRequest {
    pub fn valid(&self) -> bool {
        self.account_id.len() <= 100
            && (self.operation == "status"
                || (!self.account_id.is_empty()
                    && ["start", "pause", "resume", "cancel", "rebuild"]
                        .contains(&self.operation.as_str())))
    }
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct IndexResponse {
    pub account_id: String,
    pub status: String,
    pub generation: String,
    pub model_id: String,
    pub model_version: String,
    pub fingerprint: String,
    pub dimension: u32,
    pub eligible_messages: u64,
    pub indexed_messages: u64,
    pub complete_messages: u64,
    pub partial_messages: u64,
    pub failed_messages: u64,
    pub pending_messages: u64,
    pub unavailable_messages: u64,
    pub chunks_created: u64,
    pub embeddings_generated: u64,
    pub code: String,
}
impl IndexResponse {
    pub fn valid(&self) -> bool {
        let counts = [
            self.complete_messages,
            self.partial_messages,
            self.failed_messages,
            self.pending_messages,
            self.unavailable_messages,
        ];
        let total = counts
            .iter()
            .try_fold(0u64, |sum, value| sum.checked_add(*value));
        self.account_id.len() <= 100
            && self.generation.len() <= 128
            && self.model_id.len() <= 128
            && self.model_version.len() <= 128
            && self.dimension <= 8192
            && (self.account_id.is_empty()
                || (self.dimension > 0
                    && self.fingerprint.len() == 64
                    && self
                        .fingerprint
                        .bytes()
                        .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))))
            && [
                "idle",
                "pending",
                "running",
                "paused",
                "cancelled",
                "interrupted",
                "complete",
                "partial",
                "failed",
            ]
            .contains(&self.status.as_str())
            && [
                "",
                "unauthorized",
                "validation",
                "unavailable",
                "busy",
                "storage_error",
                "interrupted",
                "cache_changed",
                "model_changed",
                "preprocessing_changed",
                "model_unavailable",
                "inference_failed",
                "pending_changes",
            ]
            .contains(&self.code.as_str())
            && total == Some(self.eligible_messages)
            && self.indexed_messages <= self.complete_messages.saturating_add(self.partial_messages)
            && self.embeddings_generated <= self.chunks_created
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn shared_contract_rejects_paths_and_inconsistent_progress() {
        let fixture: serde_json::Value =
            serde_json::from_str(include_str!("../../contracts/local-index-v1.json")).unwrap();
        let request: IndexRequest = serde_json::from_value(fixture["request"].clone()).unwrap();
        assert!(request.valid());
        let response: IndexResponse = serde_json::from_value(fixture["response"].clone()).unwrap();
        assert!(response.valid());
        let mut bad = fixture["request"].clone();
        bad["modelPath"] = serde_json::json!("/tmp/model");
        assert!(serde_json::from_value::<IndexRequest>(bad).is_err());
        let mut response = response;
        response.code = "preprocessing_changed".into();
        assert!(response.valid());
        response.pending_messages = 0;
        assert!(!response.valid());
    }
}
