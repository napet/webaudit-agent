package psi

import (
	"os"
	"testing"

	"github.com/napet/webaudit-agent/internal/testsite"
)

// The tests never use the network; see testsite.RunOffline.
func TestMain(m *testing.M) { os.Exit(testsite.RunOffline(m)) }
