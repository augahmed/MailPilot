CREATE TABLE category_embeddings (
    account_id TEXT NOT NULL,
    category_id TEXT NOT NULL,
    provider TEXT NOT NULL,
    dimension INTEGER NOT NULL CHECK(dimension BETWEEN 1 AND 8192),
    preprocessing_version TEXT NOT NULL,
    input_hash TEXT NOT NULL CHECK(length(input_hash)=64),
    vector BLOB NOT NULL CHECK(length(vector)=dimension*4),
    PRIMARY KEY(account_id,category_id,provider,dimension,preprocessing_version),
    FOREIGN KEY(account_id,category_id) REFERENCES categories(account_id,id) ON DELETE CASCADE
);
CREATE TRIGGER invalidate_category_embeddings AFTER UPDATE OF description,enabled ON categories
WHEN OLD.description<>NEW.description OR OLD.enabled<>NEW.enabled
BEGIN
    DELETE FROM category_embeddings WHERE account_id=NEW.account_id AND category_id=NEW.id;
END;
ALTER TABLE classification_runs ADD COLUMN used_classifier TEXT NOT NULL DEFAULT '';
ALTER TABLE classification_runs ADD COLUMN reason TEXT NOT NULL DEFAULT '';
ALTER TABLE classification_runs ADD COLUMN candidates_json TEXT NOT NULL DEFAULT '[]'
    CHECK(json_valid(candidates_json) AND json_type(candidates_json)='array' AND json_array_length(candidates_json)<=5);
-- A cosine score is not a probability. Retain that distinction in stored results.
ALTER TABLE classification_results ADD COLUMN score_kind TEXT NOT NULL DEFAULT ''
    CHECK(score_kind IN ('','rule_confidence','cosine_similarity'));
