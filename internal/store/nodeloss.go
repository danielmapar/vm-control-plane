package store

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// NodeLossAction records what the node-loss sweep did to one VM.
type NodeLossAction struct {
	VMID   uuid.UUID
	VMName string
	Node   string
	Epoch  int64
	// "rescheduled" (never granted — unassigned back to Pending) or
	// "unknown" (granted — parked, never rescheduled; matrix rows 8–9).
	Outcome string
}

// ExpireNodeVMs applies the node-loss policy (plan §6.2, N2): for every VM
// whose current placement sits on a node with an EXPIRED lease —
//
//   - placement never granted → provably unexposed → unassign (capacity
//     released) and requeue for rescheduling under a new epoch;
//   - placement granted → exposure recorded → park in UNKNOWN; never
//     rescheduled automatically. Two VMs is worse than one late VM.
//
// A VM already UNKNOWN whose node's lease is live again is woken (evidence
// will restore its phase through the normal convergence path).
func (s *Store) ExpireNodeVMs(ctx context.Context) ([]NodeLossAction, error) {
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
	type candidate struct {
		id    uuid.UUID
		name  string
		node  string
		epoch int64
		state string
	}
	var cands []candidate
	for rows.Next() {
		var c candidate
		if err := rows.Scan(&c.id, &c.name, &c.node, &c.epoch, &c.state); err != nil {
			rows.Close()
			return nil, err
		}
		cands = append(cands, c)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var actions []NodeLossAction
	for _, c := range cands {
		if c.state == "assigned" {
			// Provably unexposed: UnassignIfUngranted re-verifies under the
			// lock order — a racing grant makes it a no-op and the VM parks
			// UNKNOWN on the next sweep instead.
			unassigned, err := s.UnassignIfUngranted(ctx, c.id, c.epoch)
			if err != nil && !errors.Is(err, ErrNotFound) {
				return actions, err
			}
			if unassigned {
				actions = append(actions, NodeLossAction{
					VMID: c.id, VMName: c.name, Node: c.node, Epoch: c.epoch, Outcome: "rescheduled",
				})
			}
			continue
		}
		// granted → UNKNOWN (single guarded statement; phase regression from
		// UNKNOWN happens only via fresh evidence in the convergence path).
		// Entering UNKNOWN clears the applied-revision watermark so that
		// stale pre-loss evidence cannot immediately re-converge when the
		// node returns — a FRESH report from a live session is required
		// (batch-review finding [10]).
		tag, err := s.pool.Exec(ctx, `
			UPDATE vms SET phase='UNKNOWN', applied_revision=0, observed_state='',
				resource_version = resource_version + 1, updated_at = now()
			WHERE id=$1 AND phase IN ('PROVISIONING','RUNNING','STOPPED')`, c.id)
		if err != nil {
			return actions, err
		}
		if tag.RowsAffected() == 1 {
			actions = append(actions, NodeLossAction{
				VMID: c.id, VMName: c.name, Node: c.node, Epoch: c.epoch, Outcome: "unknown",
			})
		}
	}

	// Wake UNKNOWN VMs whose node came back: fresh evidence will restore
	// the phase through convergence.
	if _, err := s.pool.Exec(ctx, `
		UPDATE vms v SET next_attempt_at = clock_timestamp()
		FROM nodes n
		WHERE v.node_name = n.name AND v.phase = 'UNKNOWN'
		  AND n.lease_expires_at >= clock_timestamp()`); err != nil {
		return actions, err
	}
	return actions, nil
}
