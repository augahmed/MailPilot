-- Versioned semantic assessments reuse existing runs/results; no vector store.
ALTER TABLE local_index_jobs ADD COLUMN classifier_version TEXT NOT NULL DEFAULT '';
ALTER TABLE classification_runs ADD COLUMN semantic_json TEXT NOT NULL DEFAULT '';
CREATE TABLE classification_state (
 account_id TEXT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
 revision TEXT NOT NULL
);
INSERT INTO classification_state SELECT id,lower(hex(randomblob(16))) FROM accounts;
CREATE TRIGGER semantic_account_insert AFTER INSERT ON accounts BEGIN
 INSERT INTO classification_state VALUES(NEW.id,lower(hex(randomblob(16))));
END;
CREATE TABLE email_category_preferences (
 account_id TEXT NOT NULL,
 email_id TEXT NOT NULL,
 mode TEXT NOT NULL CHECK(mode IN('assigned','unclassified')),
 category_id TEXT,
 revision TEXT NOT NULL,
 PRIMARY KEY(account_id,email_id),
 FOREIGN KEY(account_id,email_id) REFERENCES emails(account_id,id) ON DELETE CASCADE,
 FOREIGN KEY(account_id,category_id) REFERENCES categories(account_id,id) ON DELETE RESTRICT,
 CHECK((mode='assigned' AND category_id IS NOT NULL) OR (mode='unclassified' AND category_id IS NULL))
);
CREATE TABLE classification_email_state (
 account_id TEXT NOT NULL,
 email_id TEXT NOT NULL,
 revision TEXT NOT NULL,
 PRIMARY KEY(account_id,email_id),
 FOREIGN KEY(account_id,email_id) REFERENCES emails(account_id,id) ON DELETE CASCADE
);
INSERT INTO classification_email_state SELECT account_id,id,lower(hex(randomblob(16))) FROM emails;
CREATE TRIGGER semantic_email_insert AFTER INSERT ON emails BEGIN
 INSERT INTO classification_email_state VALUES(NEW.account_id,NEW.id,lower(hex(randomblob(16))));
END;
CREATE TRIGGER semantic_email_update AFTER UPDATE ON emails BEGIN
 UPDATE classification_email_state SET revision=lower(hex(randomblob(16))) WHERE account_id=NEW.account_id AND email_id=NEW.id;
END;
CREATE TRIGGER semantic_categories_insert AFTER INSERT ON categories BEGIN
 UPDATE classification_state SET revision=lower(hex(randomblob(16))) WHERE account_id=NEW.account_id;
END;
CREATE TRIGGER semantic_categories_update AFTER UPDATE ON categories BEGIN
 UPDATE classification_state SET revision=lower(hex(randomblob(16))) WHERE account_id=NEW.account_id;
END;
CREATE TRIGGER semantic_categories_delete AFTER DELETE ON categories BEGIN
 UPDATE classification_state SET revision=lower(hex(randomblob(16))) WHERE account_id=OLD.account_id;
END;
CREATE TRIGGER semantic_category_examples_insert AFTER INSERT ON category_examples BEGIN
 UPDATE classification_state SET revision=lower(hex(randomblob(16))) WHERE account_id=NEW.account_id;
END;
CREATE TRIGGER semantic_category_examples_update AFTER UPDATE ON category_examples BEGIN
 UPDATE classification_state SET revision=lower(hex(randomblob(16))) WHERE account_id=NEW.account_id;
END;
CREATE TRIGGER semantic_category_examples_delete AFTER DELETE ON category_examples BEGIN
 UPDATE classification_state SET revision=lower(hex(randomblob(16))) WHERE account_id=OLD.account_id;
END;
CREATE TRIGGER semantic_category_prototypes_insert AFTER INSERT ON category_prototypes BEGIN
 UPDATE classification_state SET revision=lower(hex(randomblob(16))) WHERE account_id=NEW.account_id;
END;
CREATE TRIGGER semantic_category_prototypes_update AFTER UPDATE ON category_prototypes BEGIN
 UPDATE classification_state SET revision=lower(hex(randomblob(16))) WHERE account_id=NEW.account_id;
END;
CREATE TRIGGER semantic_category_prototypes_delete AFTER DELETE ON category_prototypes BEGIN
 UPDATE classification_state SET revision=lower(hex(randomblob(16))) WHERE account_id=OLD.account_id;
END;
CREATE TRIGGER semantic_index_state_insert AFTER INSERT ON index_state BEGIN
 UPDATE classification_state SET revision=lower(hex(randomblob(16))) WHERE account_id=NEW.account_id;
END;
CREATE TRIGGER semantic_index_state_update AFTER UPDATE ON index_state BEGIN
 UPDATE classification_state SET revision=lower(hex(randomblob(16))) WHERE account_id=NEW.account_id;
END;
CREATE TRIGGER semantic_index_state_delete AFTER DELETE ON index_state BEGIN
 UPDATE classification_state SET revision=lower(hex(randomblob(16))) WHERE account_id=OLD.account_id;
END;
CREATE TRIGGER semantic_email_categories_insert AFTER INSERT ON email_categories BEGIN
 UPDATE classification_email_state SET revision=lower(hex(randomblob(16))) WHERE account_id=NEW.account_id AND email_id=NEW.email_id;
END;
CREATE TRIGGER semantic_email_categories_update AFTER UPDATE ON email_categories BEGIN
 UPDATE classification_email_state SET revision=lower(hex(randomblob(16))) WHERE account_id=NEW.account_id AND email_id=NEW.email_id;
END;
CREATE TRIGGER semantic_email_categories_delete AFTER DELETE ON email_categories BEGIN
 UPDATE classification_email_state SET revision=lower(hex(randomblob(16))) WHERE account_id=OLD.account_id AND email_id=OLD.email_id;
END;
CREATE TRIGGER semantic_email_category_preferences_insert AFTER INSERT ON email_category_preferences BEGIN
 UPDATE classification_email_state SET revision=lower(hex(randomblob(16))) WHERE account_id=NEW.account_id AND email_id=NEW.email_id;
END;
CREATE TRIGGER semantic_email_category_preferences_update AFTER UPDATE ON email_category_preferences BEGIN
 UPDATE classification_email_state SET revision=lower(hex(randomblob(16))) WHERE account_id=NEW.account_id AND email_id=NEW.email_id;
END;
CREATE TRIGGER semantic_email_category_preferences_delete AFTER DELETE ON email_category_preferences BEGIN
 UPDATE classification_email_state SET revision=lower(hex(randomblob(16))) WHERE account_id=OLD.account_id AND email_id=OLD.email_id;
END;
CREATE TRIGGER semantic_email_labels_insert AFTER INSERT ON email_labels BEGIN
 UPDATE classification_email_state SET revision=lower(hex(randomblob(16))) WHERE account_id=NEW.account_id AND email_id=NEW.email_id;
END;
CREATE TRIGGER semantic_email_labels_update AFTER UPDATE ON email_labels BEGIN
 UPDATE classification_email_state SET revision=lower(hex(randomblob(16))) WHERE account_id=NEW.account_id AND email_id=NEW.email_id;
END;
CREATE TRIGGER semantic_email_labels_delete AFTER DELETE ON email_labels BEGIN
 UPDATE classification_email_state SET revision=lower(hex(randomblob(16))) WHERE account_id=OLD.account_id AND email_id=OLD.email_id;
END;
CREATE TRIGGER semantic_label_update AFTER UPDATE ON labels BEGIN
 UPDATE classification_email_state SET revision=lower(hex(randomblob(16))) WHERE account_id=NEW.account_id AND email_id IN(SELECT email_id FROM email_labels WHERE account_id=NEW.account_id AND label_id=NEW.id);
END;
-- Confirmed remote deletion clears current local choices/assessments; immutable
-- Gmail action journals and their retained source tombstones are not rewritten.
CREATE TRIGGER semantic_source_tombstone AFTER UPDATE OF remote_deleted ON emails WHEN NEW.remote_deleted=1 BEGIN
 DELETE FROM email_category_preferences WHERE account_id=NEW.account_id AND email_id=NEW.id;
 DELETE FROM classification_results WHERE account_id=NEW.account_id AND email_id=NEW.id;
 DELETE FROM classification_runs WHERE account_id=NEW.account_id AND email_id=NEW.id;
END;
