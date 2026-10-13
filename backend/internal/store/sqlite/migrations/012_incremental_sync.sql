CREATE TABLE sync_checkpoints (
 account_id TEXT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
 state_json TEXT NOT NULL CHECK(json_valid(state_json)),
 updated_at INTEGER NOT NULL
);
-- Keep stable identities referenced by immutable action/reply journals. A
-- confirmed remote deletion clears content and derived data, hiding this row.
ALTER TABLE emails ADD COLUMN remote_deleted INTEGER NOT NULL DEFAULT 0 CHECK(remote_deleted IN (0,1));
CREATE INDEX emails_sync_scan ON emails(account_id,remote_deleted,provider_message_id);
