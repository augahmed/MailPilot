package embedding

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"mailpilot.local/core/internal/domain"
)

func TestPrepare(t *testing.T) {
	for _, body := range []string{
		"Hello  world\r\n\r\nOn Monday, Example wrote:\r\nold chain",
		"Hello world\n> old chain",
		"Hello world\n-- \nExample signature",
		"Hello world\n-----Original Message-----\nold chain",
		"Hello world\n---------- Forwarded message ---------\nold chain",
		"Hello world\nSent from my iPhone\nfooter",
	} {
		e := domain.Email{Subject: "  Test\nsubject ", Sender: domain.EmailAddress{Name: "Example", Address: "sender@example.test"}, BodyText: body}
		p, err := Prepare(e)
		if err != nil || p.Text != "Subject: Test subject\nSender: Example <sender@example.test>\nBody:\nHello world" {
			t.Fatalf("unexpected preparation: %q %v", p.Text, err)
		}
		other := e
		other.BodyText = "Hello world"
		other.Recipients = []domain.EmailAddress{{Address: "ignored@example.test"}}
		again, _ := Prepare(other)
		if p != again {
			t.Fatal("excluded content changed fingerprint")
		}
	}
}

func TestPrepareHTMLBoundsAndMissingBody(t *testing.T) {
	html := `<style>hidden</style><p>Hello &amp; welcome</p><script>hidden</script><img src="https://example.test/pixel"><blockquote>hidden quote</blockquote><div class="gmail_signature">hidden signature</div>`
	e := domain.Email{BodyHTML: &html}
	p, err := Prepare(e)
	if err != nil || !strings.HasSuffix(p.Text, "Hello & welcome") || strings.Contains(p.Text, "hidden") || strings.Contains(p.Text, "pixel") {
		t.Fatal(p, err)
	}
	e.BodyText = "Prefer plain text"
	p, _ = Prepare(e)
	if !strings.HasSuffix(p.Text, e.BodyText) {
		t.Fatal("did not prefer plain text")
	}
	e.BodyText = strings.Repeat("猫", 20000)
	p, err = Prepare(e)
	if err != nil || len(p.Text) > MaxTextBytes || !utf8.ValidString(p.Text) {
		t.Fatal("invalid bounded output")
	}
	e.BodyState = "unavailable"
	if _, err := Prepare(e); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	e.BodyState = ""
	e.BodyText = string([]byte{0xff})
	if _, err := Prepare(e); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
	e.BodyText = strings.Repeat("a", 1024*1024+1)
	if _, err := Prepare(e); !errors.Is(err, ErrInput) {
		t.Fatal(err)
	}
}

func TestPreparePreservesInlineRepliesAndOrdinaryClosings(t *testing.T) {
	p, _ := Prepare(domain.Email{BodyText: "> question\nMy reply\n> next question\nNext reply\nThanks for your help"})
	if !strings.HasSuffix(p.Text, "My reply\nNext reply\nThanks for your help") {
		t.Fatal(p.Text)
	}
}
