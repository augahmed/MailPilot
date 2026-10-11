CREATE TABLE accounts (
    id TEXT PRIMARY KEY NOT NULL,
    provider TEXT NOT NULL,
    provider_account_id TEXT NOT NULL,
    address TEXT NOT NULL,
    display_name TEXT NOT NULL,
    credential_ref TEXT,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(provider, provider_account_id)
);

CREATE TABLE emails (
    id TEXT PRIMARY KEY NOT NULL,
    account_id TEXT NOT NULL REFERENCES accounts(id),
    provider_message_id TEXT NOT NULL,
    provider_thread_id TEXT NOT NULL DEFAULT '',
    subject TEXT NOT NULL,
    body_text TEXT NOT NULL,
    body_html TEXT,
    received_at INTEGER NOT NULL,
    headers_json TEXT NOT NULL CHECK(json_valid(headers_json)),
    attachments_json TEXT NOT NULL CHECK(json_valid(attachments_json)),
    content_hash TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    UNIQUE(account_id, provider_message_id),
    UNIQUE(account_id, id)
);
CREATE INDEX emails_received ON emails(account_id, received_at DESC, id DESC);
CREATE INDEX emails_thread ON emails(account_id, provider_thread_id);

CREATE TABLE email_addresses (
    email_id TEXT NOT NULL REFERENCES emails(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK(role IN ('sender','to','cc')),
    position INTEGER NOT NULL CHECK(position >= 0),
    name TEXT NOT NULL,
    address TEXT NOT NULL,
    CHECK(role != 'sender' OR position = 0),
    PRIMARY KEY(email_id, role, position)
);

CREATE TABLE labels (
    id TEXT PRIMARY KEY NOT NULL,
    account_id TEXT NOT NULL REFERENCES accounts(id),
    provider_label_id TEXT NOT NULL,
    name TEXT NOT NULL,
    kind TEXT NOT NULL CHECK(kind IN ('user','system')),
    UNIQUE(account_id, provider_label_id),
    UNIQUE(account_id, id)
);
CREATE TABLE email_labels (
    account_id TEXT NOT NULL,
    email_id TEXT NOT NULL,
    label_id TEXT NOT NULL,
    PRIMARY KEY(email_id, label_id),
    FOREIGN KEY(account_id, email_id) REFERENCES emails(account_id, id) ON DELETE CASCADE,
    FOREIGN KEY(account_id, label_id) REFERENCES labels(account_id, id)
);
CREATE INDEX email_labels_label ON email_labels(account_id, label_id, email_id);

CREATE TABLE categories (
    id TEXT PRIMARY KEY NOT NULL,
    account_id TEXT NOT NULL REFERENCES accounts(id),
    name TEXT NOT NULL,
    UNIQUE(account_id, name),
    UNIQUE(account_id, id)
);
CREATE TABLE email_categories (
    account_id TEXT NOT NULL,
    email_id TEXT NOT NULL,
    category_id TEXT NOT NULL,
    PRIMARY KEY(email_id, category_id),
    FOREIGN KEY(account_id, email_id) REFERENCES emails(account_id, id) ON DELETE CASCADE,
    FOREIGN KEY(account_id, category_id) REFERENCES categories(account_id, id) ON DELETE CASCADE
);
CREATE TABLE classification_results (
    account_id TEXT NOT NULL,
    email_id TEXT NOT NULL,
    category_id TEXT NOT NULL,
    confidence REAL NOT NULL CHECK(confidence BETWEEN 0 AND 1),
    classifier_version TEXT NOT NULL,
    input_hash TEXT NOT NULL,
    explanation TEXT NOT NULL,
    assessed_at INTEGER NOT NULL,
    PRIMARY KEY(email_id, category_id),
    FOREIGN KEY(account_id, email_id) REFERENCES emails(account_id, id) ON DELETE CASCADE,
    FOREIGN KEY(account_id, category_id) REFERENCES categories(account_id, id) ON DELETE CASCADE
);
CREATE TABLE phishing_results (
    account_id TEXT NOT NULL,
    email_id TEXT PRIMARY KEY NOT NULL,
    score INTEGER NOT NULL CHECK(score BETWEEN 0 AND 100),
    analyzer_version TEXT NOT NULL,
    input_hash TEXT NOT NULL,
    findings_json TEXT NOT NULL CHECK(json_valid(findings_json)),
    assessed_at INTEGER NOT NULL,
    FOREIGN KEY(account_id, email_id) REFERENCES emails(account_id, id) ON DELETE CASCADE
);

CREATE TABLE mailbox_actions (
    id TEXT PRIMARY KEY NOT NULL,
    account_id TEXT NOT NULL REFERENCES accounts(id),
    kind TEXT NOT NULL CHECK(kind IN ('label_delta','undo')),
    reverses_action_id TEXT,
    status TEXT NOT NULL CHECK(status IN ('preview','applying','applied','partial','failed','cancelled','uncertain','expired')),
    plan_digest TEXT NOT NULL,
    revision INTEGER NOT NULL DEFAULT 0 CHECK(revision >= 0),
    created_at INTEGER NOT NULL,
    expires_at INTEGER NOT NULL,
    confirmed_at INTEGER,
    completed_at INTEGER,
    CHECK(expires_at > created_at),
    CHECK((kind = 'undo' AND reverses_action_id IS NOT NULL) OR (kind = 'label_delta' AND reverses_action_id IS NULL)),
    UNIQUE(account_id, id),
    FOREIGN KEY(account_id, reverses_action_id) REFERENCES mailbox_actions(account_id, id)
);
CREATE INDEX mailbox_actions_account ON mailbox_actions(account_id, created_at DESC, id);
CREATE INDEX mailbox_actions_recovery ON mailbox_actions(account_id, status);
CREATE TABLE mailbox_action_items (
    action_id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    email_id TEXT NOT NULL,
    provider_message_id TEXT NOT NULL,
    requested_delta_json TEXT NOT NULL CHECK(json_valid(requested_delta_json)),
    before_labels_json TEXT NOT NULL CHECK(json_valid(before_labels_json)),
    actual_delta_json TEXT CHECK(actual_delta_json IS NULL OR json_valid(actual_delta_json)),
    after_labels_json TEXT CHECK(after_labels_json IS NULL OR json_valid(after_labels_json)),
    state TEXT NOT NULL CHECK(state IN ('pending','in_flight','applied','failed','uncertain','conflict','skipped')),
    attempt_count INTEGER NOT NULL DEFAULT 0 CHECK(attempt_count >= 0),
    last_error_code TEXT NOT NULL DEFAULT '',
    updated_at INTEGER NOT NULL,
    PRIMARY KEY(action_id, email_id),
    FOREIGN KEY(account_id, action_id) REFERENCES mailbox_actions(account_id, id) ON DELETE CASCADE,
    FOREIGN KEY(account_id, email_id) REFERENCES emails(account_id, id)
);
