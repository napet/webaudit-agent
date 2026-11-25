package testsite

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// ErrOffline is what every DNS lookup returns under RunOffline.
var ErrOffline = errors.New("network disabled in tests")

// RunOffline runs a package's tests with DNS disabled and fails them if any
// code tried to look up a host name, listing the names. Fixture servers on
// 127.0.0.1 still work, since an IP address needs no lookup. Use it from
// TestMain:
//
//	func TestMain(m *testing.M) { os.Exit(testsite.RunOffline(m)) }
func RunOffline(m *testing.M) int {
	var mu sync.Mutex
	names := map[string]bool{}
	net.DefaultResolver = &net.Resolver{
		PreferGo: true,
		Dial: func(context.Context, string, string) (net.Conn, error) {
			return &dnsConn{seen: func(name string) {
				mu.Lock()
				names[name] = true
				mu.Unlock()
			}}, nil
		},
	}
	code := m.Run()
	mu.Lock()
	defer mu.Unlock()
	if len(names) > 0 {
		list := make([]string, 0, len(names))
		for n := range names {
			list = append(list, n)
		}
		sort.Strings(list)
		fmt.Fprintf(os.Stderr, "FAIL: tests looked up %s; tests must not use the network (dial through testsite.LocalOnly)\n", strings.Join(list, ", "))
		if code == 0 {
			code = 1
		}
	}
	return code
}

// LocalOnly dials loopback addresses as usual and answers every host name
// with the "no such host" error a resolver gives for a domain that doesn't
// exist. Tests that use it behave the same on any machine and never leave
// it. It fits checks.ProbeOptions.Dialer.
type LocalOnly struct{}

// DialContext dials an IP address or localhost; any other name fails as
// not found.
func (LocalOnly) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if net.ParseIP(host) != nil || host == "localhost" {
		var d net.Dialer
		return d.DialContext(ctx, network, addr)
	}
	return nil, &net.OpError{Op: "dial", Net: network, Err: &net.DNSError{Err: "no such host", Name: host, IsNotFound: true}}
}

// LocalOnlyTransport is an HTTP transport that dials through LocalOnly and
// ignores proxy settings.
func LocalOnlyTransport() *http.Transport {
	t := http.DefaultTransport.(*http.Transport).Clone()
	t.Proxy = nil
	t.DialContext = LocalOnly{}.DialContext
	return t
}

type stringAddr string

func (a stringAddr) Network() string { return "tcp" }
func (a stringAddr) String() string  { return string(a) }

// dnsConn reads the queried name from the first DNS message written to it
// (stream framing: a 2-byte length, then the message) and then fails.
type dnsConn struct{ seen func(string) }

func (c *dnsConn) Write(b []byte) (int, error) {
	if len(b) > 2+12 {
		c.seen(queryName(b[2+12:]))
	}
	return len(b), nil
}

func (c *dnsConn) Read([]byte) (int, error)         { return 0, ErrOffline }
func (c *dnsConn) Close() error                     { return nil }
func (c *dnsConn) LocalAddr() net.Addr              { return stringAddr("offline") }
func (c *dnsConn) RemoteAddr() net.Addr             { return stringAddr("offline") }
func (c *dnsConn) SetDeadline(time.Time) error      { return nil }
func (c *dnsConn) SetReadDeadline(time.Time) error  { return nil }
func (c *dnsConn) SetWriteDeadline(time.Time) error { return nil }

// queryName decodes the length-prefixed labels of a DNS question.
func queryName(b []byte) string {
	var labels []string
	for len(b) > 0 && b[0] != 0 && int(b[0]) < len(b) {
		n := int(b[0])
		labels = append(labels, string(b[1:1+n]))
		b = b[1+n:]
	}
	if len(labels) == 0 {
		return "(unreadable name)"
	}
	return strings.Join(labels, ".")
}
