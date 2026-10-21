-- FTS is a derived index over the shared passages, never a second text store.
CREATE VIRTUAL TABLE chunk_fts USING fts5(text, context_text,
 content='email_chunks', content_rowid='rowid', tokenize='unicode61');
INSERT INTO chunk_fts(chunk_fts) VALUES('rebuild');
CREATE TRIGGER chunk_fts_insert AFTER INSERT ON email_chunks BEGIN
 INSERT INTO chunk_fts(rowid,text,context_text) VALUES(NEW.rowid,NEW.text,NEW.context_text);
END;
CREATE TRIGGER chunk_fts_delete AFTER DELETE ON email_chunks BEGIN
 INSERT INTO chunk_fts(chunk_fts,rowid,text,context_text) VALUES('delete',OLD.rowid,OLD.text,OLD.context_text);
END;
CREATE TRIGGER chunk_fts_update AFTER UPDATE OF text,context_text ON email_chunks BEGIN
 INSERT INTO chunk_fts(chunk_fts,rowid,text,context_text) VALUES('delete',OLD.rowid,OLD.text,OLD.context_text);
 INSERT INTO chunk_fts(rowid,text,context_text) VALUES(NEW.rowid,NEW.text,NEW.context_text);
END;
