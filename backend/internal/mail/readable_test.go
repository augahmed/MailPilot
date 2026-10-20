package mail

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"mailpilot.local/core/internal/domain"
)

func newsletterText() string {
	tracking := "<https://click.mail.example.test/deeplink/?qs=" + strings.Repeat("SyntheticTracking", 12) + "&screen=Deals>"
	return "Double rewards run October 5 to October 16.\n\n  [image: Example Bank logo]\n" + tracking + "\n\n[image: Monday 10/5 to Friday 10/16]\n[image: Monday 10/5 to Friday 10/16]\n[image: Earn 2x cash back on selected deals]\n" + tracking + "\n[image: Earn 2x cash back on selected deals]\n" + tracking + "\n\nThank you for being a client.\nYour offers:*¹* <#m_-123_footnote_num1>\n[image: Northshop]\n" + tracking + "\n[image: Activate your Northshop offer]\n" + tracking + "\n\nNorthshop\n\nSave 14%\n"
}
func TestReadableNewsletterIsSharedWithChunksAndPreservesCache(t *testing.T) {
	stored := chunkMail(newsletterText())
	before := stored
	body, format, err := ReadableBody(context.Background(), stored.Email)
	if err != nil || format != "plain_text" {
		t.Fatal(err, format)
	}
	for _, noise := range []string{"[image:", "SyntheticTracking", "?qs=", "click.mail.example.test", "Example Bank logo", "<#m_"} {
		if strings.Contains(body, noise) {
			t.Fatalf("newsletter artifact retained: %s", noise)
		}
	}
	for _, fact := range []string{"October 5 to October 16", "Monday 10/5 to Friday 10/16", "Earn 2x cash back", "Northshop", "Save 14%", "Your offers:*¹*", "Example Bank"} {
		if !strings.Contains(body, fact) {
			t.Fatalf("meaningful content lost: %s", fact)
		}
	}
	if strings.Count(body, "Monday 10/5 to Friday 10/16") != 1 || strings.Count(body, "Earn 2x cash back on selected deals") != 1 || strings.Count(body, "\nNorthshop\n") != 1 {
		t.Fatal("responsive image/text alternatives duplicated")
	}
	snapshot := buildChunks(t, stored)
	for _, c := range snapshot.Chunks {
		input := ModelInput(c)
		if strings.Contains(input, "[image:") || strings.Contains(input, "?qs=") || strings.Contains(input, "SyntheticTracking") {
			t.Fatal("raw transport artifacts reached model input")
		}
	}
	if !reflect.DeepEqual(stored, before) {
		t.Fatal("cleaning overwrote cached source")
	}
	again, _, err := ReadableBody(context.Background(), domain.Email{BodyText: body, BodyState: "complete"})
	if err != nil || body != again {
		t.Fatal("text cleanup is not deterministic/idempotent", err)
	}
}
func TestReadableHTMLRetainsImageOnlyEvidenceAndOmitsHiddenContent(t *testing.T) {
	html := `<style>hidden-css</style><script>hidden-script</script><div style="DISPLAY: none !important">hidden-preheader</div><div hidden>hidden-attribute</div><div aria-hidden="true">hidden-accessibility</div><p>Selected offers:</p><a href="https://click.example.test/?qs=opaque"><img alt="Example Bank logo" src="https://tracking.example.test/logo"></a><img alt="Dates: October 5–16"><img alt="Dates: October 5–16"><img alt="Use code SAVE14"><img alt="Northshop"><p>Northshop</p><p>Save 14% &amp; earn rewards.</p><blockquote><img alt="Unique quoted image evidence"></blockquote><div class="gmail_signature">Sam</div>`
	stored := chunkMail("")
	stored.Email.BodyHTML = &html
	body, format, err := ReadableBody(context.Background(), stored.Email)
	if err != nil || format != "html_text" {
		t.Fatal(err, format)
	}
	for _, noise := range []string{"hidden-", "http", "<img", "<script", "Example Bank logo", "?qs="} {
		if strings.Contains(body, noise) {
			t.Fatalf("HTML transport/hidden content retained: %s", noise)
		}
	}
	for _, fact := range []string{"Dates: October 5–16", "Use code SAVE14", "Save 14% & earn rewards.", "Unique quoted image evidence", "Sam"} {
		if !strings.Contains(body, fact) {
			t.Fatalf("image-only/visible evidence lost: %s", fact)
		}
	}
	if strings.Count(body, "Dates: October 5–16") != 1 || strings.Count(body, "Northshop") != 1 {
		t.Fatal("image alt repeated alongside visible text")
	}
	// Existing HTML-only cache rows carry the old flattened text, including hidden
	// preheaders and missing useful image alt text. They must prefer clean HTML too.
	legacy, err := HTMLText(html)
	if err != nil {
		t.Fatal(err)
	}
	stored.Email.BodyText = legacy
	flattened, _, err := ReadableBody(context.Background(), stored.Email)
	if err != nil || flattened != body || strings.Contains(flattened, "hidden-") {
		t.Fatal("cached HTML fallback bypassed visible-text cleanup", err)
	}
	snapshot := buildChunks(t, stored)
	kinds := map[string]string{}
	for _, c := range bodyChunks(snapshot.Chunks) {
		kinds[c.SectionType] += c.Text
	}
	if !strings.Contains(kinds["quoted"], "Unique quoted image evidence") || !strings.Contains(kinds["signature"], "Sam") {
		t.Fatal("HTML provenance changed during cleanup")
	}
}
func TestReadableAlternativesKeepUniqueForwardsWithoutRepeatingBody(t *testing.T) {
	stored := chunkMail("[image: Dates: October 5–16]\n\nNorthshop\n\nSave 14%\n")
	html := `<img alt="Dates: October 5–16"><p>Northshop</p><p>Save 14%</p><div>---------- Forwarded message ---------</div><p>Unique forwarded appointment at noon.</p>`
	stored.Email.BodyHTML = &html
	body, _, err := ReadableBody(context.Background(), stored.Email)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(body, "Northshop") != 1 || strings.Count(body, "Save 14%") != 1 || strings.Count(body, "Dates: October 5–16") != 1 || !strings.Contains(body, "Unique forwarded appointment at noon.") {
		t.Fatal("MIME alternative duplicated or unique forward lost")
	}
	snapshot := buildChunks(t, stored)
	forward := ""
	for _, c := range snapshot.Chunks {
		if c.SectionType == "forwarded" {
			forward += c.Text
		}
	}
	if !strings.Contains(forward, "appointment at noon") {
		t.Fatal("forward classification lost")
	}
}
func TestReadableURLsKeepIdentifiersAndAuthoredRepetition(t *testing.T) {
	raw := "Literal syntax: [image: icon] is an example.\nSee <https://docs.example.test/agenda?id=42&utm_source=email#terms>.\nConfirm\nConfirm\n[image: Confirm]\n> [image: Confirm]\n> Unique quoted context.\n---------- Forwarded message ---------\nhttps://docs.example.test/booking?reservation=ABC123\n-- \nSam"
	body, _, err := ReadableBody(context.Background(), domain.Email{BodyText: raw, BodyState: "complete"})
	if err != nil {
		t.Fatal(err)
	}
	for _, fact := range []string{"Literal syntax: [image: icon] is an example.", "https://docs.example.test/agenda?id=42#terms", "Confirm\nConfirm", " > Confirm", "reservation=ABC123", "-- \nSam"} {
		fact = strings.TrimPrefix(fact, " ")
		if !strings.Contains(body, fact) {
			t.Fatalf("ordinary URL/section/repetition lost: %s", fact)
		}
	}
	if strings.Contains(body, "utm_source") || strings.Contains(body, "[image: Confirm]") {
		t.Fatal("transport markup retained")
	}
	// URL-only mail is not turned into an empty body.
	only := "<https://click.example.test/u/?qs=" + strings.Repeat("Synthetic", 20) + ">"
	body, _, err = ReadableBody(context.Background(), domain.Email{BodyText: only, BodyState: "complete"})
	if err != nil || body != "https://click.example.test/u/" {
		t.Fatal("URL-only message lost", err)
	}
}
func TestReadableTextBoundsAndCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := ReadableBody(ctx, domain.Email{BodyText: newsletterText(), BodyState: "complete"}); !errors.Is(err, context.Canceled) {
		t.Fatal("cancel ignored", err)
	}
	if _, _, err := ReadableBody(context.Background(), domain.Email{BodyText: strings.Repeat("x", MaxBodyBytes+1)}); !errors.Is(err, ErrContent) {
		t.Fatal("size bound ignored", err)
	}
}

// Synthetic newsletter with wrapped plain text, emphasis and link annotations.
func TestReadableMultipartNewsletterUsesOneBody(t *testing.T) {
	plain := "Network with fellow graduates!\n\n*Careers grow through relationships. How do you build your\nprofessional network? *\n\n*Campus Connect\nhttps://click.engage.example.test/*\nis the free network for students to meet alumni for guidance\non careers and industry insights.\n\nYour community is ready to help!\nSign in\n\n*Looking for advice?* Send direct messages to alumni.\n*Seeking an internship?* Browse opportunities posted by alumni.\n*Trouble signing in?* Email support@example.test\n<support@example.test?utm_source=synthetic?subject=>\nfor support.\n\n*Landing an internship\nhttps://click.engage.example.test/*\n\nWhen *Jordan (Class ’25)* began looking, a connection with\n*Casey (Class ’13)*, helped secure an internship.\nRead more\n\n*The 80/20 job search\nhttps://click.engage.example.test/*\n\nSpend 20% on applications and 80% on networking.\nRead more\n\n*Does your information look up-to-date?*\nUpdate your info\n\nTrustees of Example University\n123 Campus Street\nCity, United States\n"
	html := `<div hidden>Hidden preheader</div><p>Careers grow through relationships. How do you build your professional network?</p><p><strong>Campus Connect</strong> is the free network for students to meet alumni for guidance on careers and industry insights.</p><p>Your community is ready to help!<br>Sign in</p><p><b>Looking for advice?</b> Send direct messages to alumni.</p><p><b>Seeking an internship?</b> Browse opportunities posted by alumni.</p><p><b>Trouble signing in?</b> Email <a href="mailto:support@example.test">support@example.test</a> for support.</p><h2>Landing an internship</h2><p>When <strong>Jordan (Class ’25)</strong> began looking, a connection with <strong>Casey (Class ’13)</strong>, helped secure an internship.</p><p>Read more</p><h2>The 80/20 job search</h2><p>Spend 20% on applications and 80% on networking.</p><p>Read more</p><p>Does your information look up-to-date?</p><p>Update your info</p><p>Trustees of Example University<br>123 Campus Street<br>City, United States</p>`
	stored := chunkMail(plain)
	stored.Email.BodyHTML = &html
	body, format, err := ReadableBody(context.Background(), stored.Email)
	if err != nil || format != "html_text" {
		t.Fatal(err, format)
	}
	for _, fact := range []string{"Careers grow through relationships.", "Campus Connect", "Looking for advice?", "Seeking an internship?", "Trouble signing in?", "Landing an internship", "When Jordan (Class ’25)", "The 80/20 job search", "Spend 20%", "Does your information look up-to-date?", "Trustees of Example University", "Network with fellow graduates!"} {
		if strings.Count(body, fact) != 1 {
			t.Fatalf("missing or duplicated newsletter passage %q: %d", fact, strings.Count(body, fact))
		}
	}
	for _, noise := range []string{"*", "utm_", "click.engage", "Hidden preheader", "<support@"} {
		if strings.Contains(body, noise) {
			t.Fatalf("plain alternative artifact retained: %q", noise)
		}
	}
	if strings.Count(body, "Read more") != 2 {
		t.Fatal("distinct article actions were globally deduplicated")
	}
	snapshot := buildChunks(t, stored)
	all := ""
	for _, c := range bodyChunks(snapshot.Chunks) {
		all += c.Text
	}
	if strings.Count(all, "Careers grow through relationships.") != 1 || strings.Contains(all, "utm_") || strings.Contains(all, "*Campus") {
		t.Fatal("chunker did not use the same single cleaned body")
	}
	if stored.Email.BodyText != plain || *stored.Email.BodyHTML != html {
		t.Fatal("MIME source changed")
	}
}

func TestReadableMIMEUniqueSectionsAndPrimaryRepetitionSurvive(t *testing.T) {
	html := `<p>Confirm the agenda.</p><p>Confirm the agenda.</p><p>Article title</p><blockquote>Shared old context.</blockquote><div class="gmail_signature">Sam</div>`
	stored := chunkMail("*Confirm the agenda.*\n\nUnique plain-only booking: ABC123.\n\nOn Tuesday, Sam wrote:\nShared old context.\nUnique quote-only evidence.\n---------- Forwarded message ---------\nUnique forwarded evidence.")
	stored.Email.BodyHTML = &html
	snapshot := buildChunks(t, stored)
	p, err := Sections(context.Background(), stored.Email)
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]string{}
	for _, s := range p.Sections {
		kinds[s.Kind] += s.Text
	}
	if strings.Count(kinds["authored"], "Confirm the agenda.") != 2 || !strings.Contains(kinds["authored"], "ABC123") || !strings.Contains(kinds["quoted"], "Unique quote-only evidence.") || !strings.Contains(kinds["forwarded"], "Unique forwarded evidence.") || !strings.Contains(kinds["signature"], "Sam") || len(snapshot.Chunks) == 0 {
		t.Fatal("unique alternative evidence or primary repetition lost")
	}
	got, err := supplementAlternative(context.Background(), []Section{{Kind: "authored", Text: "Reconfirmation"}}, []Section{{Kind: "authored", Text: "confirmation", End: 12}})
	if err != nil || len(got) != 2 {
		t.Fatal("substring comparison removed distinct plain text")
	}
}

func TestMIMEComparisonPreservesMeaningfulDifferences(t *testing.T) {
	for _, text := range []string{"Cost is 2*3", "booking_A_B", "https://docs.example.test/booking?id=ABC123", "> Same content."} {
		got, err := supplementAlternative(context.Background(), []Section{{Kind: "authored", Text: "Cost is 23 bookingAB Same content."}}, []Section{{Kind: "quoted", Text: text, End: len(text)}})
		if err != nil || len(got) != 2 || got[1].Text != text {
			t.Fatalf("distinct alternative evidence lost: %q", text)
		}
	}
	if alternativeKey("*Name (Class ’25)*, director") != alternativeKey("Name (Class ’25), director") {
		t.Fatal("emphasis before punctuation was treated as new content")
	}
	if alternativeKey("Cost is 2*3") == alternativeKey("Cost is 23") || alternativeKey("booking_A_B") == alternativeKey("bookingAB") {
		t.Fatal("comparison erased arithmetic or identifiers")
	}
}
