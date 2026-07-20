//go:build e2e

package e2e

import (
	"testing"
	"time"
)

// Polls cond until it reports done, the timeout elapses, or cond
// returns an error. Real AWS side effects (event source mapping activation,
// message delivery, notification config propagation) are eventually
// consistent, so assertions on them poll with explicit deadlines instead of
// checking once.
func waitFor(
	t *testing.T,
	timeout time.Duration,
	interval time.Duration,
	description string,
	cond func() (bool, error),
) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		done, err := cond()
		if err != nil {
			t.Fatalf("waiting for %s: %v", description, err)
		}
		if done {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", timeout, description)
		}
		time.Sleep(interval)
	}
}
