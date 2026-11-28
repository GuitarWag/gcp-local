package gcplocaltest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/GuitarWag/gcp-local/tests/go/testutil"
)

// The pidfile path is $HOME/.gcp-local/gcp-local.pid, shared by every
// emulator on the machine. A concurrently exiting suite or another
// factory team can delete it first, so shutdown Remove() hits
// ErrNotExist and must tolerate it instead of failing.
func TestPidfileShutdownToleratesMissingFile(t *testing.T) {
	em := testutil.Start(t)

	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("user home dir: %v", err)
	}
	pidPath := filepath.Join(home, ".gcp-local", "gcp-local.pid")
	// ENOENT is fine; another suite may have removed the file first.
	_ = os.Remove(pidPath)

	em.Stop()
}
