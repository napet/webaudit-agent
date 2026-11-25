package testsite

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestLocalOnlyRefusesNames(t *testing.T) {
	_, err := LocalOnly{}.DialContext(context.Background(), "tcp", "kis-ceg.example:443")
	var dnsErr *net.DNSError
	if !errors.As(err, &dnsErr) || !dnsErr.IsNotFound || dnsErr.Name != "kis-ceg.example" {
		t.Fatalf("err = %v, want a not-found DNS error", err)
	}
	if got, want := err.Error(), "dial tcp: lookup kis-ceg.example: no such host"; got != want {
		t.Errorf("err = %q, want %q (the text a real resolver gives)", got, want)
	}
}

func TestLocalOnlyTransportReachesLoopback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusTeapot) }))
	defer srv.Close()
	c := &http.Client{Transport: LocalOnlyTransport()}
	resp, err := c.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusTeapot {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if _, err := c.Get("http://kis-ceg.example/"); err == nil {
		t.Error("a host name must not be reachable")
	}
}

func TestQueryName(t *testing.T) {
	q := []byte{3, 'w', 'w', 'w', 7, 'e', 'x', 'a', 'm', 'p', 'l', 'e', 2, 'h', 'u', 0, 0, 1, 0, 1}
	if got := queryName(q); got != "www.example.hu" {
		t.Errorf("queryName = %q", got)
	}
	if got := queryName([]byte{9, 'x'}); got != "(unreadable name)" {
		t.Errorf("truncated name = %q", got)
	}
}
