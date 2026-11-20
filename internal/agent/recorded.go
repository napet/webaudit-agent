package agent

import (
	"context"
	"errors"

	"github.com/napet/webaudit-agent/internal/capture"
)

// ErrNotRecorded is returned by a recorded session for a page the snapshot
// does not hold. inspect_page refuses it without using page budget.
var ErrNotRecorded = errors.New("not available in this replay: only pages recorded in the snapshot can be opened")

// RecordedSession serves the pages an earlier agent run recorded, so an
// agent replay can run without a browser or any request to the site.
type RecordedSession struct {
	pages map[string]int
	all   []capture.Page
	shots []capture.Shot
}

// NewRecordedSession serves pages under both their requested and final URLs.
// shots holds the screenshots at the same index.
func NewRecordedSession(pages []capture.Page, shots []capture.Shot) *RecordedSession {
	s := &RecordedSession{pages: map[string]int{}, all: pages, shots: shots}
	for i, p := range pages {
		for _, u := range []string{p.RequestedURL, p.FinalURL} {
			if n := Normalize(u); n != "" {
				if _, ok := s.pages[n]; !ok {
					s.pages[n] = i
				}
			}
		}
	}
	return s
}

// Render serves a recorded page and its screenshots, or ErrNotRecorded for a
// URL the original run did not visit.
func (s *RecordedSession) Render(_ context.Context, u string) (capture.Page, capture.Shot, error) {
	i, ok := s.pages[Normalize(u)]
	if !ok {
		return capture.Page{}, capture.Shot{}, ErrNotRecorded
	}
	var shot capture.Shot
	if i < len(s.shots) {
		shot = s.shots[i]
	}
	return s.all[i], shot, nil
}

// Close does nothing: a recorded session holds no browser.
func (s *RecordedSession) Close() {}

var _ capture.Session = (*RecordedSession)(nil)
