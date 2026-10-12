package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/embedding"
	"mailpilot.local/core/internal/repository"
)

func contentHash(e domain.Email) (string, error) { return repository.EmailContentHash(e) }

func (r *emailRepository) Upsert(ctx context.Context, e domain.Email) (domain.EmailID, error) {
	ids, err := r.UpsertBatch(ctx, e.AccountID, []domain.Email{e})
	if err != nil {
		return "", err
	}
	return ids[0], nil
}
func (r *emailRepository) UpsertBatch(ctx context.Context, account domain.AccountID, emails []domain.Email) ([]domain.EmailID, error) {
	if account == "" || len(emails) > 100 {
		return nil, repository.ErrInvalid
	}
	for _, e := range emails {
		if e.AccountID != account || e.Validate() != nil {
			return nil, repository.ErrInvalid
		}
	}
	ids := make([]domain.EmailID, 0, len(emails))
	err := r.store.transaction(ctx, func(tx *sql.Tx) error {
		if err := exists(ctx, tx, "SELECT 1 FROM accounts WHERE id=?", account); err != nil {
			return err
		}
		for _, e := range emails {
			id, err := upsertEmail(ctx, tx, e)
			if err != nil {
				return err
			}
			ids = append(ids, id)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return ids, nil
}

func upsertEmail(ctx context.Context, tx *sql.Tx, e domain.Email) (domain.EmailID, error) {
	var id domain.EmailID
	var oldHash string
	err := tx.QueryRowContext(ctx, "SELECT id,content_hash FROM emails WHERE account_id=? AND provider_message_id=?", e.AccountID, e.ProviderID).Scan(&id, &oldHash)
	if errors.Is(err, sql.ErrNoRows) {
		id = e.ID
		if id == "" {
			id = domain.EmailID(newID())
		}
	} else if err != nil {
		return "", err
	}
	if e.ID != "" && e.ID != id {
		return "", repository.ErrConflict
	}
	hash, err := contentHash(e)
	if err != nil {
		return "", err
	}
	headers, _ := json.Marshal(append([]domain.EmailHeader{}, e.Headers...))
	attachments, _ := json.Marshal(append([]domain.AttachmentMetadata{}, e.Attachments...))
	now := nowMillis()
	_, err = tx.ExecContext(ctx, `INSERT INTO emails(id,account_id,provider_message_id,provider_thread_id,subject,body_text,body_html,received_at,headers_json,attachments_json,content_hash,created_at,updated_at,content_revision,body_state)
 VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(account_id,provider_message_id) DO UPDATE SET
 content_revision=excluded.content_revision,body_state=excluded.body_state,provider_thread_id=excluded.provider_thread_id,subject=excluded.subject,body_text=excluded.body_text,body_html=excluded.body_html,
 received_at=excluded.received_at,headers_json=excluded.headers_json,attachments_json=excluded.attachments_json,content_hash=excluded.content_hash,updated_at=excluded.updated_at`,
		id, e.AccountID, e.ProviderID, e.ProviderThreadID, e.Subject, e.BodyText, e.BodyHTML, e.ReceivedAt.UnixMilli(), string(headers), string(attachments), hash, now, now, e.ContentRevision, e.BodyState)
	if err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM email_addresses WHERE email_id=?", id); err != nil {
		return "", err
	}
	for role, addresses := range map[string][]domain.EmailAddress{"sender": {e.Sender}, "to": e.Recipients, "cc": e.CCRecipients} {
		for position, address := range addresses {
			if _, err = tx.ExecContext(ctx, "INSERT INTO email_addresses VALUES(?,?,?,?,?)", id, role, position, address.Name, address.Address); err != nil {
				return "", err
			}
		}
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM email_labels WHERE account_id=? AND email_id=?", e.AccountID, id); err != nil {
		return "", err
	}
	for _, label := range e.Labels {
		labelID, err := upsertLabel(ctx, tx, label)
		if err != nil {
			return "", err
		}
		if _, err = tx.ExecContext(ctx, "INSERT INTO email_labels VALUES(?,?,?) ON CONFLICT DO NOTHING", e.AccountID, id, labelID); err != nil {
			return "", err
		}
	}
	if oldHash != "" && oldHash != hash {
		// Keep vectors for unchanged cleaned text (for example, quoted-chain or
		// recipient changes); remove stale derived data in this same transaction.
		prepared, prepareErr := embedding.Prepare(e)
		if prepareErr != nil {
			prepared.Hash = "" // Ineligible content invalidates every cached vector.
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM email_embeddings WHERE account_id=? AND email_id=? AND (input_hash<>? OR preprocessing_version<>?)", e.AccountID, id, prepared.Hash, embedding.PreprocessingVersion); err != nil {
			return "", err
		}
		for _, table := range []string{"classification_results", "classification_runs", "phishing_results"} {
			if _, err = tx.ExecContext(ctx, "DELETE FROM "+table+" WHERE account_id=? AND email_id=?", e.AccountID, id); err != nil {
				return "", err
			}
		}
	}
	return id, nil
}

func upsertLabel(ctx context.Context, tx *sql.Tx, l domain.EmailLabel) (domain.LabelID, error) {
	var id domain.LabelID
	err := tx.QueryRowContext(ctx, "SELECT id FROM labels WHERE account_id=? AND provider_label_id=?", l.AccountID, l.ProviderID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		id = l.ID
		if id == "" {
			id = domain.LabelID(newID())
		}
	} else if err != nil {
		return "", err
	}
	if l.ID != "" && l.ID != id {
		return "", repository.ErrConflict
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO labels VALUES(?,?,?,?,?) ON CONFLICT(account_id,provider_label_id) DO UPDATE SET name=excluded.name,kind=excluded.kind`, id, l.AccountID, l.ProviderID, l.Name, l.Kind)
	return id, err
}

const emailColumns = "id,account_id,provider_message_id,provider_thread_id,subject,body_text,body_html,received_at,headers_json,attachments_json,content_hash,created_at,updated_at,content_revision,body_state"

type scanner interface{ Scan(...any) error }

func scanEmail(row scanner) (repository.StoredEmail, error) {
	var result repository.StoredEmail
	e := &result.Email
	var html sql.NullString
	var received, created, updated int64
	var headers, attachments string
	err := row.Scan(&e.ID, &e.AccountID, &e.ProviderID, &e.ProviderThreadID, &e.Subject, &e.BodyText, &html, &received, &headers, &attachments, &result.ContentHash, &created, &updated, &e.ContentRevision, &e.BodyState)
	if err != nil {
		return result, err
	}
	if html.Valid {
		e.BodyHTML = &html.String
	}
	e.ReceivedAt = instant(received)
	result.CreatedAt = instant(created)
	result.UpdatedAt = instant(updated)
	if err = json.Unmarshal([]byte(headers), &e.Headers); err != nil {
		return result, err
	}
	if err = json.Unmarshal([]byte(attachments), &e.Attachments); err != nil {
		return result, err
	}
	return result, nil
}

func loadEmailRelations(ctx context.Context, tx *sql.Tx, e *domain.Email) error {
	rows, err := tx.QueryContext(ctx, `SELECT role,name,address FROM email_addresses WHERE email_id=? ORDER BY role,position`, e.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var role string
		var a domain.EmailAddress
		if err := rows.Scan(&role, &a.Name, &a.Address); err != nil {
			_ = rows.Close()
			return err
		}
		switch role {
		case "sender":
			e.Sender = a
		case "to":
			e.Recipients = append(e.Recipients, a)
		case "cc":
			e.CCRecipients = append(e.CCRecipients, a)
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	rows, err = tx.QueryContext(ctx, `SELECT l.id,l.account_id,l.provider_label_id,l.name,l.kind FROM labels l JOIN email_labels el ON el.account_id=l.account_id AND el.label_id=l.id WHERE el.account_id=? AND el.email_id=? ORDER BY l.provider_label_id`, e.AccountID, e.ID)
	if err != nil {
		return err
	}
	for rows.Next() {
		var l domain.EmailLabel
		if err := rows.Scan(&l.ID, &l.AccountID, &l.ProviderID, &l.Name, &l.Kind); err != nil {
			_ = rows.Close()
			return err
		}
		e.Labels = append(e.Labels, l)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	rows, err = tx.QueryContext(ctx, `SELECT c.id,c.name,c.description,c.enabled,c.created_at,c.updated_at FROM categories c JOIN email_categories ec ON ec.account_id=c.account_id AND ec.category_id=c.id WHERE ec.account_id=? AND ec.email_id=? ORDER BY c.name,c.id`, e.AccountID, e.ID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		c, err := scanCategory(rows)
		if err != nil {
			return err
		}
		e.Categories = append(e.Categories, c)
	}
	return rows.Err()
}

func (r *emailRepository) get(ctx context.Context, account domain.AccountID, column string, id any) (repository.StoredEmail, error) {
	var result repository.StoredEmail
	err := r.store.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = scanEmail(tx.QueryRowContext(ctx, "SELECT "+emailColumns+" FROM emails WHERE account_id=? AND "+column+"=?", account, id))
		if err != nil {
			return err
		}
		return loadEmailRelations(ctx, tx, &result.Email)
	})
	if err != nil {
		return repository.StoredEmail{}, err
	}
	return result, nil
}
func (r *emailRepository) Get(ctx context.Context, a domain.AccountID, id domain.EmailID) (repository.StoredEmail, error) {
	return r.get(ctx, a, "id", id)
}
func (r *emailRepository) GetByProviderID(ctx context.Context, a domain.AccountID, id domain.ProviderMessageID) (repository.StoredEmail, error) {
	return r.get(ctx, a, "provider_message_id", id)
}
func (r *emailRepository) List(ctx context.Context, a domain.AccountID, page repository.Page) ([]repository.StoredEmail, error) {
	if a == "" || page.Validate() != nil {
		return nil, repository.ErrInvalid
	}
	result := []repository.StoredEmail{}
	err := r.store.transaction(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, "SELECT "+emailColumns+" FROM emails WHERE account_id=? ORDER BY received_at DESC,id DESC LIMIT ? OFFSET ?", a, page.Limit, page.Offset)
		if err != nil {
			return err
		}
		for rows.Next() {
			e, err := scanEmail(rows)
			if err != nil {
				_ = rows.Close()
				return err
			}
			result = append(result, e)
		}
		err = rows.Err()
		_ = rows.Close()
		if err != nil {
			return err
		}
		for i := range result {
			if err := loadEmailRelations(ctx, tx, &result[i].Email); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}

func checkInputHash(ctx context.Context, tx *sql.Tx, a domain.AccountID, id domain.EmailID, hash string) error {
	var current string
	if err := tx.QueryRowContext(ctx, "SELECT content_hash FROM emails WHERE account_id=? AND id=?", a, id).Scan(&current); err != nil {
		return err
	}
	if hash != current {
		return repository.ErrConflict
	}
	return nil
}
func (r *emailRepository) SavePhishingResult(ctx context.Context, p domain.PhishingResult) error {
	if p.AccountID == "" || p.EmailID == "" || p.Score < 0 || p.Score > 100 || p.AnalyzerVersion == "" || p.InputHash == "" || p.AssessedAt.IsZero() {
		return repository.ErrInvalid
	}
	findings, _ := json.Marshal(append([]domain.PhishingFinding{}, p.Findings...))
	return r.store.transaction(ctx, func(tx *sql.Tx) error {
		if err := checkInputHash(ctx, tx, p.AccountID, p.EmailID, p.InputHash); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx, `INSERT INTO phishing_results VALUES(?,?,?,?,?,?,?) ON CONFLICT(email_id) DO UPDATE SET score=excluded.score,analyzer_version=excluded.analyzer_version,input_hash=excluded.input_hash,findings_json=excluded.findings_json,assessed_at=excluded.assessed_at WHERE excluded.assessed_at >= phishing_results.assessed_at`, p.AccountID, p.EmailID, p.Score, p.AnalyzerVersion, p.InputHash, string(findings), p.AssessedAt.UnixMilli())
		if err != nil {
			return err
		}
		n, err := result.RowsAffected()
		if err == nil && n == 0 {
			return repository.ErrConflict
		}
		return err
	})
}
func (r *emailRepository) GetPhishingResult(ctx context.Context, a domain.AccountID, id domain.EmailID) (domain.PhishingResult, error) {
	var p domain.PhishingResult
	var findings string
	var assessed int64
	err := r.store.db.QueryRowContext(ctx, `SELECT account_id,email_id,score,analyzer_version,input_hash,findings_json,assessed_at FROM phishing_results WHERE account_id=? AND email_id=? AND input_hash=(SELECT content_hash FROM emails WHERE account_id=phishing_results.account_id AND id=phishing_results.email_id)`, a, id).Scan(&p.AccountID, &p.EmailID, &p.Score, &p.AnalyzerVersion, &p.InputHash, &findings, &assessed)
	if err != nil {
		return p, storageError(err)
	}
	if err = json.Unmarshal([]byte(findings), &p.Findings); err != nil {
		return domain.PhishingResult{}, repository.ErrStorage
	}
	p.AssessedAt = instant(assessed)
	return p, nil
}

// ListSummaries avoids loading cached bodies into the mailbox list or IPC.
func (r *emailRepository) ListSummaries(ctx context.Context, a domain.AccountID, page repository.Page) ([]repository.EmailSummary, error) {
	if a == "" || page.Validate() != nil {
		return nil, repository.ErrInvalid
	}
	rows, err := r.store.db.QueryContext(ctx, `SELECT e.id,COALESCE(a.name,''),COALESCE(a.address,''),e.subject,e.received_at,e.body_state FROM emails e LEFT JOIN email_addresses a ON a.email_id=e.id AND a.role='sender' WHERE e.account_id=? ORDER BY e.received_at DESC,e.id DESC LIMIT ? OFFSET ?`, a, page.Limit, page.Offset)
	if err != nil {
		return nil, storageError(err)
	}
	defer rows.Close()
	result := []repository.EmailSummary{}
	for rows.Next() {
		var row repository.EmailSummary
		var ms int64
		if err := rows.Scan(&row.ID, &row.Sender.Name, &row.Sender.Address, &row.Subject, &ms, &row.BodyState); err != nil {
			return nil, storageError(err)
		}
		row.ReceivedAt = instant(ms)
		result = append(result, row)
	}
	return result, storageError(rows.Err())
}
