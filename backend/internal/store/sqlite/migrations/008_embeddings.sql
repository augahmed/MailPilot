-- Local derived data; deliberately no similarity index or external model.
CREATE TABLE email_embeddings (
    account_id TEXT NOT NULL,
    email_id TEXT NOT NULL,
    provider TEXT NOT NULL CHECK(length(provider) BETWEEN 1 AND 256),
    dimension INTEGER NOT NULL CHECK(dimension BETWEEN 1 AND 8192),
    preprocessing_version TEXT NOT NULL,
    input_hash TEXT NOT NULL CHECK(length(input_hash) = 64),
    vector BLOB NOT NULL CHECK(length(vector) = dimension * 4),
    PRIMARY KEY(account_id, email_id, provider, dimension, preprocessing_version),
    FOREIGN KEY(account_id, email_id) REFERENCES emails(account_id, id) ON DELETE CASCADE
);
