// Package htmltext holds the small text and HTML helpers the capture, checks
// and agent packages share.
package htmltext

import (
	"strings"

	"github.com/PuerkitoBio/goquery"
)

// Squash collapses every run of whitespace to one space and trims the ends.
func Squash(s string) string { return strings.Join(strings.Fields(s), " ") }

// Shorten cuts s to at most n runes, marking a cut with "…". Use it for text
// shown to people or the model, where n counts characters.
func Shorten(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// TruncateBytes cuts s to at most n bytes on a rune boundary, marking a cut
// with "…". Use it where n is a size budget, as for the page summary.
func TruncateBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && n < len(s) && s[n]&0xC0 == 0x80 {
		n--
	}
	return s[:n] + "…"
}

// Parse parses an HTML page. It never returns nil: markup that cannot be read
// gives an empty document, so callers find nothing instead of failing.
func Parse(html string) *goquery.Document {
	d, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		d, _ = goquery.NewDocumentFromReader(strings.NewReader(""))
	}
	return d
}
