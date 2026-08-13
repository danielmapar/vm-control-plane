package store

import (
	"context"
)

// IntentRow is the desired state of one VM for the daemon serving its node:
// the raw material for a poll response.
type IntentRow struct {
	VM       *VM
	NodeName string
}

// ListIntentsForNodes returns every VM assigned to the given nodes, including
// tombstoned ones, since a tombstone is itself an intent that drives teardown.
// The result is an authoritative snapshot for those nodes.
func (s *Store) ListIntentsForNodes(ctx context.Context, nodeNames []string) ([]*IntentRow, error) {
	if len(nodeNames) == 0 {
		return nil, nil
	}
	rows, err := s.pool.Query(ctx, `
		SELECT `+vmColumns+` FROM vms
		WHERE node_name = ANY($1)
		ORDER BY name`, nodeNames)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*IntentRow
	for rows.Next() {
		vm, err := scanVM(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, &IntentRow{VM: vm, NodeName: *vm.NodeName})
	}
	return out, rows.Err()
}
