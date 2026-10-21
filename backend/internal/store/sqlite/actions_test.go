package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

func actionPlan(account domain.AccountID, id domain.EmailID, remote domain.ProviderMessageID) domain.MailboxAction {
	return domain.MailboxAction{AccountID: account, Kind: domain.ActionLabelDelta, ExpiresAt: time.Now().Add(time.Hour), Items: []domain.MailboxActionItem{{EmailID: id, ProviderMessageID: remote, RequestedDelta: domain.LabelDelta{Remove: []domain.ProviderLabelID{"work"}}, BeforeLabels: []domain.ProviderLabelID{"work", "INBOX"}}}}
}
func createAction(t *testing.T, s *Store, plan domain.MailboxAction) domain.ActionID {
	t.Helper()
	id, err := seedLegacyAction(context.Background(), s, plan)
	if err != nil {
		t.Fatal(err)
	}
	return id
}
func appliedUpdate(id domain.EmailID) repository.ActionItemUpdate {
	after := []domain.ProviderLabelID{"INBOX"}
	return repository.ActionItemUpdate{EmailID: id, State: domain.ItemApplied, ActualDelta: &domain.LabelDelta{Remove: []domain.ProviderLabelID{"work"}}, AfterLabels: &after}
}

func TestActionJournalSurvivesRestartAndSupportsUndo(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	s, err := Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	a := account(t, s, "a")
	e := sampleEmail(a, "message")
	emailID := saveEmail(t, s, e)
	plan := actionPlan(a, emailID, e.ProviderID)
	actionID := createAction(t, s, plan)
	initial, err := s.MailboxActions().Get(ctx, a, actionID)
	if err != nil {
		t.Fatal(err)
	}
	if initial.PlanDigest == "" || initial.Revision != 0 || initial.Status != domain.ActionPreview || initial.Items[0].ActualDelta != nil {
		t.Fatal("invalid initial journal")
	}
	if plan.Items[0].BeforeLabels[0] != "work" {
		t.Fatal("repository mutated caller snapshot")
	}
	plan.ID = actionID
	if _, err := seedLegacyAction(ctx, s, plan); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("duplicate plan overwrote history", err)
	}
	if err := s.MailboxActions().Transition(ctx, a, actionID, 0, domain.ActionApplying); err != nil {
		t.Fatal(err)
	}
	if err := s.MailboxActions().UpdateItem(ctx, a, actionID, 0, repository.ActionItemUpdate{EmailID: emailID, State: domain.ItemInFlight}); !errors.Is(err, repository.ErrConflict) {
		t.Fatal("stale worker accepted", err)
	}
	if err := s.MailboxActions().UpdateItem(ctx, a, actionID, 1, repository.ActionItemUpdate{EmailID: emailID, State: domain.ItemInFlight}); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	s, err = Open(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	recoverable, err := s.MailboxActions().ListRecoverable(ctx, a, repository.Page{Limit: 10})
	if err != nil || len(recoverable) != 1 || recoverable[0].Items[0].State != domain.ItemInFlight {
		t.Fatal("crash recovery information lost", err)
	}
	if err := s.MailboxActions().Transition(ctx, a, actionID, 2, domain.ActionApplied); !errors.Is(err, repository.ErrInvalid) {
		t.Fatal("in-flight write reported as complete")
	}
	if err := s.MailboxActions().UpdateItem(ctx, a, actionID, 2, appliedUpdate(emailID)); err != nil {
		t.Fatal(err)
	}
	if err := s.MailboxActions().Transition(ctx, a, actionID, 3, domain.ActionApplied); err != nil {
		t.Fatal(err)
	}
	final, err := s.MailboxActions().Get(ctx, a, actionID)
	if err != nil {
		t.Fatal(err)
	}
	if final.PlanDigest != initial.PlanDigest || !reflect.DeepEqual(final.Items[0].RequestedDelta, initial.Items[0].RequestedDelta) || !reflect.DeepEqual(final.Items[0].BeforeLabels, initial.Items[0].BeforeLabels) {
		t.Fatal("plan was mutated")
	}
	if final.ConfirmedAt == nil || final.CompletedAt == nil || final.Items[0].AttemptCount != 1 || final.Items[0].ActualDelta == nil || final.Items[0].AfterLabels == nil {
		t.Fatal("undo evidence incomplete")
	}
	e.BodyText = "new cached text"
	if saveEmail(t, s, e) != emailID {
		t.Fatal("sync broke journal reference")
	}
	if _, err := s.db.Exec("DELETE FROM emails WHERE id=?", emailID); err == nil {
		t.Fatal("journal email deleted without retention policy")
	}
	undo := actionPlan(a, emailID, e.ProviderID)
	undo.Kind = domain.ActionUndo
	undo.ReversesActionID = &actionID
	undo.Items[0].BeforeLabels = []domain.ProviderLabelID{"INBOX"}
	undo.Items[0].RequestedDelta = domain.LabelDelta{Add: []domain.ProviderLabelID{"work"}}
	undoID := createAction(t, s, undo)
	saved, err := s.MailboxActions().Get(ctx, a, undoID)
	if err != nil || saved.ReversesActionID == nil || *saved.ReversesActionID != actionID {
		t.Fatal("undo provenance lost", err)
	}
}

func TestActionCreationRollsBackAndEnforcesAccountOwnership(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := account(t, s, "a")
	b := account(t, s, "b")
	first := sampleEmail(a, "first")
	id := saveEmail(t, s, first)
	other := sampleEmail(b, "other")
	otherID := saveEmail(t, s, other)
	plan := actionPlan(a, id, first.ProviderID)
	plan.Items = append(plan.Items, actionPlan(b, otherID, other.ProviderID).Items[0])
	if _, err := seedLegacyAction(ctx, s, plan); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("cross-account action accepted", err)
	}
	if count(t, s, "mailbox_actions") != 0 || count(t, s, "mailbox_action_items") != 0 {
		t.Fatal("partial plan committed")
	}
	plan = actionPlan(a, id, first.ProviderID)
	plan.Items[0].ProviderMessageID = "wrong"
	if _, err := seedLegacyAction(ctx, s, plan); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("wrong remote identity accepted", err)
	}
	plan = actionPlan(a, id, first.ProviderID)
	plan.Items[0].RequestedDelta.Remove = []domain.ProviderLabelID{"INBOX"}
	if _, err := seedLegacyAction(ctx, s, plan); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("system-label mutation allowed", err)
	}
	actionID := createAction(t, s, actionPlan(a, id, first.ProviderID))
	if _, err := s.MailboxActions().Get(ctx, b, actionID); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("foreign action read", err)
	}
	if err := s.MailboxActions().Transition(ctx, b, actionID, 0, domain.ActionApplying); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("foreign action modified", err)
	}
	if err := s.MailboxActions().Transition(ctx, a, actionID, 0, domain.ActionCancelled); err != nil {
		t.Fatal(err)
	}
	undo := actionPlan(b, otherID, other.ProviderID)
	undo.Kind = domain.ActionUndo
	undo.ReversesActionID = &actionID
	if _, err := seedLegacyAction(ctx, s, undo); !errors.Is(err, repository.ErrNotFound) {
		t.Fatal("cross-account undo accepted", err)
	}
}

func TestUncertainActionRequiresReconciliation(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := account(t, s, "a")
	e := sampleEmail(a, "mail")
	id := saveEmail(t, s, e)
	actionID := createAction(t, s, actionPlan(a, id, e.ProviderID))
	if err := s.MailboxActions().Transition(ctx, a, actionID, 0, domain.ActionApplying); err != nil {
		t.Fatal(err)
	}
	if err := s.MailboxActions().UpdateItem(ctx, a, actionID, 1, repository.ActionItemUpdate{EmailID: id, State: domain.ItemInFlight}); err != nil {
		t.Fatal(err)
	}
	if err := s.MailboxActions().UpdateItem(ctx, a, actionID, 2, repository.ActionItemUpdate{EmailID: id, State: domain.ItemUncertain, LastErrorCode: "network_timeout"}); err != nil {
		t.Fatal(err)
	}
	if err := s.MailboxActions().Transition(ctx, a, actionID, 3, domain.ActionUncertain); err != nil {
		t.Fatal(err)
	}
	if err := s.MailboxActions().Transition(ctx, a, actionID, 4, domain.ActionCancelled); !errors.Is(err, repository.ErrInvalid) {
		t.Fatal("uncertain effects hidden as cancellation")
	}
	if err := s.MailboxActions().UpdateItem(ctx, a, actionID, 4, repository.ActionItemUpdate{EmailID: id, State: domain.ItemInFlight}); !errors.Is(err, repository.ErrInvalid) {
		t.Fatal("blind retry allowed")
	}
	bad := appliedUpdate(id)
	after := []domain.ProviderLabelID{}
	bad.AfterLabels = &after
	if err := s.MailboxActions().UpdateItem(ctx, a, actionID, 4, bad); !errors.Is(err, repository.ErrInvalid) {
		t.Fatal("unrelated label removal accepted")
	}
	bad = appliedUpdate(id)
	bad.ActualDelta = &domain.LabelDelta{}
	if err := s.MailboxActions().UpdateItem(ctx, a, actionID, 4, bad); !errors.Is(err, repository.ErrInvalid) {
		t.Fatal("incorrect actual delta accepted")
	}
	if err := s.MailboxActions().UpdateItem(ctx, a, actionID, 4, appliedUpdate(id)); err != nil {
		t.Fatal(err)
	}
	if err := s.MailboxActions().Transition(ctx, a, actionID, 5, domain.ActionApplied); err != nil {
		t.Fatal(err)
	}
	recovered, err := s.MailboxActions().ListRecoverable(ctx, a, repository.Page{Limit: 10})
	if err != nil || len(recovered) != 0 {
		t.Fatal("completed action still in recovery list", err)
	}
}

func TestPartialActionCannotBeReportedAsFullyApplied(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := account(t, s, "a")
	first := sampleEmail(a, "one")
	second := sampleEmail(a, "two")
	id := saveEmail(t, s, first)
	other := saveEmail(t, s, second)
	plan := actionPlan(a, id, first.ProviderID)
	plan.Items = append(plan.Items, actionPlan(a, other, second.ProviderID).Items[0])
	actionID := createAction(t, s, plan)
	if err := s.MailboxActions().Transition(ctx, a, actionID, 0, domain.ActionApplying); err != nil {
		t.Fatal(err)
	}
	if err := s.MailboxActions().UpdateItem(ctx, a, actionID, 1, repository.ActionItemUpdate{EmailID: id, State: domain.ItemInFlight}); err != nil {
		t.Fatal(err)
	}
	if err := s.MailboxActions().UpdateItem(ctx, a, actionID, 2, appliedUpdate(id)); err != nil {
		t.Fatal(err)
	}
	if err := s.MailboxActions().UpdateItem(ctx, a, actionID, 3, repository.ActionItemUpdate{EmailID: other, State: domain.ItemConflict, LastErrorCode: "stale_preview"}); err != nil {
		t.Fatal(err)
	}
	if err := s.MailboxActions().Transition(ctx, a, actionID, 4, domain.ActionApplied); !errors.Is(err, repository.ErrInvalid) {
		t.Fatal("partial action reported as complete")
	}
	if err := s.MailboxActions().Transition(ctx, a, actionID, 4, domain.ActionPartial); err != nil {
		t.Fatal(err)
	}
}

func TestExpiredPreviewAndConcurrentWorkers(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := account(t, s, "a")
	e := sampleEmail(a, "mail")
	id := saveEmail(t, s, e)
	expired := createAction(t, s, actionPlan(a, id, e.ProviderID))
	now := nowMillis()
	if _, err := s.db.Exec("UPDATE mailbox_actions SET created_at=?,expires_at=? WHERE id=?", now-10000, now-5000, expired); err != nil {
		t.Fatal(err)
	}
	if err := s.MailboxActions().Transition(ctx, a, expired, 0, domain.ActionApplying); !errors.Is(err, repository.ErrInvalid) {
		t.Fatal("expired preview confirmed")
	}
	if err := s.MailboxActions().Transition(ctx, a, expired, 0, domain.ActionExpired); err != nil {
		t.Fatal(err)
	}
	actionID := createAction(t, s, actionPlan(a, id, e.ProviderID))
	if err := s.MailboxActions().Transition(ctx, a, actionID, 0, domain.ActionApplying); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- s.MailboxActions().UpdateItem(ctx, a, actionID, 1, repository.ActionItemUpdate{EmailID: id, State: domain.ItemInFlight})
		}()
	}
	wg.Wait()
	close(errs)
	successes, conflicts := 0, 0
	for err := range errs {
		if err == nil {
			successes++
		} else if errors.Is(err, repository.ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatal("competing journal writers were not serialized")
	}
}

func TestActionValidationRejectsContradictoryPlans(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	a := account(t, s, "a")
	e := sampleEmail(a, "mail")
	id := saveEmail(t, s, e)
	for _, change := range []func(*domain.MailboxAction){
		func(a *domain.MailboxAction) { a.Items = append(a.Items, a.Items[0]) },
		func(a *domain.MailboxAction) { a.Items[0].RequestedDelta.Add = []domain.ProviderLabelID{"work"} },
		func(a *domain.MailboxAction) { a.Items[0].BeforeLabels = []domain.ProviderLabelID{"work", "work"} },
		func(a *domain.MailboxAction) { a.Items[0].ActualDelta = &domain.LabelDelta{} },
		func(a *domain.MailboxAction) { a.ExpiresAt = time.Time{} },
		func(a *domain.MailboxAction) { a.Kind = domain.ActionUndo },
	} {
		plan := actionPlan(a, id, e.ProviderID)
		change(&plan)
		if _, err := seedLegacyAction(ctx, s, plan); !errors.Is(err, repository.ErrInvalid) {
			t.Fatal("invalid plan accepted", err)
		}
	}
	if count(t, s, "mailbox_actions") != 0 {
		t.Fatal("invalid plans persisted")
	}
}

func TestUnencodableActionCannotCreateDigest(t *testing.T) {
	s := testStore(t)
	a := account(t, s, "encoding")
	e := sampleEmail(a, "message")
	id := saveEmail(t, s, e)
	plan := actionPlan(a, id, e.ProviderID)
	plan.ExpiresAt = time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := seedLegacyAction(context.Background(), s, plan); !errors.Is(err, repository.ErrInvalid) {
		t.Fatal("unencodable plan accepted")
	}
	if count(t, s, "mailbox_actions") != 0 {
		t.Fatal("invalid plan persisted")
	}
}

// seedLegacyAction reproduces the old producer only for synthetic compatibility
// fixtures. No runtime API can create this obsolete journal plan shape.
func seedLegacyAction(ctx context.Context, store *Store, a domain.MailboxAction) (domain.ActionID, error) {
	now := nowMillis()
	if a.AccountID == "" || len(a.Items) == 0 || len(a.Items) > 100 || a.ExpiresAt.UnixMilli() <= now {
		return "", repository.ErrInvalid
	}
	if a.Status != "" && a.Status != domain.ActionPreview {
		return "", repository.ErrInvalid
	}
	if (a.Kind != domain.ActionLabelDelta && a.Kind != domain.ActionUndo) || (a.Kind == domain.ActionUndo) != (a.ReversesActionID != nil) {
		return "", repository.ErrInvalid
	}
	if a.ID == "" {
		a.ID = domain.ActionID(newID())
	}
	// Copy before normalization: repositories never mutate caller-owned slices.
	a.Items = append([]domain.MailboxActionItem{}, a.Items...)
	seen := map[domain.EmailID]bool{}
	for i := range a.Items {
		item := &a.Items[i]
		if item.EmailID == "" || item.ProviderMessageID == "" || seen[item.EmailID] || item.ActualDelta != nil || item.AfterLabels != nil || item.AttemptCount != 0 || item.LastErrorCode != "" || (item.State != "" && item.State != domain.ItemPending) {
			return "", repository.ErrInvalid
		}
		seen[item.EmailID] = true
		var err error
		item.RequestedDelta, err = normalizeDelta(item.RequestedDelta)
		if err != nil {
			return "", err
		}
		if len(item.RequestedDelta.Add)+len(item.RequestedDelta.Remove) == 0 {
			return "", repository.ErrInvalid
		}
		item.BeforeLabels, err = labelSet(item.BeforeLabels)
		if err != nil {
			return "", err
		}
		item.State = domain.ItemPending
		item.UpdatedAt = instant(now)
	}
	slices.SortFunc(a.Items, func(x, y domain.MailboxActionItem) int {
		if x.EmailID < y.EmailID {
			return -1
		}
		if x.EmailID > y.EmailID {
			return 1
		}
		return 0
	})
	a.Status = domain.ActionPreview
	a.CreatedAt = instant(now)
	a.ExpiresAt = instant(a.ExpiresAt.UnixMilli())
	a.ConfirmedAt = nil
	a.CompletedAt = nil
	a.Revision = 0
	a.PlanDigest = ""
	plan, err := encode(a)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256([]byte(plan))
	a.PlanDigest = hex.EncodeToString(sum[:])
	err = store.transaction(ctx, func(tx *sql.Tx) error {
		if a.ReversesActionID != nil {
			if *a.ReversesActionID == a.ID {
				return repository.ErrInvalid
			}
			if err := exists(ctx, tx, `SELECT 1 FROM mailbox_actions WHERE account_id=? AND id=? AND status IN ('applied','partial','cancelled')`, a.AccountID, *a.ReversesActionID); err != nil {
				return err
			}
		}
		var reverses any
		if a.ReversesActionID != nil {
			reverses = string(*a.ReversesActionID)
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO mailbox_actions(id,account_id,kind,reverses_action_id,status,plan_digest,revision,created_at,expires_at) VALUES(?,?,?,?,?,?,0,?,?)`, a.ID, a.AccountID, a.Kind, reverses, a.Status, a.PlanDigest, now, a.ExpiresAt.UnixMilli())
		if err != nil {
			return err
		}
		for _, item := range a.Items {
			if err := exists(ctx, tx, "SELECT 1 FROM emails WHERE remote_deleted=0 AND account_id=? AND id=? AND provider_message_id=?", a.AccountID, item.EmailID, item.ProviderMessageID); err != nil {
				return err
			}
			for _, ids := range [][]domain.ProviderLabelID{item.RequestedDelta.Add, item.RequestedDelta.Remove} {
				for _, id := range ids {
					if err := exists(ctx, tx, "SELECT 1 FROM labels WHERE account_id=? AND provider_label_id=? AND kind='user'", a.AccountID, id); err != nil {
						return err
					}
				}
			}
			requested, err := encode(item.RequestedDelta)
			if err != nil {
				return err
			}
			before, err := encode(item.BeforeLabels)
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx, `INSERT INTO mailbox_action_items(action_id,account_id,email_id,provider_message_id,requested_delta_json,before_labels_json,state,updated_at) VALUES(?,?,?,?,?,?,?,?)`, a.ID, a.AccountID, item.EmailID, item.ProviderMessageID, requested, before, domain.ItemPending, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	return a.ID, nil
}

func TestLegacyJournalCannotCreateNewRuntimePlans(t *testing.T) {
	legacy := reflect.TypeFor[repository.MailboxActionRepository]()
	if _, ok := legacy.MethodByName("Create"); ok {
		t.Fatal("obsolete generic plan producer returned to the runtime contract")
	}
	current := reflect.TypeFor[repository.OrganizationRepository]()
	if _, ok := current.MethodByName("Create"); !ok {
		t.Fatal("reviewed organization plan creation was removed")
	}
}
