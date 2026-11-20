package assess

import (
	"context"
	"testing"

	"github.com/napet/webaudit-agent/internal/capture"
	"github.com/napet/webaudit-agent/internal/testsite"
)

// liveScreenshots captures the outdated fixture site with Chrome, so the live
// test sends the model real screenshots.
func liveScreenshots(t *testing.T) ([]byte, []byte) {
	t.Helper()
	site := testsite.Start("old2012")
	defer site.Close()
	res, err := (&capture.Chrome{}).Capture(context.Background(), site.URL)
	if err != nil {
		t.Skipf("Chrome unavailable: %v", err)
	}
	return res.DesktopPNG, res.MobilePNG
}
