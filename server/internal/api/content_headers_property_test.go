package api

import (
	"mime"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/PeterGuy326/mem/server/internal/pathx"
)

// TestSanitizeFilenameIsNoWeakerThanUploadValidator pins the claim in
// sanitizeFilename's comment that the two layers share one character policy.
//
// The direction matters: pathx.ValidateName is the gate a normal upload passes,
// and sanitizeFilename is the last line for a name that reached a response
// without it (a row written outside the validated path, or a future caller). So
// sanitizeFilename must strip at least everything the validator rejects. It may
// strip more — the header value also forbids '"', ';' and '\'.
//
// Without this, tightening pathx alone would silently reopen the response path,
// which is exactly how the C1-control gap in review 5063230531 came to exist.
func TestSanitizeFilenameIsNoWeakerThanUploadValidator(t *testing.T) {
	// The basic multilingual plane covers every control, bidi and line-break
	// character in question, plus the rest of Unicode's assigned ranges.
	runes := make([]rune, 0, 0x11000)
	for r := rune(0); r <= 0xFFFF; r++ {
		if !utf8.ValidRune(r) || r == utf8.RuneError {
			continue
		}
		runes = append(runes, r)
	}
	for _, r := range []rune{'\U0001F3FB', '\U000E0001', '\U000F0000', '\U0010FFFF'} {
		runes = append(runes, r)
	}

	for _, r := range runes {
		name := "a" + string(r) + "b"
		if pathx.ValidateName(name) == nil {
			continue // the upload path accepts it; sanitize owes nothing here
		}
		if got := sanitizeFilename(name); got == name {
			t.Errorf("pathx.ValidateName(%q) rejects it but sanitizeFilename passes it "+
				"through unchanged as %q: the response path is weaker than the upload path",
				name, got)
		}
	}
}

// FuzzContentResponseHeaders asserts the property the whole helper exists to
// provide: whatever a client declared, neither response header can carry a line
// break or an empty disposition. Under `go test` this runs the seed corpus; use
// `go test -run '^$' -fuzz FuzzContentResponseHeaders` to search.
func FuzzContentResponseHeaders(f *testing.F) {
	seeds := []string{
		"text/html; charset=utf-8",
		"multipart/mixed; boundary=mem-boundary",
		"text/plain; format=flowed; charset=GBK",
		"application/example; note=\"safe value\"",
		"text/plain; charset=\"x\r\nY: 1\"",
		"image/png; note=\"x\r\nX-Injected: 1\"",
		"text/html\r\nX-Injected: 1",
		"font/sfnt",
		"text/javascript1.5",
		"",
		"html",
		";;;",
		strings.Repeat("a", 300) + "/b",
	}
	for _, s := range seeds {
		f.Add(s, "note.txt")
		f.Add(s, "报\ue202e告.exe.html")
	}

	f.Fuzz(func(t *testing.T, storedMIME, name string) {
		contentType, disposition := contentResponseHeaders(storedMIME, name)

		for label, value := range map[string]string{
			"Content-Type":        contentType,
			"Content-Disposition": disposition,
		} {
			if strings.ContainsAny(value, "\r\n") {
				t.Fatalf("%s = %q for declared %q name %q: carries a line break",
					label, value, storedMIME, name)
			}
		}
		if contentType == "" {
			t.Fatalf("Content-Type is empty for declared %q", storedMIME)
		}
		if _, _, err := mime.ParseMediaType(contentType); err != nil {
			t.Fatalf("served Content-Type %q for declared %q does not parse: %v",
				contentType, storedMIME, err)
		}
		if !strings.HasPrefix(disposition, "inline; ") && !strings.HasPrefix(disposition, "attachment; ") {
			t.Fatalf("Content-Disposition = %q for declared %q: no disposition token",
				disposition, storedMIME)
		}
		if !strings.Contains(disposition, `filename="`) {
			t.Fatalf("Content-Disposition = %q for declared %q: no quoted filename",
				disposition, storedMIME)
		}
	})
}
