-- One durable local job; chunks/vectors remain in the shared 015/016 store.
CREATE TABLE local_index_jobs (
 account_id TEXT PRIMARY KEY NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 generation TEXT NOT NULL CHECK(length(generation)>0),
 model_id TEXT NOT NULL,
 model_version TEXT NOT NULL,
 dimension INTEGER NOT NULL CHECK(dimension BETWEEN 1 AND 8192),
 preprocessing_version TEXT NOT NULL,
 index_version TEXT NOT NULL,
 fingerprint TEXT NOT NULL CHECK(length(fingerprint)=64),
 status TEXT NOT NULL CHECK(status IN ('running','paused','cancelled','interrupted','complete','partial','failed')),
 code TEXT NOT NULL DEFAULT '',
 updated_at INTEGER NOT NULL
);
-- Pin each account/model recipe across A -> B -> A transitions as well.
CREATE TABLE local_index_models (
 account_id TEXT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 model_id TEXT NOT NULL,
 model_version TEXT NOT NULL,
 dimension INTEGER NOT NULL CHECK(dimension BETWEEN 1 AND 8192),
 fingerprint TEXT NOT NULL CHECK(length(fingerprint)=64),
 PRIMARY KEY(account_id,model_id,model_version)
);
ALTER TABLE index_state ADD COLUMN attempt_generation TEXT NOT NULL DEFAULT '';
ALTER TABLE index_state ADD COLUMN failure_code TEXT NOT NULL DEFAULT '';

CREATE INDEX live_cached_emails ON emails(account_id,id) WHERE remote_deleted=0;

-- Source changes permit retry; unchanged bad messages are attempted once per run.
CREATE TRIGGER index_attempt_source_changed AFTER UPDATE OF content_hash,body_state ON emails
WHEN OLD.content_hash<>NEW.content_hash OR OLD.body_state<>NEW.body_state
BEGIN
 UPDATE index_state SET attempt_generation='',failure_code=''
 WHERE account_id=NEW.account_id AND email_id=NEW.id;
END;

-- A cache removal revokes in-flight publication before rows can be reimported.
-- Historical journal-retained tombstones remain intact.
CREATE TRIGGER index_cache_deleted BEFORE DELETE ON emails
BEGIN
 UPDATE local_index_jobs SET status='paused',code='cache_changed',generation=lower(hex(randomblob(16)))
 WHERE account_id=OLD.account_id;
END;
CREATE TRIGGER index_cache_tombstoned AFTER UPDATE OF remote_deleted ON emails
WHEN OLD.remote_deleted=0 AND NEW.remote_deleted=1
BEGIN
 UPDATE local_index_jobs SET status='paused',code='cache_changed',generation=lower(hex(randomblob(16)))
 WHERE account_id=NEW.account_id;
END;
