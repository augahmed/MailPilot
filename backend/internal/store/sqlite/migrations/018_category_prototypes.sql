-- Shared local category representations; historical vector/assessment rows stay inert.
-- Only untouched, entirely unused legacy job defaults are replaced. Never merge
-- assignments or rewrite category definitions referenced by recovery evidence.
CREATE TEMP TABLE jobs_default_upgrade AS
 SELECT a.account_id,a.id AS applications_id,b.id AS interviews_id FROM categories a
 JOIN categories b ON b.account_id=a.account_id AND b.name='Job Interviews'
 WHERE a.name='Job Applications' AND a.enabled=1 AND b.enabled=1
 AND a.created_at=a.updated_at AND b.created_at=b.updated_at
 AND a.description='Applications submitted for employment, application acknowledgments, recruiter updates, and decisions about job opportunities.'
 AND b.description='Interview invitations, scheduling, preparation instructions, assessments, and follow-up conversations with hiring teams.'
 AND NOT EXISTS(SELECT 1 FROM categories c WHERE c.account_id=a.account_id AND c.name='Jobs')
 AND NOT EXISTS(SELECT 1 FROM email_categories x WHERE x.account_id=a.account_id AND x.category_id IN(a.id,b.id))
 AND NOT EXISTS(SELECT 1 FROM classification_results x WHERE x.account_id=a.account_id AND x.category_id IN(a.id,b.id))
 AND NOT EXISTS(SELECT 1 FROM classification_runs x WHERE x.account_id=a.account_id)
 AND NOT EXISTS(SELECT 1 FROM mailbox_actions x WHERE x.account_id=a.account_id)
 AND NOT EXISTS(SELECT 1 FROM reply_draft_actions x WHERE x.account_id=a.account_id);
UPDATE categories SET name='Jobs',description='Employment applications, application acknowledgments, recruiter updates, interview invitations, scheduling, assessments, and hiring decisions.'
 WHERE id IN(SELECT applications_id FROM jobs_default_upgrade);
DELETE FROM categories WHERE id IN(SELECT interviews_id FROM jobs_default_upgrade);
DROP TABLE jobs_default_upgrade;
DROP TRIGGER account_default_categories;
CREATE TRIGGER account_default_categories AFTER INSERT ON accounts BEGIN
 INSERT INTO categories(id,account_id,name,description,enabled,created_at,updated_at) VALUES(lower(hex(randomblob(16))),NEW.id,'Jobs','Employment applications, application acknowledgments, recruiter updates, interview invitations, scheduling, assessments, and hiring decisions.',1,NEW.created_at,NEW.created_at);
 INSERT INTO categories(id,account_id,name,description,enabled,created_at,updated_at) VALUES(lower(hex(randomblob(16))),NEW.id,'School','Coursework, classes, enrollment, academic deadlines, campus announcements, and communication with teachers or classmates.',1,NEW.created_at,NEW.created_at);
 INSERT INTO categories(id,account_id,name,description,enabled,created_at,updated_at) VALUES(lower(hex(randomblob(16))),NEW.id,'Receipts','Purchase confirmations, itemized receipts, paid invoices, order details, shipping updates, and refunds.',1,NEW.created_at,NEW.created_at);
 INSERT INTO categories(id,account_id,name,description,enabled,created_at,updated_at) VALUES(lower(hex(randomblob(16))),NEW.id,'Finance','Banking, credit cards, investments, loans, taxes, account statements, and other financial account matters.',1,NEW.created_at,NEW.created_at);
 INSERT INTO categories(id,account_id,name,description,enabled,created_at,updated_at) VALUES(lower(hex(randomblob(16))),NEW.id,'Newsletters','Recurring editorial updates, subscriptions, digests, announcements, and promotional mailing lists.',1,NEW.created_at,NEW.created_at);
 INSERT INTO categories(id,account_id,name,description,enabled,created_at,updated_at) VALUES(lower(hex(randomblob(16))),NEW.id,'Personal','Conversations with friends and family, personal plans, invitations, and everyday non-work correspondence.',1,NEW.created_at,NEW.created_at);
END;
ALTER TABLE categories ADD COLUMN vector_revision TEXT NOT NULL DEFAULT '';
UPDATE categories SET vector_revision=lower(hex(randomblob(16)));
ALTER TABLE local_index_jobs ADD COLUMN category_recipe TEXT NOT NULL DEFAULT '';
CREATE TABLE category_examples (
 account_id TEXT NOT NULL,
 category_id TEXT NOT NULL,
 email_id TEXT NOT NULL,
 source_revision TEXT NOT NULL CHECK(length(source_revision)=64 AND source_revision NOT GLOB '*[^0-9a-f]*'),
 confirmation_id TEXT NOT NULL,
 confirmed_at INTEGER NOT NULL,
 PRIMARY KEY(account_id,category_id,email_id),
 FOREIGN KEY(account_id,category_id) REFERENCES categories(account_id,id) ON DELETE CASCADE,
 FOREIGN KEY(account_id,email_id) REFERENCES emails(account_id,id) ON DELETE CASCADE
);
CREATE INDEX category_example_sources ON category_examples(account_id,email_id);
CREATE TABLE category_prototypes (
 account_id TEXT NOT NULL,
 category_id TEXT NOT NULL,
 profile_id TEXT NOT NULL CHECK(length(profile_id)=64),
 model_id TEXT NOT NULL,
 model_version TEXT NOT NULL,
 dimension INTEGER NOT NULL CHECK(dimension BETWEEN 1 AND 8192),
 fingerprint TEXT NOT NULL CHECK(length(fingerprint)=64),
 preprocessing_version TEXT NOT NULL,
 index_version TEXT NOT NULL,
 recipe TEXT NOT NULL,
 definition_hash TEXT NOT NULL CHECK(length(definition_hash)=64),
 source_hash TEXT NOT NULL,
 definition_inputs_json TEXT NOT NULL CHECK(json_valid(definition_inputs_json)),
 definition_vector BLOB NOT NULL CHECK(length(definition_vector)=dimension*4),
 vector_data BLOB,
 status TEXT NOT NULL CHECK(status IN('ready','partial','stale')),
 updated_at INTEGER NOT NULL,
 PRIMARY KEY(account_id,category_id,profile_id),
 FOREIGN KEY(account_id,category_id) REFERENCES categories(account_id,id) ON DELETE CASCADE,
 FOREIGN KEY(account_id,model_id,model_version) REFERENCES local_index_models(account_id,model_id,model_version),
 CHECK((status IN('ready','partial') AND length(source_hash)=64 AND typeof(vector_data)='blob' AND length(vector_data)=dimension*4) OR (status='stale' AND vector_data IS NULL))
);
CREATE TRIGGER category_vector_insert AFTER INSERT ON categories BEGIN
 UPDATE categories SET vector_revision=lower(hex(randomblob(16))) WHERE account_id=NEW.account_id AND id=NEW.id;
END;
CREATE TRIGGER category_vector_edit AFTER UPDATE OF name,description,enabled,updated_at ON categories BEGIN
 UPDATE categories SET vector_revision=lower(hex(randomblob(16))) WHERE account_id=NEW.account_id AND id=NEW.id;
 UPDATE category_prototypes SET status='stale',vector_data=NULL,source_hash='' WHERE account_id=NEW.account_id AND category_id=NEW.id;
END;
CREATE TRIGGER category_example_added AFTER INSERT ON category_examples BEGIN
 UPDATE category_prototypes SET status='stale',vector_data=NULL,source_hash='' WHERE account_id=NEW.account_id AND category_id=NEW.category_id;
END;
CREATE TRIGGER category_example_edited AFTER UPDATE ON category_examples BEGIN
 UPDATE category_prototypes SET status='stale',vector_data=NULL,source_hash='' WHERE account_id=NEW.account_id AND category_id=NEW.category_id;
END;
CREATE TRIGGER category_example_removed AFTER DELETE ON category_examples BEGIN
 UPDATE category_prototypes SET status='stale',vector_data=NULL,source_hash='' WHERE account_id=OLD.account_id AND category_id=OLD.category_id;
END;
CREATE TRIGGER category_example_source_changed AFTER UPDATE OF content_hash,body_state,remote_deleted ON emails
WHEN OLD.content_hash<>NEW.content_hash OR OLD.body_state<>NEW.body_state OR OLD.remote_deleted<>NEW.remote_deleted BEGIN
 UPDATE category_prototypes SET status='stale',vector_data=NULL,source_hash='' WHERE account_id=NEW.account_id AND category_id IN(SELECT category_id FROM category_examples WHERE account_id=NEW.account_id AND email_id=NEW.id);
 DELETE FROM category_examples WHERE account_id=NEW.account_id AND email_id=NEW.id AND NEW.remote_deleted=1;
END;
CREATE TRIGGER category_example_index_insert AFTER INSERT ON index_state BEGIN
 UPDATE category_prototypes SET status='stale',vector_data=NULL,source_hash='' WHERE account_id=NEW.account_id AND category_id IN(SELECT category_id FROM category_examples WHERE account_id=NEW.account_id AND email_id=NEW.email_id);
END;
CREATE TRIGGER category_example_index_update AFTER UPDATE ON index_state BEGIN
 UPDATE category_prototypes SET status='stale',vector_data=NULL,source_hash='' WHERE account_id=NEW.account_id AND category_id IN(SELECT category_id FROM category_examples WHERE account_id=NEW.account_id AND email_id=NEW.email_id);
END;
CREATE TRIGGER category_example_index_delete AFTER DELETE ON index_state BEGIN
 UPDATE category_prototypes SET status='stale',vector_data=NULL,source_hash='' WHERE account_id=OLD.account_id AND category_id IN(SELECT category_id FROM category_examples WHERE account_id=OLD.account_id AND email_id=OLD.email_id);
END;
