package demo

import (
	"context"
	"errors"
	"time"

	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
)

var samples = []struct{ id, sender, subject, body string }{
	{"application", "hiring@acorn.example.test", "Application received: software engineer", "We received your application for the software engineer position. Our hiring team will review it this week. This is synthetic demonstration mail."},
	{"interview", "recruiter@acorn.example.test", "Interview invitation: software engineer", "We would like to schedule an interview for the software engineer position. Your interview is on October 28 at 14:00 UTC. Please confirm your availability."},
	{"receipt", "receipts@shop.example.test", "Your receipt for order MP-1042", "Thank you for your purchase. Order MP-1042: desk lamp, $28.00; delivery, $4.00. Total paid: $32.00. Keep this receipt for your records."},
	{"flight", "bookings@airline.example.test", "Flight confirmation: Boston to Chicago", "Your flight reservation is confirmed. Booking reference DEMO42. Depart Boston on November 3 at 09:15; arrive Chicago at 11:05. Carry-on baggage is included. This itinerary is fictional."},
	{"newsletter", "weekly@science.example.test", "Science weekly newsletter", "Welcome to our weekly newsletter. This edition covers ocean research and community science events. You subscribed to this mailing list. Unsubscribe from future newsletters in your preferences."},
	{"meeting", "maya@team.example.test", "Project Atlas planning notes", "Project Atlas review is Thursday at 10:00 UTC in room Cedar. Bring the accessibility checklist and the revised rollout plan. The prototype milestone is November 6."},
	{"invoice", "billing@studio.example.test", "Invoice paid: design workshop", "Payment received for invoice INV-DEMO-52. Your design workshop receipt totals $85.00. The workshop begins November 12 at 16:00 UTC."},
	{"delivery", "updates@parcel.example.test", "Your parcel arrives Friday", "Your desk lamp order MP-1042 is out for delivery on Friday. Tracking reference DEMO-PARCEL-7. No action or payment is required."},
	{"garden", "neighbors@community.example.test", "Community garden this weekend", "Our community garden volunteers meet Saturday at 09:00 near the library. Bring gloves and a refillable water bottle. We will plant herbs and repair the raised beds."},
	{"security", "support@account-review.example.test", "URGENT: verify your account immediately", "Synthetic phishing example. Your account will be suspended in 24 hours! Verify your password immediately at https://account-verification.example.test/login. Never follow this fictional request or submit a password. The app must display it as untrusted text."},
	{"forwarded", "sam@team.example.test", "Fwd: workshop travel details", "Forwarded message\nFrom: coordinator@events.example.test\nSubject: Workshop venue\nThe workshop is at Harbor Hall, 17 Example Street. Registration opens at 08:30 UTC.\n\nSam: Please keep the original venue information for our travel plan."},
	{"reader", "demo@welcome.example.test", "Welcome to your synthetic MailPilot workspace", "Every message in this profile is fictional. Try keyword search for MP-1042, flight, or Atlas. Start Local Email Index explicitly for real local semantic/hybrid retrieval. External AI and Gmail operations are disabled. Text such as <img src=\"https://pixel.example.test\"> stays escaped and loads no remote content."},
}

// Seed uses current repositories and chunk preparation. Restarts preserve edits;
// reset restores only bundled sample messages. It never
// removes mail, credentials, derived indexes or immutable recovery journals.
func Seed(ctx context.Context, accounts repository.AccountRepository, emails repository.EmailRepository, prepare func(context.Context, domain.AccountID, domain.ProviderMessageID) error, reset bool) error {
	if _, err := accounts.Upsert(ctx, domain.Account{ID: AccountID, Provider: "synthetic", ProviderAccountID: string(AccountID), Address: domain.EmailAddress{Name: "Synthetic pilot", Address: "pilot@example.test"}}); err != nil {
		return err
	}

	for i, f := range samples {
		pid := domain.ProviderMessageID("sample-" + f.id)
		_, err := emails.GetByProviderID(ctx, AccountID, pid)
		if errors.Is(err, repository.ErrNotFound) || reset && err == nil {
			e := domain.Email{ID: domain.EmailID("demo-" + f.id), AccountID: AccountID, ProviderID: pid, ProviderThreadID: domain.ProviderThreadID("thread-" + f.id), Sender: domain.EmailAddress{Address: f.sender}, Recipients: []domain.EmailAddress{{Address: "pilot@example.test"}}, Subject: f.subject, BodyText: f.body, BodyState: "complete", ContentRevision: "synthetic-v1", ReceivedAt: time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC).Add(-time.Duration(i) * time.Hour), Labels: []domain.EmailLabel{Inbox()}, Headers: []domain.EmailHeader{{Name: "X-MailPilot-Synthetic", Value: "true"}}}
			_, err = emails.Upsert(ctx, e)
		}
		if err != nil {
			return err
		}
		if err = prepare(ctx, AccountID, pid); err != nil {
			return err
		}
	}
	return nil
}

func Inbox() domain.EmailLabel {
	return domain.EmailLabel{AccountID: AccountID, ProviderID: "DEMO_INBOX", Name: "INBOX", Kind: domain.LabelKindSystem}
}

// Mailbox supplies cached sample snapshots for the existing label-preview logic.
// It has no token source, network client or mutation implementation.
type Mailbox struct {
	Account domain.AccountID
	Emails  repository.EmailRepository
}

func (p *Mailbox) FetchMessage(ctx context.Context, id domain.ProviderMessageID) (domain.Email, error) {
	if p.Account != AccountID {
		return domain.Email{}, repository.ErrInvalid
	}
	r, err := p.Emails.GetByProviderID(ctx, AccountID, id)
	return r.Email, err
}
