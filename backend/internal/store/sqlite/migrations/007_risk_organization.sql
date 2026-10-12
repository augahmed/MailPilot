-- Risk proposals have no classification category. Preserve existing category
-- references and all account-scoped foreign keys while allowing that absence.
CREATE TABLE organization_emails_v7 (
 account_id TEXT NOT NULL,
 action_id TEXT NOT NULL,
 email_id TEXT NOT NULL,
 category_id TEXT,
 PRIMARY KEY(action_id,email_id),
 FOREIGN KEY(account_id,action_id) REFERENCES organization_plans(account_id,action_id),
 FOREIGN KEY(account_id,email_id) REFERENCES emails(account_id,id),
 FOREIGN KEY(account_id,category_id) REFERENCES categories(account_id,id)
);
INSERT INTO organization_emails_v7 SELECT * FROM organization_emails;
DROP TABLE organization_emails;
ALTER TABLE organization_emails_v7 RENAME TO organization_emails;
