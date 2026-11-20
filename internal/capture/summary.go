package capture

import (
	"fmt"
	"strings"

	"github.com/PuerkitoBio/goquery"

	"github.com/napet/webaudit-agent/internal/htmltext"
)

// SummaryLimit caps the DOM summary sent to the LLM, in bytes.
const SummaryLimit = 4000

// Summarize builds the compact text view of a page the LLM sees next to the
// screenshots: what the page says and how it is built, without raw markup.
func Summarize(html string, limit int) string {
	doc := htmltext.Parse(html)
	var b strings.Builder
	line := func(label, v string) {
		if v = htmltext.Squash(v); v != "" {
			fmt.Fprintf(&b, "%s: %s\n", label, v)
		}
	}
	line("Title", doc.Find("title").First().Text())
	line("Meta description", doc.Find(`meta[name="description"]`).AttrOr("content", ""))
	line("Language", doc.Find("html").AttrOr("lang", ""))
	line("Generator", doc.Find(`meta[name="generator"]`).AttrOr("content", ""))

	var heads []string
	doc.Find("h1, h2, h3").EachWithBreak(func(i int, s *goquery.Selection) bool {
		if t := htmltext.Squash(s.Text()); t != "" {
			heads = append(heads, goquery.NodeName(s)+" "+t)
		}
		return len(heads) < 15
	})
	if len(heads) > 0 {
		b.WriteString("Headings:\n  " + strings.Join(heads, "\n  ") + "\n")
	}

	nav := doc.Find("nav a, header a, #menu a, .menu a, nav [onclick], header [onclick]")
	if nav.Length() == 0 {
		all := doc.Find(LinkSelector)
		nav = all.Slice(0, min(all.Length(), 25))
	}
	var links []string
	seen := map[string]bool{}
	nav.Each(func(_ int, s *goquery.Selection) {
		if t := htmltext.Squash(s.Text()); t != "" && !seen[t] && len(links) < 25 {
			seen[t] = true
			links = append(links, t)
		}
	})
	line("Navigation", strings.Join(links, " | "))

	footer := doc.Find("footer").Text()
	if footer == "" {
		footer = doc.Find("#footer, .footer").Text()
	}
	line("Footer", htmltext.TruncateBytes(htmltext.Squash(footer), 400))

	var hints []string
	scriptHosts := map[string]bool{}
	doc.Find("script[src]").Each(func(_ int, s *goquery.Selection) {
		src := s.AttrOr("src", "")
		name := src[strings.LastIndex(src, "/")+1:]
		if q := strings.IndexAny(name, "?#"); q >= 0 {
			name = name[:q]
		}
		if name != "" && !scriptHosts[name] && len(scriptHosts) < 15 {
			scriptHosts[name] = true
			hints = append(hints, name)
		}
	})
	line("Scripts", strings.Join(hints, ", "))
	line("Layout tables", fmt.Sprint(doc.Find("table table, body > table, table[width]").Length()))
	line("Flash/obsolete embeds", fmt.Sprint(doc.Find(`object, embed, applet, marquee, blink, font`).Length()))

	return htmltext.TruncateBytes(b.String(), limit)
}
