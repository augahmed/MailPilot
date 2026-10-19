-- Provenance/context for deterministic source projection. Old chunks stay valid
-- with unknown mappings; do not invent offsets or rewrite historical vectors.
ALTER TABLE email_chunks ADD COLUMN source_kind TEXT NOT NULL DEFAULT ''
 CHECK(source_kind IN ('','subject','body_text','body_html'));
ALTER TABLE email_chunks ADD COLUMN section_index INTEGER NOT NULL DEFAULT 0 CHECK(section_index>=0);
ALTER TABLE email_chunks ADD COLUMN section_start_offset INTEGER NOT NULL DEFAULT 0 CHECK(section_start_offset>=0);
ALTER TABLE email_chunks ADD COLUMN section_end_offset INTEGER NOT NULL DEFAULT 0 CHECK(section_end_offset>=section_start_offset);
ALTER TABLE email_chunks ADD COLUMN context_text TEXT NOT NULL DEFAULT '' CHECK(length(CAST(context_text AS BLOB))<=1024);
ALTER TABLE index_state ADD COLUMN content_status TEXT NOT NULL DEFAULT 'partial'
 CHECK(content_status IN ('complete','partial','unavailable'));
UPDATE index_state SET content_status=CASE WHEN body_state='complete' THEN 'complete' ELSE 'partial' END;

CREATE TRIGGER chunks_context_changed AFTER UPDATE OF context_text ON email_chunks
WHEN OLD.context_text<>NEW.context_text
BEGIN
 DELETE FROM chunk_embeddings WHERE account_id=OLD.account_id AND chunk_id=OLD.id;
 UPDATE index_state SET status='stale',generation=lower(hex(randomblob(16))),last_indexed_at=NULL
 WHERE account_id=OLD.account_id AND email_id=OLD.email_id;
END;
CREATE TRIGGER chunks_mapping_changed AFTER UPDATE OF source_kind,section_index,section_start_offset,section_end_offset ON email_chunks
WHEN OLD.source_kind<>NEW.source_kind OR OLD.section_index<>NEW.section_index
 OR OLD.section_start_offset<>NEW.section_start_offset OR OLD.section_end_offset<>NEW.section_end_offset
BEGIN
 UPDATE index_state SET status='stale',generation=lower(hex(randomblob(16))),last_indexed_at=NULL
 WHERE account_id=OLD.account_id AND email_id=OLD.email_id;
END;

CREATE TRIGGER chunks_mapping_insert BEFORE INSERT ON email_chunks
WHEN (NEW.source_kind<>'' AND (NEW.section_start_offset>NEW.start_offset OR NEW.section_end_offset<NEW.end_offset
 OR NEW.section_end_offset>4194304 OR NEW.section_index>=4096))
 OR (NEW.source_kind='' AND (NEW.section_index<>0 OR NEW.section_start_offset<>0 OR NEW.section_end_offset<>0))
BEGIN SELECT RAISE(ABORT,'invalid chunk mapping'); END;
CREATE TRIGGER chunks_mapping_update BEFORE UPDATE OF source_kind,section_index,section_start_offset,section_end_offset,start_offset,end_offset ON email_chunks
WHEN (NEW.source_kind<>'' AND (NEW.section_start_offset>NEW.start_offset OR NEW.section_end_offset<NEW.end_offset
 OR NEW.section_end_offset>4194304 OR NEW.section_index>=4096))
 OR (NEW.source_kind='' AND (NEW.section_index<>0 OR NEW.section_start_offset<>0 OR NEW.section_end_offset<>0))
BEGIN SELECT RAISE(ABORT,'invalid chunk mapping'); END;
