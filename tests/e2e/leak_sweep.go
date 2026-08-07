//go:build e2e

package e2e

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"
)

// Destroys complete before test cleanup returns, but a target's list APIs can
// keep returning deleted resources briefly; the sweep re-polls until the target
// looks clean or this deadline passes.
//
// Variables rather than constants so the orchestration's own tests can run
// without waiting out intervals sized for a cloud.
var (
	leakSweepTimeout  = 3 * time.Minute
	leakSweepInterval = 10 * time.Second
)

// A deploy target the sweep knows how to inspect and tidy up after.
//
// Celerity applications deploy to more than one target, and a test suite that
// exercised two of them would otherwise need two sweeps with nothing in common.
// Everything generic lives here: which names belong to this run, how long to wait
// for a target to settle, what may be deleted, and how it is reported. A target
// supplies only the parts that cannot be shared, which is how it lists resources
// and how it removes them.
type targetSweeper interface {
	// Name identifies the target in the sweep's output.
	Name() string

	// Available reports whether this target is configured for the current run.
	//
	// A suite that only exercised one target must not fail because another
	// target's credentials are absent, so an unavailable target is skipped
	// rather than treated as an error.
	Available() bool

	// Find lists resources belonging to any of the given scopes that survived
	// teardown, along with any problems encountered while listing.
	//
	// Listing problems do not stop the sweep: a partial answer still removes what
	// it can, and the problems are reported alongside whatever is left.
	//
	// Order does not matter. Dependencies between resources are handled by the
	// sweep re-running until the target settles.
	Find(ctx context.Context, scopes []string) (found []leakedResource, problems []string)

	// Delete removes one resource. A resource that has already gone is not an
	// error, since a list is a snapshot and teardown may still be completing.
	Delete(ctx context.Context, resource leakedResource) error
}

// Implemented by a target that creates resources as a side effect of running
// rather than because a blueprint declared them.
//
// AWS Lambda's log groups are the case: nothing in teardown owns them, so their
// survival is not a teardown bug, but they accumulate indefinitely. They are
// removed and never counted as leaks.
type targetTidier interface {
	Tidy(ctx context.Context, scopes []string) int
}

var sweeperRegistry = struct {
	mu       sync.Mutex
	sweepers []targetSweeper
}{}

// Called from a target's own build-tagged file, so adding a target is a new file
// rather than an edit to shared code.
func registerTargetSweeper(sweeper targetSweeper) {
	sweeperRegistry.mu.Lock()
	defer sweeperRegistry.mu.Unlock()

	sweeperRegistry.sweepers = append(sweeperRegistry.sweepers, sweeper)
}

func registeredTargetSweepers() []targetSweeper {
	sweeperRegistry.mu.Lock()
	defer sweeperRegistry.mu.Unlock()

	return append([]targetSweeper{}, sweeperRegistry.sweepers...)
}

// Each Setup registers its run-unique name scope (NamePrefix == AppName) here so
// the post-run sweep in TestMain knows which deployed names belong to this
// process.
var sweepRegistry = struct {
	mu     sync.Mutex
	scopes []string
}{}

func registerSweepScope(scope string) {
	sweepRegistry.mu.Lock()
	defer sweepRegistry.mu.Unlock()

	sweepRegistry.scopes = append(sweepRegistry.scopes, scope)
}

func registeredSweepScopes() []string {
	sweepRegistry.mu.Lock()
	defer sweepRegistry.mu.Unlock()

	return append([]string{}, sweepRegistry.scopes...)
}

// A resource the sweep found after teardown should have removed it.
type leakedResource struct {
	kind string
	// What to show a human.
	id string
	// What the delete call needs, where that differs from the display id (an API
	// is deleted by id and listed by name).
	handle string
	// Whether the resource matched a scope this process registered.
	//
	// Only a scoped match is deleted. A legacy-shape match carries no run scope, so
	// it cannot be attributed to this process: in a shared account it may belong to
	// another run or another developer, and deleting it would turn a reporting
	// mechanism into a hazard. Those are reported and left alone.
	scoped bool
}

func (l leakedResource) String() string {
	return l.kind + ": " + l.id
}

func (l leakedResource) deleteHandle() string {
	if l.handle != "" {
		return l.handle
	}

	return l.id
}

// Finds and removes resources whose names belong to any scope registered during
// the run, across every configured target, after the suite has finished and
// destroys have run.
//
// Deleting is a tidy-up, not an excuse. A resource surviving teardown is a bug in
// teardown, so the run still fails once anything is found; the sweep only stops
// that bug from also costing money and colliding with the next run.
//
// A no-op when no harness ever registered a scope (the suite skipped) or no
// target is configured.
func runLeakSweep() bool {
	scopes := registeredSweepScopes()
	if len(scopes) == 0 {
		return false
	}

	ctx := context.Background()
	failed := false
	ran := 0

	for _, sweeper := range registeredTargetSweepers() {
		if !sweeper.Available() {
			continue
		}
		ran++
		if sweepOneTarget(ctx, sweeper, scopes) {
			failed = true
		}
	}

	if ran == 0 {
		return false
	}

	return failed
}

// Sweeps a single target, reporting what it found and what is still outstanding.
// Returns whether anything should fail the run.
func sweepOneTarget(ctx context.Context, sweeper targetSweeper, scopes []string) bool {
	if tidier, ok := sweeper.(targetTidier); ok {
		if removed := tidier.Tidy(ctx, scopes); removed > 0 {
			fmt.Fprintf(
				os.Stderr,
				"leak sweep [%s]: removed %d resource(s) no teardown owns\n",
				sweeper.Name(), removed,
			)
		}
	}

	found, remaining := sweepUntilSettled(ctx, sweeper, scopes)
	if len(found) == 0 && len(remaining) == 0 {
		fmt.Fprintf(os.Stderr, "leak sweep [%s]: no leaked resources found\n", sweeper.Name())
		return false
	}

	if len(found) > 0 {
		fmt.Fprintf(
			os.Stderr,
			"leak sweep [%s]: %d leaked resource(s) found:\n",
			sweeper.Name(), len(found),
		)
		for _, leak := range found {
			disposition := "deleted"
			if !leak.scoped {
				disposition = "left alone (not attributable to this run)"
			}
			fmt.Fprintf(os.Stderr, "  - %s [%s]\n", leak, disposition)
		}
	}

	if len(remaining) > 0 {
		fmt.Fprintf(
			os.Stderr,
			"leak sweep [%s]: %d problem(s) still present after %s:\n",
			sweeper.Name(), len(remaining), leakSweepTimeout,
		)
		for _, problem := range remaining {
			fmt.Fprintf(os.Stderr, "  - %s\n", problem)
		}
	}

	return true
}

// Lists what leaked, deletes what it is entitled to delete, and re-lists until
// the target settles or the deadline passes.
//
// Returns everything that was ever found, and the problems still outstanding at
// the end: resources that would not delete, resources left alone deliberately,
// and listing problems.
func sweepUntilSettled(
	ctx context.Context,
	sweeper targetSweeper,
	scopes []string,
) (found []leakedResource, remaining []string) {
	seen := map[string]bool{}
	deadline := time.Now().Add(leakSweepTimeout)

	for {
		leaks, problems := sweeper.Find(ctx, scopes)
		for _, leak := range leaks {
			if !seen[leak.String()] {
				seen[leak.String()] = true
				found = append(found, leak)
			}
		}

		if len(leaks) == 0 && len(problems) == 0 {
			return found, nil
		}

		blocked := append(
			deleteScoped(ctx, sweeper, leaks),
			unattributable(leaks)...,
		)

		// Every remaining item either cannot be deleted or must not be, so another
		// pass would list exactly the same things. Stop rather than spin to the
		// deadline.
		if len(blocked) == len(leaks) && len(problems) == 0 {
			return found, blocked
		}

		if time.Now().After(deadline) {
			return found, append(blocked, problems...)
		}

		time.Sleep(leakSweepInterval)
	}
}

func deleteScoped(
	ctx context.Context,
	sweeper targetSweeper,
	leaks []leakedResource,
) []string {
	problems := []string{}
	for _, leak := range leaks {
		if !leak.scoped {
			continue
		}
		if err := sweeper.Delete(ctx, leak); err != nil {
			problems = append(problems, fmt.Sprintf("could not delete %s: %v", leak, err))
		}
	}

	return problems
}

func unattributable(leaks []leakedResource) []string {
	problems := []string{}
	for _, leak := range leaks {
		if !leak.scoped {
			problems = append(
				problems,
				leak.String()+" (not attributable to this run, left alone)",
			)
		}
	}

	return problems
}
