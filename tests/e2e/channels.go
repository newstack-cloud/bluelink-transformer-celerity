//go:build e2e

package e2e

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/newstack-cloud/bluelink/libs/blueprint/changes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/container"
	"github.com/newstack-cloud/bluelink/libs/blueprint/core"
	"github.com/newstack-cloud/bluelink/libs/blueprint/includes"
	"github.com/newstack-cloud/bluelink/libs/blueprint/subengine"
	"github.com/stretchr/testify/require"
)

// Determines how long we wait for any single message from the engine before
// giving up; generous because real AWS create/destroy/stabilise can be slow.
const channelTimeout = 30 * time.Minute

func newChangeStagingChannels() *container.ChangeStagingChannels {
	return &container.ChangeStagingChannels{
		ResourceChangesChan: make(chan container.ResourceChangesMessage),
		ChildChangesChan:    make(chan container.ChildChangesMessage),
		LinkChangesChan:     make(chan container.LinkChangesMessage),
		CompleteChan:        make(chan changes.BlueprintChanges),
		ErrChan:             make(chan error),
	}
}

func consumeStage(t *testing.T, channels *container.ChangeStagingChannels) *changes.BlueprintChanges {
	t.Helper()
	changeSet, err := consumeStageErr(channels)
	require.NoError(t, err, "stage changes")
	return changeSet
}

func consumeStageErr(channels *container.ChangeStagingChannels) (*changes.BlueprintChanges, error) {
	for {
		select {
		case <-channels.ResourceChangesChan:
		case <-channels.ChildChangesChan:
		case <-channels.LinkChangesChan:
		case changeSet := <-channels.CompleteChan:
			return &changeSet, nil
		case err := <-channels.ErrChan:
			return nil, err
		case <-time.After(channelTimeout):
			return nil, errors.New("timed out waiting for change staging to complete")
		}
	}
}

func consumeDeploy(t *testing.T, channels *container.DeployChannels) (container.DeploymentFinishedMessage, []string) {
	t.Helper()
	finished, elementFailures, err := consumeDeployErr(channels)
	require.NoError(t, err, "deploy")
	return finished, elementFailures
}

// Drains the deploy event stream until the finish message,
// collecting per-element failure reasons along the way. The finish message
// only names failed elements ("failed to deploy link(a::b)"), so the
// element-level events are the only place the underlying error appears.
func consumeDeployErr(
	channels *container.DeployChannels,
) (container.DeploymentFinishedMessage, []string, error) {
	var elementFailures []string
	collect := func(kind, name string, reasons []string) {
		for _, reason := range reasons {
			elementFailures = append(elementFailures,
				fmt.Sprintf("%s %s: %s", kind, name, reason))
		}
	}
	for {
		select {
		case msg := <-channels.ResourceUpdateChan:
			collect("resource", msg.ResourceName, msg.FailureReasons)
		case <-channels.ChildUpdateChan:
		case msg := <-channels.LinkUpdateChan:
			collect("link", msg.LinkName, msg.FailureReasons)
		case <-channels.DeploymentUpdateChan:
		case finished := <-channels.FinishChan:
			// Element update messages race with the finish message across
			// separate channels (select picks among ready channels at
			// random), so failure-carrying updates can still be in flight.
			// Grace-drain briefly so their reasons are not lost.
			deadline := time.After(2 * time.Second)
			for {
				select {
				case msg := <-channels.ResourceUpdateChan:
					collect("resource", msg.ResourceName, msg.FailureReasons)
				case msg := <-channels.LinkUpdateChan:
					collect("link", msg.LinkName, msg.FailureReasons)
				case <-channels.ChildUpdateChan:
				case <-channels.DeploymentUpdateChan:
				case <-deadline:
					return finished, elementFailures, nil
				}
			}
		case err := <-channels.ErrChan:
			return container.DeploymentFinishedMessage{}, elementFailures, err
		case <-time.After(channelTimeout):
			return container.DeploymentFinishedMessage{}, elementFailures,
				errors.New("timed out waiting for deployment to finish")
		}
	}
}

// Rejects child includes; e2e fixtures never use them.
type noChildResolver struct{}

func (r *noChildResolver) Resolve(
	_ context.Context,
	includeName string,
	_ *subengine.ResolvedInclude,
	_ core.BlueprintParams,
) (*includes.ChildBlueprintInfo, error) {
	return nil, fmt.Errorf("child includes are not supported in e2e tests: %s", includeName)
}
