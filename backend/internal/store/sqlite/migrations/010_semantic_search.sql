-- Scan only one account/model vector space in deterministic email-ID order.
-- Existing composite FKs and transactional content invalidation remain authoritative.
CREATE INDEX email_embeddings_model ON email_embeddings(account_id,provider,dimension,preprocessing_version,email_id);
