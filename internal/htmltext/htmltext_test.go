package htmltext

import "testing"

func TestSquash(t *testing.T) {
	if got := Squash("  a \n\t b  c "); got != "a b c" {
		t.Errorf("Squash = %q", got)
	}
}

func TestShorten(t *testing.T) {
	if got := Shorten("árvíztűrő", 3); got != "árv…" {
		t.Errorf("Shorten = %q", got)
	}
	if got := Shorten("abc", 3); got != "abc" {
		t.Errorf("Shorten kept = %q", got)
	}
}

func TestTruncateBytes(t *testing.T) {
	// "á" is two bytes: a cut inside it moves back to the rune boundary.
	if got := TruncateBytes("aáb", 2); got != "a…" {
		t.Errorf("TruncateBytes = %q", got)
	}
	if got := TruncateBytes("abc", 5); got != "abc" {
		t.Errorf("TruncateBytes kept = %q", got)
	}
}

func TestParse(t *testing.T) {
	if got := Parse("<title> Hi </title>").Find("title").Text(); got != " Hi " {
		t.Errorf("Parse title = %q", got)
	}
	if Parse("") == nil {
		t.Error("Parse returned nil")
	}
}
