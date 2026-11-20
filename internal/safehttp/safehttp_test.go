package safehttp

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRefusesNonGetHead(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { hits++ }))
	defer srv.Close()
	c := New(Options{})

	for _, m := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions} {
		req, _ := http.NewRequest(m, srv.URL, strings.NewReader("x=1"))
		_, err := c.Do(req)
		var e ErrMethodNotAllowed
		if !errors.As(err, &e) {
			t.Errorf("%s: want ErrMethodNotAllowed, got %v", m, err)
		}
	}
	if _, err := c.PostForm(srv.URL, nil); err == nil {
		t.Error("PostForm: want error")
	}
	if hits != 0 {
		t.Fatalf("server received %d requests, want 0", hits)
	}
}

func TestAllowsGetHeadAndSetsUA(t *testing.T) {
	var ua string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { ua = r.UserAgent() }))
	defer srv.Close()
	c := New(Options{})
	if _, err := c.Get(srv.URL); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Head(srv.URL); err != nil {
		t.Fatal(err)
	}
	if ua != UserAgent {
		t.Errorf("user agent = %q", ua)
	}
}

func TestRequestCap(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	defer srv.Close()
	c := New(Options{MaxRequest: 2})
	for i := 0; i < 2; i++ {
		resp, err := c.Get(srv.URL)
		if err != nil {
			t.Fatalf("request %d: %v", i+1, err)
		}
		resp.Body.Close()
	}
	if _, err := c.Get(srv.URL); !errors.Is(err, ErrRequestCap) {
		t.Fatalf("third request: want ErrRequestCap, got %v", err)
	}
}
