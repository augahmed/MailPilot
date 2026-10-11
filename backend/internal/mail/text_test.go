package mail

import "testing"

func TestCharsetAndHTMLAreDecodedLocally(t *testing.T) {
	got, err := DecodeText([]byte{'c', 'a', 'f', 0xe9}, "text/plain; charset=iso-8859-1")
	if err != nil || got != "café" {
		t.Fatal("charset conversion", got, err)
	}
	if _, err = DecodeText([]byte{0xff}, "text/plain; charset=utf-8"); err == nil {
		t.Fatal("invalid UTF-8 accepted")
	}
	got, err = HTMLText(`<div>One<br>Two &lt;three&gt;</div><style>hidden</style><iframe src="https://example.test">hidden</iframe>`)
	if err != nil || got != "One\nTwo <three>" {
		t.Fatal("HTML fallback", got, err)
	}
}
