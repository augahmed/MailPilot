package reply

import (
	"mailpilot.local/core/internal/domain"
	"mailpilot.local/core/internal/repository"
	"strings"
	"testing"
	"time"
)

func row(id string) repository.StoredEmail {
	return repository.StoredEmail{Email: domain.Email{AccountID: "a", ID: domain.EmailID(id), ProviderThreadID: "thread", BodyState: "complete", BodyText: "Relevant meeting request.\n> QUOTED_PRIVATE\n-- \nSIGNATURE_PRIVATE", Subject: "Hello", Sender: domain.EmailAddress{Address: "sender@example.test"}, Headers: []domain.EmailHeader{{Name: "Message-ID", Value: "<parent@example.test>"}}, ReceivedAt: time.Now()}, ContentHash: strings.Repeat("a", 64)}
}
func TestBoundedContextTonesAndHostileMetadata(t *testing.T) {
	for _, tone := range []string{"Professional", "Friendly", "Concise", "Formal"} {
		request, sources, draft, _, e := Build([]repository.StoredEmail{row("selected")}, "selected", tone, "")
		if e != nil || len(sources) != 1 || draft.Subject != "Re: Hello" || !strings.Contains(request.Instruction.Reveal(), tone) {
			t.Fatal("tone/default instructions", e)
		}
		for _, hidden := range []string{"QUOTED_PRIVATE", "SIGNATURE_PRIVATE"} {
			if strings.Contains(request.Context.Reveal(), hidden) {
				t.Fatal("unnecessary context")
			}
		}
	}
	for _, mutation := range []func(*domain.Email){func(e *domain.Email) { e.BodyState = "unavailable" }, func(e *domain.Email) { e.Subject = "Bad\r\nBcc: attacker@example.test" }, func(e *domain.Email) { e.Headers = []domain.EmailHeader{{Name: "Message-ID", Value: "bad-id"}} }, func(e *domain.Email) {
		e.Headers = append(e.Headers, domain.EmailHeader{Name: "Reply-To", Value: "one@example.test, two@example.test"})
	}, func(e *domain.Email) { e.Sender.Address = "bad" }} {
		r := row("selected")
		mutation(&r.Email)
		if _, _, _, _, e := Build([]repository.StoredEmail{r}, "selected", "Formal", ""); e == nil {
			t.Fatal("unsafe metadata accepted")
		}
	}
	a, b := row("a"), row("selected")
	b.Email.AccountID = "b"
	if _, _, _, _, e := Build([]repository.StoredEmail{a, b}, "selected", "Concise", ""); e == nil {
		t.Fatal("cross-account context")
	}
	b = row("selected")
	b.Email.ProviderThreadID = "foreign"
	if _, _, _, _, e := Build([]repository.StoredEmail{a, b}, "selected", "Concise", ""); e == nil {
		t.Fatal("unrelated thread")
	}
	var rows []repository.StoredEmail
	for _, id := range []string{"1", "2", "3", "4", "selected"} {
		r := row(id)
		r.Email.BodyText = strings.Repeat("界", 3000)
		rows = append(rows, r)
	}
	r, _, _, truncated, e := Build(rows, "selected", "Formal", strings.Repeat("x", 1000))
	if e != nil || !truncated || len(r.Context.Reveal()) > 6000 || r.Validate() != nil {
		t.Fatal("bounded Unicode context", e)
	}
}
