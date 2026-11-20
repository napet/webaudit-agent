package psi

// The fixtures follow the documented PageSpeed Insights v5 response shape,
// trimmed to the fields the parser reads.

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func load(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("testdata/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseWithFieldData(t *testing.T) {
	ps, err := Parse(load(t, "with_field.json"))
	if err != nil {
		t.Fatal(err)
	}
	if ps.Scores["performance"] != 0.42 || ps.Scores["best-practices"] != 0.78 {
		t.Errorf("scores = %v", ps.Scores)
	}
	if m := ps.Field["LARGEST_CONTENTFUL_PAINT_MS"]; m.Percentile != 4100 || m.Category != "SLOW" {
		t.Errorf("field LCP = %+v", m)
	}
	if s := ps.Audits["font-size"]; s == nil || *s != 0 {
		t.Errorf("font-size audit = %v", s)
	}
	if _, ok := ps.Audits["tap-targets"]; ok {
		t.Error("absent audit should not appear")
	}
}

func TestParseWithoutFieldData(t *testing.T) {
	ps, err := Parse(load(t, "lab_only.json"))
	if err != nil {
		t.Fatal(err)
	}
	if ps.Field != nil {
		t.Errorf("field = %v, want absent", ps.Field)
	}
	if ps.Scores["performance"] != 0.91 {
		t.Errorf("scores = %v", ps.Scores)
	}
}

func TestParseRejectsGarbage(t *testing.T) {
	if _, err := Parse([]byte(`{"error":{}}`)); err == nil {
		t.Error("want error for response without lighthouseResult")
	}
}

func TestClientRequestAndErrors(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		if r.URL.Query().Get("key") == "bad" {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"API key not valid"}}`))
			return
		}
		_, _ = w.Write(load(t, "lab_only.json"))
	}))
	defer srv.Close()

	c := &Client{Key: "k", Endpoint: srv.URL}
	raw, err := c.Run(context.Background(), "https://kis-ceg.hu/")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(raw); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"strategy=mobile", "category=PERFORMANCE", "category=SEO", "url=https%3A%2F%2Fkis-ceg.hu%2F"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query %q missing %q", gotQuery, want)
		}
	}

	_, err = (&Client{Key: "bad", Endpoint: srv.URL}).Run(context.Background(), "https://x.hu/")
	if err == nil || !strings.Contains(err.Error(), "API key not valid") {
		t.Errorf("err = %v", err)
	}
}

func TestClientTimeoutHidesKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
	}))
	defer srv.Close()
	c := &Client{Key: "secret-key", Endpoint: srv.URL, HTTP: &http.Client{Timeout: 50 * time.Millisecond}}
	_, err := c.Run(context.Background(), "https://x.hu/")
	if err == nil || strings.Contains(err.Error(), "secret-key") {
		t.Errorf("err = %v", err)
	}
}
