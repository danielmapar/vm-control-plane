package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// NodeLossOutcome is what the sweep did to a VM whose node's lease expired.
type NodeLossOutcome string

const (
	// OutcomeRescheduled: the placement was never granted, so it was provably
	// unexposed and was unassigned back to Pending for rescheduling.
	OutcomeRescheduled NodeLossOutcome = "rescheduled"
	// OutcomeUnknown: the placement was granted, so the VM is parked in UNKNOWN
	// and never rescheduled automatically.
	OutcomeUnknown NodeLossOutcome = "unknown"
)

// NodeLossAction records what the node-loss sweep did to one VM.
type NodeLossAction struct {
	VMID    uuid.UUID
	VMName  string
	Node    string
	Epoch   int64
	Outcome NodeLossOutcome
}

// ExpireNodeVMs applies the node-loss policy: for every VM whose current
// placement sits on a node with an expired lease —
//
//   - placement never granted -> provably unexposed -> unassign (capacity
//     released) and requeue for rescheduling under a new epoch;
//   - placement granted -> exposure recorded -> park in UNKNOWN; never
//     rescheduled automatically. Two VMs is worse than one late VM.
//
// A VM already UNKNOWN whose node's lease is live again is woken; evidence
// will restore its phase through the normal convergence path.
func (s *Store) ExpireNodeVMs(ctx context.Context) ([]NodeLossAction, error) {
	cands, err := s.expiredNodeCandidates(ctx)
	if err != nil {
		return nil, err
	}
	var actions []NodeLossAction
	for _, c := range cands {
		action, err := s.expireNodeVM(ctx, c)
		if err != nil {
			return actions, err // partial progress is fine; the next sweep retries
		}
		if action != nil {
			actions = append(actions, *action)
		}
	}
	if err := s.wakeRecoveredNodeVMs(ctx); err != nil {
		return actions, err
	}
	return actions, nil
}

// nodeLossCandidate is a VM whose current placement sits on a lease-expired node.
type nodeLossCandidate struct {
	id    uuid.UUID
	name  string
	node  string
	epoch int64
	state PlacementState
}

// expiredNodeCandidates loads the VMs whose current placement sits on a node
// with an expired lease.
func (s *Store) expiredNodeCandidates(ctx context.Context) ([]nodeLossCandidate, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT v.id, v.name, v.node_name, v.placement_epoch, p.state
		FROM vms v
		JOIN placements p ON p.vm_id = v.id AND p.epoch = v.placement_epoch
		JOIN nodes n ON n.name = v.node_name
		WHERE n.lease_expires_at < clock_timestamp()
		  AND v.deleted_at IS NULL
		  AND v.phase IN ('PROVISIONING','RUNNING','STOPPED')`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var cands []nodeLossCandidate
	for rows.Next() {
		var c nodeLossCandidate
		if err := rows.Scan(&c.id, &c.name, &c.node, &c.epoch, &c.state); err != nil {
			return nil, err
		}
		cands = append(cands, c)
	}
	return cands, rows.Err()
}

// expireNodeVM applies the node-loss policy to one candidate, returning the
// audit action it produced (or nil when nothing changed):
//
//   - a never-granted placement is provably unexposed, so it is unassigned
//     (capacity released) and requeued for rescheduling under a new epoch;
//   - a granted placement is parked in UNKNOWN and never rescheduled
//     automatically. Two VMs is worse than one late VM.
func (s *Store) expireNodeVM(ctx context.Context, c nodeLossCandidate) (*NodeLossAction, error) {
	if c.state == PlacementAssigned {
		// UnassignIfUngranted re-verifies under the lock order — a racing grant
		// makes it a no-op and the VM parks UNKNOWN on the next sweep instead.
		unassigned, err := s.UnassignIfUngranted(ctx, c.id, c.epoch)
		if err != nil && !errors.Is(err, ErrNotFound) {
			return nil, err
		}
		if !unassigned {
			return nil, nil
		}
		return &NodeLossAction{VMID: c.id, VMName: c.name, Node: c.node, Epoch: c.epoch, Outcome: OutcomeRescheduled}, nil
	}
	// Entering UNKNOWN clears the applied-revision watermark so stale pre-loss
	// evidence cannot immediately re-converge when the node returns: a fresh
	// report from a live session is required.
	tag, err := s.pool.Exec(ctx, `
		UPDATE vms SET phase = 'UNKNOWN', applied_revision = 0, observed_state = '',
			resource_version = resource_version + 1, updated_at = now()
		WHERE id = $1 AND phase IN ('PROVISIONING','RUNNING','STOPPED')`, c.id)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() != 1 {
		return nil, nil
	}
	return &NodeLossAction{VMID: c.id, VMName: c.name, Node: c.node, Epoch: c.epoch, Outcome: OutcomeUnknown}, nil
}

// wakeRecoveredNodeVMs wakes UNKNOWN VMs whose node's lease is live again;
// fresh evidence will restore their phase through convergence.
func (s *Store) wakeRecoveredNodeVMs(ctx context.Context) error {
	_, err := s.pool.Exec(ctx, `
		UPDATE vms v SET next_attempt_at = clock_timestamp()
		FROM nodes n
		WHERE v.node_name = n.name AND v.phase = 'UNKNOWN'
		  AND n.lease_expires_at >= clock_timestamp()`)
	return err
}
