-- Opaque content identity and explicit normalization completeness, not a sync cursor.
ALTER TABLE emails ADD COLUMN content_revision TEXT NOT NULL DEFAULT '';
ALTER TABLE emails ADD COLUMN body_state TEXT NOT NULL DEFAULT '' CHECK(body_state IN ('','complete','unavailable'));
