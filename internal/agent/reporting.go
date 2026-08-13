package agent

import (
	"context"
	"sync/atomic"

	"github.com/sigtunnel/vm-control-plane/internal/driver/compute"
	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

// observedStateProto maps a driver-observed State to its wire enum.
func observedStateProto(s compute.State) vmcv1.ObservedState {
	switch s {
	case compute.StateRunning:
		return vmcv1.ObservedState_OBSERVED_STATE_RUNNING
	case compute.StateShutoff:
		return vmcv1.ObservedState_OBSERVED_STATE_SHUTOFF
	default:
		return vmcv1.ObservedState_OBSERVED_STATE_ABSENT
	}
}

func (d *Daemon) actionFailed(ctx context.Context, sess *vmcv1.NodeSession, intent *vmcv1.Intent, cause error) {
	_, _ = d.client.ActionFailed(ctx, &vmcv1.ActionFailedRequest{
		Session: sess, VmId: intent.GetVmId(),
		PlacementEpoch: intent.GetPlacementEpoch(), DesiredRevision: intent.GetDesiredRevision(),
		AttemptToken: intent.GetAttemptToken(), Error: cause.Error(), ErrorClass: "transient",
	})
}

func (d *Daemon) nextSeq(vmID string) int64 {
	d.mu.Lock()
	counter, ok := d.seqs[vmID]
	if !ok {
		var v int64
		counter = &v
		d.seqs[vmID] = counter
	}
	d.mu.Unlock()
	return atomic.AddInt64(counter, 1)
}
