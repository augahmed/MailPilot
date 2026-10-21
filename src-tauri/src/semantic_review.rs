use serde::{Deserialize, Serialize};
pub fn hash(s: &str) -> bool {
    s.len() == 64
        && s.bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
}
#[derive(Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ReviewCategory {
    pub id: String,
    pub name: String,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct ChunkEvidence {
    pub chunk_id: String,
    pub section: String,
    pub text: String,
    pub similarity: f64,
    pub weight: f64,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct CategoryScore {
    pub category_id: String,
    pub category_name: String,
    pub score: f64,
    pub prototype_hash: String,
    pub prototype_status: String,
    pub chunk_count: u32,
    pub evidence: Vec<ChunkEvidence>,
}
#[derive(Deserialize, Serialize)]
#[serde(rename_all = "camelCase", deny_unknown_fields)]
pub struct SemanticReview {
    pub state: String,
    pub model_version: String,
    pub classifier_version: String,
    pub source_revision: String,
    pub minimum_score: f64,
    pub minimum_margin: f64,
    pub assigned: Vec<ReviewCategory>,
    pub scores: Vec<CategoryScore>,
}
impl SemanticReview {
    pub fn valid(&self) -> bool {
        ["suggested", "assigned", "unclassified"].contains(&self.state.as_str())
            && !self.model_version.is_empty()
            && self.model_version.len() <= 128
            && !self.classifier_version.is_empty()
            && self.classifier_version.len() <= 100
            && hash(&self.source_revision)
            && self.minimum_score.is_finite()
            && self.minimum_score > 0.0
            && self.minimum_score <= 1.0
            && self.minimum_margin.is_finite()
            && (0.0..=1.0).contains(&self.minimum_margin)
            && self.assigned.len() <= 1000
            && self
                .assigned
                .iter()
                .all(|c| !c.id.is_empty() && c.id.len() <= 100 && c.name.chars().count() <= 101)
            && self.scores.len() <= 100
            && self.scores.iter().all(|s| {
                !s.category_id.is_empty()
                    && s.category_id.len() <= 100
                    && s.category_name.chars().count() <= 101
                    && s.score.is_finite()
                    && (-1.0..=1.0).contains(&s.score)
                    && hash(&s.prototype_hash)
                    && ["ready", "partial"].contains(&s.prototype_status.as_str())
                    && (2..=4096).contains(&s.chunk_count)
                    && (2..=4).contains(&s.evidence.len())
                    && s.evidence.iter().all(|e| {
                        !e.chunk_id.is_empty()
                            && e.chunk_id.len() <= 128
                            && e.section.len() <= 64
                            && e.text.len() <= 160
                            && e.similarity.is_finite()
                            && (-1.0..=1.0).contains(&e.similarity)
                            && e.weight.is_finite()
                            && e.weight > 0.0
                            && e.weight <= 1.0
                    })
            })
    }
}

#[cfg(test)]
mod tests {
    use crate::bridge::{ClassificationPage, ClassificationRequest};
    #[test]
    fn local_classification_fixture_preserves_score_types() {
        let fixture: serde_json::Value =
            serde_json::from_str(include_str!("../../contracts/classification-local-v1.json"))
                .unwrap();
        let page: ClassificationPage = serde_json::from_value(fixture["response"].clone()).unwrap();
        assert!(page.valid());
        assert_eq!(page.rows[0].semantic.as_ref().unwrap().state, "suggested");
        assert!(page.rows[0].semantic.as_ref().unwrap().valid());
        assert_eq!(page.rows[3].predictions.len(), 5);
        let mut outside = fixture["response"].clone();
        outside["rows"][3]["semantic"]["scores"][4]["score"] = serde_json::json!(0.68);
        outside["rows"][3]["predictions"][4]["confidence"] = serde_json::json!(0.68);
        let invalid: ClassificationPage = serde_json::from_value(outside).unwrap();
        assert!(!invalid.valid());
        let mut request: ClassificationRequest =
            serde_json::from_value(fixture["overrideRequest"].clone()).unwrap();
        assert!(request.valid());
        request.revision.clear();
        assert!(!request.valid());
        let mut bad = fixture["response"].clone();
        bad["rows"][0]["semantic"]["vectors"] = serde_json::json!([1]);
        assert!(serde_json::from_value::<ClassificationPage>(bad).is_err());
    }
}
