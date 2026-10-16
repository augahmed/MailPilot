package mail

import (
	"mailpilot.local/core/internal/domain"
	"strings"
	"testing"
)

func TestCleanBodyPreservesCurrentEvidence(t *testing.T) {
	for _, tail := range []string{"\n> old chain", "\nOn Monday, Example wrote:\nold chain", "\n-- \nSignature", "\n-----Original Message-----\nold chain", "\n---------- Forwarded message ---------\nold chain", "\nSent from my iPhone\nfooter"} {
		got, err := CleanBody(domain.Email{BodyText: "Hello  world\r\n" + tail})
		if err != nil || got != "Hello world" {
			t.Fatalf("unexpected cleanup %q %v", got, err)
		}
	}
	got, err := CleanBody(domain.Email{BodyText: "> question\nMy reply\n> next question\nNext reply\nThanks for your help"})
	if err != nil || got != "My reply\nNext reply\nThanks for your help" {
		t.Fatal(got, err)
	}
}

func TestCleanBodyHTMLAndLimits(t *testing.T) {
	html := `<style>hidden</style><p>Hello &amp; welcome</p><script>hidden</script><img src="https://example.test/pixel"><blockquote>hidden quote</blockquote><div class="gmail_signature">hidden signature</div>`
	got, err := CleanBody(domain.Email{BodyHTML: &html})
	if err != nil || got != "Hello & welcome" {
		t.Fatal(got, err)
	}
	got, err = CleanBody(domain.Email{BodyText: "Prefer plain text", BodyHTML: &html})
	if err != nil || got != "Prefer plain text" {
		t.Fatal(got, err)
	}
	for _, value := range []string{string([]byte{0xff}), strings.Repeat("a", MaxBodyBytes+1)} {
		if _, err := CleanBody(domain.Email{BodyText: value}); err != ErrContent {
			t.Fatal(err)
		}
		if _, err := CleanBody(domain.Email{BodyHTML: &value}); err != ErrContent {
			t.Fatal(err)
		}
	}
	deep := strings.Repeat("<div>", 110) + "text" + strings.Repeat("</div>", 110)
	if _, err := CleanBody(domain.Email{BodyHTML: &deep}); err != ErrContent {
		t.Fatal("unbounded HTML depth", err)
	}
}
