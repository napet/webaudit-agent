// Package testsite serves the fixture sites in testdata/sites for tests and
// records every request, so tests can assert that the tool never POSTed.
package testsite

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"sync"
)

// Dir returns the absolute path of testdata/sites.
func Dir() string {
	_, file, _, _ := goruntime.Caller(0)
	return filepath.Join(filepath.Dir(file), "..", "..", "testdata", "sites")
}

// Server is a running fixture site.
type Server struct {
	*httptest.Server
	mu       sync.Mutex
	requests []string // "METHOD /path"
}

// Requests returns every request received so far.
func (s *Server) Requests() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.requests...)
}

// Posts returns the non-GET/HEAD requests received so far.
func (s *Server) Posts() []string {
	var out []string
	for _, r := range s.Requests() {
		if !strings.HasPrefix(r, "GET ") && !strings.HasPrefix(r, "HEAD ") {
			out = append(out, r)
		}
	}
	return out
}

// Start serves testdata/sites/<name>. "/x" serves x.html when it exists, so
// fixtures can use extensionless links. "/_hang" never answers until the
// client gives up, so a page that embeds it never fires its load event.
// Unknown paths return 404.
func Start(name string) *Server {
	root := filepath.Join(Dir(), name)
	s := &Server{}
	s.Server = httptest.NewServer(s.handler(root))
	return s
}

// StartTLS is Start over HTTPS with the httptest self-signed certificate.
func StartTLS(name string) *Server {
	root := filepath.Join(Dir(), name)
	s := &Server{}
	s.Server = httptest.NewTLSServer(s.handler(root))
	return s
}

func (s *Server) handler(root string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		s.mu.Lock()
		s.requests = append(s.requests, r.Method+" "+r.URL.Path)
		s.mu.Unlock()
		if r.URL.Path == "/_hang" {
			<-r.Context().Done()
			return
		}
		p := filepath.Join(root, filepath.FromSlash(strings.TrimPrefix(r.URL.Path, "/")))
		if r.URL.Path == "/" {
			p = filepath.Join(root, "index.html")
		}
		if fi, err := os.Stat(p); err != nil || fi.IsDir() {
			if _, err := os.Stat(p + ".html"); err == nil {
				p += ".html"
			} else {
				http.NotFound(w, r)
				return
			}
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			http.Error(w, "fixture accepts GET and HEAD only", http.StatusMethodNotAllowed)
			return
		}
		http.ServeFile(w, r, p)
	})
}
