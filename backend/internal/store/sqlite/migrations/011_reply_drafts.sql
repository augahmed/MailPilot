-- Non-secret AI preference; API keys remain exclusively in OS credential storage.
CREATE TABLE reply_ai_settings (
 account_id TEXT PRIMARY KEY REFERENCES accounts(id),
 model TEXT NOT NULL CHECK(length(model)<=128),
 enabled INTEGER NOT NULL CHECK(enabled IN (0,1))
);
-- Draft creation has different recovery semantics from label membership. Keep
-- an immutable reviewed plan and a single-dispatch state in its own journal.
CREATE TABLE reply_draft_actions (
 account_id TEXT NOT NULL,
 id TEXT NOT NULL,
 email_id TEXT NOT NULL,
 plan_json TEXT NOT NULL CHECK(json_valid(plan_json)),
 state TEXT NOT NULL CHECK(state IN ('preview','in_flight','created','uncertain','conflict','resolved')),
 reference_json TEXT NOT NULL DEFAULT '{}' CHECK(json_valid(reference_json)),
 code TEXT NOT NULL DEFAULT '',
 reconciled INTEGER NOT NULL DEFAULT 0 CHECK(reconciled IN (0,1)),
 created_at INTEGER NOT NULL,
 updated_at INTEGER NOT NULL,
 PRIMARY KEY(account_id,id),
 FOREIGN KEY(account_id,email_id) REFERENCES emails(account_id,id)
);
CREATE UNIQUE INDEX reply_draft_unresolved ON reply_draft_actions(account_id)
 WHERE state IN ('in_flight','uncertain','conflict');
CREATE TABLE reply_draft_sources (
 account_id TEXT NOT NULL,
 action_id TEXT NOT NULL,
 email_id TEXT NOT NULL,
 PRIMARY KEY(account_id,action_id,email_id),
 FOREIGN KEY(account_id,action_id) REFERENCES reply_draft_actions(account_id,id),
 FOREIGN KEY(account_id,email_id) REFERENCES emails(account_id,id)
);
CREATE TRIGGER reply_draft_plan_immutable BEFORE UPDATE OF plan_json,email_id,account_id,id ON reply_draft_actions
 BEGIN SELECT RAISE(ABORT,'immutable draft plan'); END;
