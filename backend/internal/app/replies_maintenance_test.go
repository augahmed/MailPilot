package app

import (
	"context"
	"testing"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/provider"
)

func TestMaintenanceRejectsNewWorkAndDoesNotRestoreHistoricalPreviews(t *testing.T) {
	legacy, db, fake, mailbox := replyFixture(t)
	preview := previewDraft(t, legacy) // synthetic historical record, no Gmail write
	before := fake.count()
	s := NewReplyMaintenance(context.Background(), legacy.session, db.Replies(), db.AISettings(),
		func(domain.AccountID) provider.DraftFinder { return mailbox }, legacy.keys, legacy.aiFactory)
	t.Cleanup(s.Close)
	application := &App{Replies: s}
	r := application.Reply(context.Background(), ReplyRequest{Operation: "status"})
	if r.ActionID != preview.ActionID || r.State != "draft_preview" || r.PreviewID != "" || r.ReviewID != "" || s.reviewID != "" || s.selected != "" {
		t.Fatal("historical preview became an editable/creatable session")
	}
	for _, p := range []ReplyRequest{
		{Operation: "prepare", AccountID: "a", EmailID: "selected", IncludeThread: true, Tone: "Friendly", Instruction: "arbitrary"},
		{Operation: "generate", AccountID: "a", PreviewID: "p", Approved: true},
		{Operation: "preview_draft", AccountID: "a", PreviewID: "p", Body: "body"},
		{Operation: "create", AccountID: "a", ActionID: preview.ActionID, PreviewID: preview.PreviewID, Approved: true},
		{Operation: "summarize_thread", AccountID: "a"},
		{Operation: "chat", AccountID: "a", Instruction: "arbitrary"},
	} {
		if r := application.Reply(context.Background(), p); r.Code != "validation" {
			t.Fatal("retired application operation accepted", p.Operation)
		}
		if r := s.Handle(context.Background(), p); r.Code != "validation" {
			t.Fatal("maintenance service exposed legacy implementation", p.Operation)
		}
	}
	if fake.count() != before || mailbox.creates.Load() != 0 || mailbox.finds.Load() != 0 {
		t.Fatal("rejected operation dispatched external work")
	}
}

func TestMaintenanceRecoversWithoutAIAndNeverCreatesDrafts(t *testing.T) {
	legacy, db, fake, mailbox := replyFixture(t)
	p := previewDraft(t, legacy)
	ctx := context.Background()
	if err := db.Replies().Transition(ctx, "a", p.ActionID, "preview", "in_flight", provider.DraftReference{}, "", false); err != nil {
		t.Fatal(err)
	}
	before := fake.count()
	s := NewReplyMaintenance(ctx, legacy.session, db.Replies(), db.AISettings(),
		func(domain.AccountID) provider.DraftFinder { return mailbox }, nil, nil)
	t.Cleanup(s.Close)
	// No AI adapter/key factory is needed for historical outcome checks.
	r := s.HandleMaintenance(ctx, ReplyRequest{Operation: "status"})
	if r.State != "uncertain" || r.ActionID != p.ActionID {
		t.Fatal("interrupted action hidden")
	}
	if r := s.HandleMaintenance(ctx, ReplyRequest{Operation: "reconcile", AccountID: "b", ActionID: p.ActionID}); r.Code != "unauthorized" {
		t.Fatal("cross-account recovery accepted")
	}
	if r := s.HandleMaintenance(ctx, ReplyRequest{Operation: "resolve", AccountID: "a", ActionID: p.ActionID, Approved: true}); r.Code != "reconcile_required" {
		t.Fatal("resolved before reconciliation")
	}
	s.HandleMaintenance(ctx, ReplyRequest{Operation: "reconcile", AccountID: "a", ActionID: p.ActionID})
	r = waitReply(t, s)
	if r.State != "created" || !r.Reconciled || mailbox.finds.Load() != 1 || mailbox.creates.Load() != 0 || fake.count() != before {
		t.Fatal("legacy reconciliation generated or wrote mail", r.State, r.Code)
	}
}
