//go:build e2e

package e2e

import (
	"os"
	"testing"
)

// TestMain runs the suite and then, when the suite actually deployed against
// AWS, sweeps the account for resources leaked by this run (see leak_sweep.go).
// Leaks fail the run even when every test passed.
func TestMain(m *testing.M) {
	code := m.Run()
	if runLeakSweep() {
		os.Exit(1)
	}
	os.Exit(code)
}
