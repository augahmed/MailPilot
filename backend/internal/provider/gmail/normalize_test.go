package gmail

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"

	"mailpilot.local/core/internal/provider"
)

func fixture(t *testing.T, name string) message {
	t.Helper()
	data, err := os.ReadFile("testdata/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	var m message
	if err = json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	return m
}
func TestNormalizeFixtures(t *testing.T) {
	e, err := normalize("account", fixture(t, "multipart"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if e.BodyText != "Plain body\nSecond line" || e.Subject != "Hello" || e.Sender.Address != "sender@example.test" || len(e.Recipients) != 2 || len(e.CCRecipients) != 1 || len(e.Headers) != 5 || e.ReceivedAt.UnixMilli() != 1700000000123 {
		t.Fatal("normalization lost message fields")
	}
	if e.BodyHTML == nil || len(e.Attachments) != 1 || e.Attachments[0].ProviderID != "att1" || *e.Attachments[0].SizeBytes != 123 || len(e.Labels) != 2 {
		t.Fatal("missing HTML, labels or attachment metadata")
	}
	e, err = normalize("account", fixture(t, "html"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if e.BodyText != "Hello & welcome\nSecond line" || strings.Contains(e.BodyText, "secret") || strings.Contains(e.BodyText, "tracker") {
		t.Fatalf("unsafe or unreadable HTML text: %q", e.BodyText)
	}
}
func TestNormalizeUnavailableAndMalformed(t *testing.T) {
	m := fixture(t, "html")
	m.Payload.Body.Data = ""
	m.Payload.Body.AttachmentID = "external"
	e, err := normalize("account", m, nil)
	if err != nil || e.BodyState != "unavailable" {
		t.Fatal("missing body reported complete", err)
	}
	for _, change := range []func(*message){
		func(m *message) { m.InternalDate = "bad" },
		func(m *message) { m.Payload.Body.Data = "%%%" },
		func(m *message) { m.Payload.Body.Data = strings.Repeat("a", 3*1024*1024) },
		func(m *message) {
			for i := 0; i < 22; i++ {
				m.Payload = part{MIMEType: "multipart/mixed", Parts: []part{m.Payload}}
			}
		},
	} {
		m = fixture(t, "html")
		change(&m)
		if _, err = normalize("account", m, nil); !errors.Is(err, provider.ErrMalformed) {
			t.Fatal("malformed input accepted", err)
		}
	}
}
func TestAttachedMessagesAreNotBody(t *testing.T) {
	m := fixture(t, "multipart")
	m.Payload.Parts[1].MIMEType = "message/rfc822"
	m.Payload.Parts[1].Parts = []part{fixture(t, "html").Payload}
	e, err := normalize("account", m, nil)
	if err != nil || e.BodyText != "Plain body\nSecond line" || len(e.Attachments) != 1 {
		t.Fatal("attached mail escaped into body", err)
	}
}

func TestMultipleMIMEAlternativesAreNotConcatenated(t *testing.T) {
	m := fixture(t, "html")
	html := m.Payload
	m.Payload = part{MIMEType: "multipart/alternative", Parts: []part{html, html}}
	e, err := normalize("account", m, nil)
	if err != nil || strings.Count(e.BodyText, "Hello & welcome") != 1 {
		t.Fatal("duplicated alternative body", err)
	}
	m = fixture(t, "html")
	m.Payload.Body.Data = ""
	size := int64(42)
	m.Payload.Body.Size = &size
	e, err = normalize("account", m, nil)
	if err != nil || e.BodyState != "unavailable" {
		t.Fatal("missing positive-sized body claimed complete", err)
	}
	m = fixture(t, "html")
	size = 10000
	m.Payload.Body.Size = &size
	e, err = normalize("account", m, nil)
	if err != nil || e.BodyState != "unavailable" || !strings.Contains(e.BodyText, "Hello & welcome") {
		t.Fatal("available fragment lost or claimed complete", err)
	}
}
