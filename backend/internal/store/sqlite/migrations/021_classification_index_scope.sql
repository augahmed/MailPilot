-- Email index changes revoke that email's assessment. Shared category/example
-- changes still rotate classification_state through the existing triggers.
-- Preserve per-email ABA fencing without invalidating unrelated reviewed mail.
DROP TRIGGER semantic_index_state_insert;
DROP TRIGGER semantic_index_state_update;
DROP TRIGGER semantic_index_state_delete;
CREATE TRIGGER semantic_index_state_insert AFTER INSERT ON index_state BEGIN
 UPDATE classification_email_state SET revision=lower(hex(randomblob(16)))
 WHERE account_id=NEW.account_id AND email_id=NEW.email_id;
END;
CREATE TRIGGER semantic_index_state_update AFTER UPDATE ON index_state BEGIN
 UPDATE classification_email_state SET revision=lower(hex(randomblob(16)))
 WHERE account_id=NEW.account_id AND email_id=NEW.email_id;
END;
CREATE TRIGGER semantic_index_state_delete AFTER DELETE ON index_state BEGIN
 UPDATE classification_email_state SET revision=lower(hex(randomblob(16)))
 WHERE account_id=OLD.account_id AND email_id=OLD.email_id;
END;
