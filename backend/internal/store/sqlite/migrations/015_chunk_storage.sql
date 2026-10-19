-- Active shared chunk storage. Historical vector tables remain inert and intact.
CREATE TABLE email_chunks (
 id TEXT PRIMARY KEY NOT NULL,
 account_id TEXT NOT NULL,
 email_id TEXT NOT NULL,
 chunk_index INTEGER NOT NULL CHECK(chunk_index >= 0),
 section_type TEXT NOT NULL CHECK(length(section_type) BETWEEN 1 AND 64),
 text TEXT NOT NULL CHECK(length(text) > 0),
 token_count INTEGER NOT NULL CHECK(token_count > 0),
 source_revision TEXT NOT NULL CHECK(length(source_revision)=64 AND source_revision NOT GLOB '*[^0-9a-f]*'),
 start_offset INTEGER NOT NULL CHECK(start_offset >= 0),
 end_offset INTEGER NOT NULL CHECK(end_offset > start_offset),
 content_hash TEXT NOT NULL CHECK(length(content_hash)=64 AND content_hash NOT GLOB '*[^0-9a-f]*'),
 preprocessing_version TEXT NOT NULL CHECK(length(preprocessing_version) BETWEEN 1 AND 128),
 UNIQUE(account_id,id),
 UNIQUE(account_id,email_id,chunk_index),
 FOREIGN KEY(account_id,email_id) REFERENCES emails(account_id,id) ON DELETE CASCADE,
 CHECK(end_offset-start_offset=length(CAST(text AS BLOB)))
);

CREATE TABLE chunk_embeddings (
 account_id TEXT NOT NULL,
 chunk_id TEXT NOT NULL,
 model_id TEXT NOT NULL CHECK(length(model_id) BETWEEN 1 AND 128),
 model_version TEXT NOT NULL CHECK(length(model_version) BETWEEN 1 AND 128),
 dimension INTEGER NOT NULL CHECK(dimension BETWEEN 1 AND 8192),
 vector_data BLOB,
 status TEXT NOT NULL CHECK(status IN ('pending','ready','failed')),
 PRIMARY KEY(account_id,chunk_id,model_id,model_version),
 FOREIGN KEY(account_id,chunk_id) REFERENCES email_chunks(account_id,id) ON DELETE CASCADE,
 CHECK((status='ready' AND typeof(vector_data)='blob' AND length(vector_data)=dimension*4)
    OR (status IN ('pending','failed') AND vector_data IS NULL))
);

CREATE TABLE index_state (
 account_id TEXT NOT NULL,
 email_id TEXT NOT NULL,
 source_revision TEXT NOT NULL CHECK(length(source_revision)=64 AND source_revision NOT GLOB '*[^0-9a-f]*'),
 preprocessing_version TEXT NOT NULL CHECK(length(preprocessing_version) BETWEEN 1 AND 128),
 index_version TEXT NOT NULL CHECK(length(index_version) BETWEEN 1 AND 128),
 model_id TEXT NOT NULL CHECK(length(model_id) BETWEEN 1 AND 128),
 model_version TEXT NOT NULL CHECK(length(model_version) BETWEEN 1 AND 128),
 dimension INTEGER NOT NULL CHECK(dimension BETWEEN 1 AND 8192),
 generation TEXT NOT NULL CHECK(length(generation)>0),
 body_state TEXT NOT NULL CHECK(body_state IN ('','complete','unavailable')),
 status TEXT NOT NULL CHECK(status IN ('pending','partial','ready','unavailable','stale')),
 last_indexed_at INTEGER,
 PRIMARY KEY(account_id,email_id),
 FOREIGN KEY(account_id,email_id) REFERENCES emails(account_id,id) ON DELETE CASCADE
);

-- Keep old chunks private as a reuse cache until the next atomic replacement.
-- No stale generation can be read as active or receive embedding results.
CREATE TRIGGER chunks_source_changed AFTER UPDATE OF content_hash,body_state,remote_deleted ON emails
WHEN NEW.remote_deleted=0 AND (OLD.content_hash<>NEW.content_hash OR OLD.body_state<>NEW.body_state)
BEGIN
 UPDATE index_state SET source_revision=NEW.content_hash, body_state=NEW.body_state,
  status='stale', generation=lower(hex(randomblob(16))), last_indexed_at=NULL
 WHERE account_id=NEW.account_id AND email_id=NEW.id;
END;

CREATE TRIGGER chunks_source_deleted AFTER UPDATE OF remote_deleted ON emails
WHEN NEW.remote_deleted=1
BEGIN
 DELETE FROM email_chunks WHERE account_id=NEW.account_id AND email_id=NEW.id;
 DELETE FROM index_state WHERE account_id=NEW.account_id AND email_id=NEW.id;
END;

CREATE TRIGGER chunks_current_insert BEFORE INSERT ON email_chunks
WHEN NOT EXISTS (SELECT 1 FROM emails WHERE account_id=NEW.account_id AND id=NEW.email_id
 AND content_hash=NEW.source_revision AND remote_deleted=0)
BEGIN SELECT RAISE(ABORT,'stale chunk source'); END;

CREATE TRIGGER chunks_current_update BEFORE UPDATE ON email_chunks
WHEN NOT EXISTS (SELECT 1 FROM emails WHERE account_id=NEW.account_id AND id=NEW.email_id
 AND content_hash=NEW.source_revision AND remote_deleted=0)
BEGIN SELECT RAISE(ABORT,'stale chunk source'); END;

CREATE TRIGGER chunks_identity_immutable BEFORE UPDATE OF id,account_id,email_id ON email_chunks
WHEN OLD.id<>NEW.id OR OLD.account_id<>NEW.account_id OR OLD.email_id<>NEW.email_id
BEGIN SELECT RAISE(ABORT,'immutable chunk identity'); END;

-- A changed embedding input can never retain its previous cached vectors.
CREATE TRIGGER chunks_input_changed AFTER UPDATE OF text,content_hash,section_type,token_count,preprocessing_version ON email_chunks
WHEN OLD.text<>NEW.text OR OLD.content_hash<>NEW.content_hash OR OLD.section_type<>NEW.section_type
 OR OLD.token_count<>NEW.token_count OR OLD.preprocessing_version<>NEW.preprocessing_version
BEGIN
 DELETE FROM chunk_embeddings WHERE account_id=OLD.account_id AND chunk_id=OLD.id;
END;

-- Defensive invalidation also covers maintenance SQL, not just repository writes.
CREATE TRIGGER chunks_updated AFTER UPDATE ON email_chunks
WHEN OLD.source_revision<>NEW.source_revision OR OLD.text<>NEW.text OR OLD.content_hash<>NEW.content_hash
 OR OLD.section_type<>NEW.section_type OR OLD.token_count<>NEW.token_count
 OR OLD.preprocessing_version<>NEW.preprocessing_version OR OLD.chunk_index<>NEW.chunk_index
 OR OLD.start_offset<>NEW.start_offset OR OLD.end_offset<>NEW.end_offset
BEGIN
 UPDATE index_state SET status='stale', generation=lower(hex(randomblob(16))), last_indexed_at=NULL
 WHERE account_id=OLD.account_id AND email_id=OLD.email_id;
END;
CREATE TRIGGER chunks_inserted AFTER INSERT ON email_chunks
BEGIN
 UPDATE index_state SET status='stale', generation=lower(hex(randomblob(16))), last_indexed_at=NULL
 WHERE account_id=NEW.account_id AND email_id=NEW.email_id;
END;
CREATE TRIGGER chunks_deleted AFTER DELETE ON email_chunks
BEGIN
 UPDATE index_state SET status='stale', generation=lower(hex(randomblob(16))), last_indexed_at=NULL
 WHERE account_id=OLD.account_id AND email_id=OLD.email_id;
END;

CREATE TRIGGER embeddings_inserted AFTER INSERT ON chunk_embeddings
BEGIN
 UPDATE index_state SET status='pending', last_indexed_at=NULL
 WHERE account_id=NEW.account_id AND model_id=NEW.model_id AND model_version=NEW.model_version
 AND status<>'stale' AND email_id=(SELECT email_id FROM email_chunks WHERE account_id=NEW.account_id AND id=NEW.chunk_id);
END;
CREATE TRIGGER embeddings_updated AFTER UPDATE ON chunk_embeddings
BEGIN
 UPDATE index_state SET status='pending', last_indexed_at=NULL
 WHERE account_id=NEW.account_id AND model_id=NEW.model_id AND model_version=NEW.model_version
 AND status<>'stale' AND email_id=(SELECT email_id FROM email_chunks WHERE account_id=NEW.account_id AND id=NEW.chunk_id);
END;
CREATE TRIGGER embeddings_deleted AFTER DELETE ON chunk_embeddings
BEGIN
 UPDATE index_state SET status='pending', last_indexed_at=NULL
 WHERE account_id=OLD.account_id AND model_id=OLD.model_id AND model_version=OLD.model_version
 AND status<>'stale' AND email_id=(SELECT email_id FROM email_chunks WHERE account_id=OLD.account_id AND id=OLD.chunk_id);
END;

CREATE TRIGGER embeddings_identity_immutable BEFORE UPDATE OF account_id,chunk_id,model_id,model_version ON chunk_embeddings
WHEN OLD.account_id<>NEW.account_id OR OLD.chunk_id<>NEW.chunk_id OR OLD.model_id<>NEW.model_id OR OLD.model_version<>NEW.model_version
BEGIN SELECT RAISE(ABORT,'immutable embedding identity'); END;
