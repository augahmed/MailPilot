-- Immutable organization previews and explicit write outcomes extend the existing
-- action journal. Label creation is journaled alongside message memberships.
CREATE TABLE organization_plans (
 account_id TEXT NOT NULL,
 action_id TEXT NOT NULL,
 plan_json TEXT NOT NULL CHECK(json_valid(plan_json)),
 steps_json TEXT NOT NULL CHECK(json_valid(steps_json)),
 PRIMARY KEY(account_id,action_id),
 FOREIGN KEY(account_id,action_id) REFERENCES mailbox_actions(account_id,id)
);

CREATE TABLE organization_emails (
 account_id TEXT NOT NULL,
 action_id TEXT NOT NULL,
 email_id TEXT NOT NULL,
 category_id TEXT NOT NULL,
 PRIMARY KEY(action_id,email_id),
 FOREIGN KEY(account_id,action_id) REFERENCES organization_plans(account_id,action_id),
 FOREIGN KEY(account_id,email_id) REFERENCES emails(account_id,id),
 FOREIGN KEY(account_id,category_id) REFERENCES categories(account_id,id)
);
