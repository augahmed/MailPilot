-- Search representations are independent of single-vector classification caches.
CREATE TABLE email_passage_vectors (
 account_id TEXT NOT NULL,
 email_id TEXT NOT NULL,
 provider TEXT NOT NULL,
 dimension INTEGER NOT NULL CHECK(dimension BETWEEN 1 AND 8192),
 preprocessing_version TEXT NOT NULL,
 input_hash TEXT NOT NULL CHECK(length(input_hash)=64),
 vector BLOB NOT NULL CHECK(length(vector)=dimension*4),
 PRIMARY KEY(account_id,email_id,provider,dimension,preprocessing_version,input_hash),
 FOREIGN KEY(account_id,email_id) REFERENCES emails(account_id,id) ON DELETE CASCADE
);
CREATE TABLE email_retrieval_indexes (
 account_id TEXT NOT NULL,
 email_id TEXT NOT NULL,
 provider TEXT NOT NULL,
 dimension INTEGER NOT NULL,
 preprocessing_version TEXT NOT NULL,
 input_hash TEXT NOT NULL CHECK(length(input_hash)=64),
 PRIMARY KEY(account_id,email_id,provider,dimension,preprocessing_version),
 FOREIGN KEY(account_id,email_id) REFERENCES emails(account_id,id) ON DELETE CASCADE
);
CREATE INDEX retrieval_model_scan ON email_retrieval_indexes(account_id,provider,dimension,preprocessing_version,email_id);
