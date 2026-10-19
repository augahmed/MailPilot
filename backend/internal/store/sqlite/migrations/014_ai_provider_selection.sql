-- Existing configured models use the only previously supported adapter.
ALTER TABLE reply_ai_settings ADD COLUMN provider TEXT NOT NULL DEFAULT 'openai'
 CHECK(length(provider) BETWEEN 1 AND 64);
