package agent

import (
	"context"

	"github.com/sigtunnel/vm-control-plane/internal/driver/compute"
	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

// resyncLoop is the drift detector: it periodically observes every VM this
// daemon has realized, reports the evidence, and re-ensures through the
// executor when observation diverges from the applied intent. Drift repair is
// provisioning; there is one code path.
func (d *Daemon) resyncLoop(ctx context.Context) {
	interval := d.cfg.PollInterval * 4
	for {
		select {
		case <-ctx.Done():
			return
		case <-d.cfg.Clock.After(interval):
		}
		if d.halted.Load() {
			continue
		}
		d.mu.Lock()
		snapshot := make([]*vmcv1.Intent, 0, len(d.lastApplied))
		for _, in := range d.lastApplied {
			snapshot = append(snapshot, in)
		}
		d.mu.Unlock()

		for _, intent := range snapshot {
			d.resyncOne(ctx, intent)
		}
	}
}

// resyncOne observes one realized VM, reports the evidence, and re-dispatches
// it through the executor if it has drifted from its desired power state.
func (d *Daemon) resyncOne(ctx context.Context, intent *vmcv1.Intent) {
	state, err := d.cfg.Compute.Observe(ctx, intent.GetVmId(), intent.GetPlacementEpoch())
	if err != nil {
		return
	}
	sess := d.sessions[intent.GetNodeName()]
	if sess == nil {
		return
	}
	wantRunning := intent.GetSpec().GetPower() != vmcv1.PowerState_POWER_STATE_STOPPED
	drifted := (wantRunning && state != compute.StateRunning) ||
		(!wantRunning && state != compute.StateShutoff)
	detail := ""
	if drifted {
		detail = "drift-detected"
	}
	_, _ = d.client.Report(ctx, &vmcv1.ReportRequest{
		Session: sess, VmId: intent.GetVmId(), PlacementEpoch: intent.GetPlacementEpoch(),
		ReportSeq:       d.nextSeq(intent.GetVmId()),
		AppliedRevision: intent.GetDesiredRevision(),
		State:           observedStateProto(state),
		Detail:          detail,
	})
	if drifted {
		d.cfg.Log.Warn("drift detected — re-ensuring through the executor",
			"vm", intent.GetVmName(), "observed", string(state))
		d.dispatch(ctx, intent)
	}
}
