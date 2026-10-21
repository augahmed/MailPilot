use serde::{Deserialize, Serialize};

fn text(s: &str, max: usize) -> bool {
    s.len() <= max && !s.contains('\0')
}
fn hash(s: &str) -> bool {
    s.len() == 64
        && s.bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
}
fn source_id(s: &str) -> bool {
    s.len() == 2 && s.starts_with('S') && (b'1'..=b'8').contains(&s.as_bytes()[1])
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct AnswerRequest {
    pub operation: String,
    pub account_id: String,
    pub question: String,
    pub filter: crate::search::SearchFilter,
    pub job_id: String,
    pub preview_id: String,
    pub digest: String,
    pub source_ids: Vec<String>,
    pub approved: bool,
}
impl AnswerRequest {
    pub fn valid(&self) -> bool {
        if !text(&self.account_id, 100)
            || !text(&self.job_id, 32)
            || !text(&self.preview_id, 64)
            || !text(&self.digest, 64)
            || self.source_ids.len() > 8
        {
            return false;
        }
        let base =
            self.question.is_empty() && self.filter == crate::search::SearchFilter::default();
        let controls = self.job_id.is_empty()
            && self.preview_id.is_empty()
            && self.digest.is_empty()
            && self.source_ids.is_empty()
            && !self.approved;
        match self.operation.as_str() {
            "retrieve" => {
                !self.account_id.is_empty()
                    && controls
                    && text(&self.question, 2048)
                    && !self.question.trim().is_empty()
                    && self.filter.valid()
            }
            "status" => {
                base && self.preview_id.is_empty()
                    && self.digest.is_empty()
                    && self.source_ids.is_empty()
                    && !self.approved
                    && (self.job_id.is_empty() || self.job_id.len() == 32)
            }
            "clear" => !self.account_id.is_empty() && base && controls,
            "cancel" => {
                !self.account_id.is_empty()
                    && base
                    && self.job_id.len() == 32
                    && self.preview_id.is_empty()
                    && self.digest.is_empty()
                    && self.source_ids.is_empty()
                    && !self.approved
            }
            "select" => {
                !self.account_id.is_empty()
                    && base
                    && self.preview_id.len() == 64
                    && self.job_id.is_empty()
                    && self.digest.is_empty()
                    && !self.approved
                    && !self.source_ids.is_empty()
                    && self
                        .source_ids
                        .iter()
                        .enumerate()
                        .all(|(i, id)| source_id(id) && !self.source_ids[..i].contains(id))
            }
            "approve" => {
                !self.account_id.is_empty()
                    && base
                    && self.preview_id.len() == 64
                    && self.digest.len() == 64
                    && self.job_id.is_empty()
                    && self.source_ids.is_empty()
                    && self.approved
            }
            _ => false,
        }
    }
}

#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Evidence {
    pub source_id: String,
    pub email_id: String,
    pub chunk_id: String,
    pub source_revision: String,
    pub reader_revision: String,
    pub subject: String,
    pub sender: String,
    pub received_at: i64,
    pub excerpt: String,
    pub partial_content: bool,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Citation {
    pub source_id: String,
    pub quote: String,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Statement {
    pub text: String,
    pub citations: Vec<Citation>,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct AnswerResult {
    pub outcome: String,
    pub statements: Vec<Statement>,
    pub limitations: Vec<String>,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct Coverage {
    pub eligible: u64,
    pub semantic: u64,
    pub keyword: u64,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct AnswerResponse {
    pub account_id: String,
    pub state: String,
    pub job_id: String,
    pub preview_id: String,
    pub digest: String,
    pub expires_at: u64,
    pub question: String,
    pub provider: String,
    pub model: String,
    pub external: bool,
    pub policy: String,
    pub payload: String,
    pub evidence: Vec<Evidence>,
    pub selected_ids: Vec<String>,
    pub coverage: Coverage,
    pub mode: String,
    pub warning: String,
    pub exhaustive: bool,
    pub aggregate: bool,
    pub result: Option<AnswerResult>,
    pub code: String,
}
impl AnswerResponse {
    pub fn valid(&self) -> bool {
        text(&self.account_id, 100)
            && text(&self.job_id, 32)
            && text(&self.preview_id, 64)
            && text(&self.digest, 64)
            && text(&self.question, 2048)
            && text(&self.provider, 64)
            && text(&self.model, 128)
            && text(&self.policy, 4096)
            && text(&self.payload, 12288)
            && text(&self.code, 64)
            && !self.exhaustive
            && [
                "idle",
                "retrieving",
                "preview",
                "running",
                "completed",
                "error",
            ]
            .contains(&self.state.as_str())
            && ["", "keyword", "hybrid"].contains(&self.mode.as_str())
            && ["", "semantic_unavailable"].contains(&self.warning.as_str())
            && self.coverage.semantic <= self.coverage.eligible
            && self.coverage.keyword <= self.coverage.eligible
            && self.evidence.len() <= 8
            && self.evidence.iter().enumerate().all(|(i, e)| {
                source_id(&e.source_id)
                    && !self.evidence[..i]
                        .iter()
                        .any(|v| v.source_id == e.source_id || v.chunk_id == e.chunk_id)
                    && !e.email_id.is_empty()
                    && text(&e.email_id, 100)
                    && !e.chunk_id.is_empty()
                    && text(&e.chunk_id, 128)
                    && hash(&e.source_revision)
                    && hash(&e.reader_revision)
                    && text(&e.subject, 120)
                    && text(&e.sender, 120)
                    && !e.excerpt.trim().is_empty()
                    && text(&e.excerpt, 600)
            })
            && self.selected_ids.len() <= 8
            && self.selected_ids.iter().enumerate().all(|(i, id)| {
                !self.selected_ids[..i].contains(id)
                    && self.evidence.iter().any(|s| s.source_id == *id)
            })
            && (self.state != "preview"
                || (!self.account_id.is_empty()
                    && hash(&self.preview_id)
                    && hash(&self.digest)
                    && self.expires_at > 0
                    && !self.policy.is_empty()
                    && !self.payload.is_empty()
                    && !self.selected_ids.is_empty()
                    && self.result.is_none()
                    && self.code.is_empty()))
            && (self.evidence.is_empty()
                || (self.code.is_empty()
                    && !self.account_id.is_empty()
                    && ["preview", "running", "completed"].contains(&self.state.as_str())))
            && self.result.as_ref().is_none_or(|r| {
                self.state == "completed"
                    && self.code.is_empty()
                    && ["answered", "insufficient", "conflicting"].contains(&r.outcome.as_str())
                    && r.statements.len() <= 6
                    && r.limitations.len() <= 6
                    && (r.outcome == "insufficient" || !r.statements.is_empty())
                    && (r.outcome != "insufficient" || !r.limitations.is_empty())
                    && (!self.aggregate || r.outcome == "insufficient")
                    && r.limitations
                        .iter()
                        .all(|s| !s.trim().is_empty() && text(s, 300))
                    && r.statements.iter().all(|s| {
                        !s.text.trim().is_empty()
                            && text(&s.text, 800)
                            && !s.citations.is_empty()
                            && s.citations.len() <= 3
                            && s.citations.iter().all(|c| {
                                !c.quote.is_empty()
                                    && text(&c.quote, 300)
                                    && self.selected_ids.contains(&c.source_id)
                                    && self.evidence.iter().any(|e| {
                                        e.source_id == c.source_id && e.excerpt.contains(&c.quote)
                                    })
                            })
                    })
            })
            && serde_json::to_vec(self).is_ok_and(|v| v.len() <= 48 * 1024)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn shared_answer_contract_has_no_tools_and_validates_citations() {
        let f: serde_json::Value =
            serde_json::from_str(include_str!("../../contracts/email-answer-v1.json")).unwrap();
        for key in ["request", "approve"] {
            assert!(serde_json::from_value::<AnswerRequest>(f[key].clone())
                .unwrap()
                .valid());
        }
        for key in ["preview", "response"] {
            assert!(serde_json::from_value::<AnswerResponse>(f[key].clone())
                .unwrap()
                .valid());
        }
        let mut request = f["request"].clone();
        request["tools"] = serde_json::json!(["gmail"]);
        assert!(serde_json::from_value::<AnswerRequest>(request).is_err());
        let mut response: AnswerResponse = serde_json::from_value(f["response"].clone()).unwrap();
        response.result.as_mut().unwrap().statements[0].citations[0].source_id = "S9".into();
        assert!(!response.valid());
    }
}
