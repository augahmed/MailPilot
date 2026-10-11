-- Preserve legacy evidence while adding structured reasons and classifier identity.
ALTER TABLE classification_results ADD COLUMN classifier_name TEXT NOT NULL DEFAULT 'legacy';
ALTER TABLE classification_results ADD COLUMN reasons_json TEXT NOT NULL DEFAULT '["Legacy classification reason was not recorded."]'
    CHECK(json_valid(reasons_json) AND json_type(reasons_json) = 'array' AND json_array_length(reasons_json) BETWEEN 1 AND 6);
UPDATE classification_results SET reasons_json = json_array(CASE WHEN trim(explanation) = '' THEN 'Legacy classification reason was not recorded.' ELSE explanation END);

-- One complete local assessment per email, including zero predictions. Legacy
-- rows have no run/fingerprint and are retained until explicitly recomputed.
CREATE TABLE classification_runs (
    account_id TEXT NOT NULL,
    email_id TEXT PRIMARY KEY NOT NULL,
    classifier_name TEXT NOT NULL,
    classifier_version TEXT NOT NULL,
    input_hash TEXT NOT NULL,
    timestamp INTEGER NOT NULL,
    FOREIGN KEY(account_id, email_id) REFERENCES emails(account_id, id) ON DELETE CASCADE
);
