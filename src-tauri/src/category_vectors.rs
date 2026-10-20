use serde::{Deserialize, Serialize};
fn hash(s: &str) -> bool {
    s.len() == 64
        && s.bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
}
fn id(s: &str) -> bool {
    !s.is_empty() && s.len() <= 100 && !s.contains('\0')
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct CategoryVectorRequest {
    pub operation: String,
    pub account_id: String,
    pub category_id: String,
    pub email_id: String,
    pub revision: String,
    pub approved: bool,
}
impl CategoryVectorRequest {
    pub fn valid(&self) -> bool {
        id(&self.account_id)
            && id(&self.category_id)
            && match self.operation.as_str() {
                "inspect" => self.email_id.is_empty() && self.revision.is_empty() && !self.approved,
                "confirm_example" => id(&self.email_id) && hash(&self.revision) && self.approved,
                "remove_example" => {
                    id(&self.email_id) && self.revision.is_empty() && !self.approved
                }
                _ => false,
            }
    }
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ExampleChunk {
    pub chunk_id: String,
    pub section: String,
    pub text: String,
    pub context: String,
    pub truncated: bool,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct CategoryExample {
    pub email_id: String,
    pub subject: String,
    pub revision: String,
    pub status: String,
    pub chunks: Vec<ExampleChunk>,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct CategoryVectorResponse {
    pub account_id: String,
    pub category_id: String,
    pub status: String,
    pub code: String,
    pub source_hash: String,
    pub model_id: String,
    pub model_version: String,
    pub dimension: u32,
    pub recipe: String,
    pub definition_inputs: Vec<String>,
    pub examples: Vec<CategoryExample>,
}
impl CategoryVectorResponse {
    pub fn valid(&self) -> bool {
        if !self.code.is_empty() {
            return [
                "validation",
                "unauthorized",
                "unavailable",
                "not_found",
                "stale",
                "cancelled",
                "storage_error",
                "too_large",
            ]
            .contains(&self.code.as_str())
                && self.status == "unavailable"
                && self.account_id.is_empty()
                && self.category_id.is_empty()
                && self.definition_inputs.is_empty()
                && self.examples.is_empty();
        }
        id(&self.account_id)
            && id(&self.category_id)
            && ["ready", "partial", "pending", "disabled"].contains(&self.status.as_str())
            && (if ["ready", "partial"].contains(&self.status.as_str()) {
                hash(&self.source_hash)
            } else {
                self.source_hash.is_empty()
            })
            && !self.model_id.is_empty()
            && self.model_id.len() <= 128
            && !self.model_version.is_empty()
            && self.model_version.len() <= 128
            && (1..=8192).contains(&self.dimension)
            && self.recipe == "category-prototype-v1-definition-and-examples-equal-unit-mean"
            && !self.definition_inputs.is_empty()
            && self.definition_inputs.len() <= 20
            && self
                .definition_inputs
                .iter()
                .map(|s| s.len())
                .sum::<usize>()
                <= 16000
            && self.examples.len() <= 10
            && self.examples.iter().all(|e| {
                id(&e.email_id)
                    && e.subject.len() <= 120
                    && hash(&e.revision)
                    && ["included", "index_pending", "source_changed"].contains(&e.status.as_str())
                    && e.chunks.len() <= 3
                    && (e.status != "included" || !e.chunks.is_empty())
                    && e.chunks.iter().all(|c| {
                        !c.chunk_id.is_empty()
                            && c.chunk_id.len() <= 128
                            && c.section.len() <= 32
                            && c.context.len() <= 1024
                            && c.text.len() <= 160
                    })
            })
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn shared_contract_and_explicit_confirmation() {
        let fixture: serde_json::Value =
            serde_json::from_str(include_str!("../../contracts/category-vectors-v1.json")).unwrap();
        let mut request: CategoryVectorRequest =
            serde_json::from_value(fixture["request"].clone()).unwrap();
        let mut response: CategoryVectorResponse =
            serde_json::from_value(fixture["response"].clone()).unwrap();
        assert!(request.valid());
        assert!(response.valid());
        request.approved = false;
        assert!(!request.valid());
        response.examples[0].revision.clear();
        assert!(!response.valid());
        let mut bad = fixture["request"].clone();
        bad["vector"] = serde_json::json!([1]);
        assert!(serde_json::from_value::<CategoryVectorRequest>(bad).is_err());
    }
}
