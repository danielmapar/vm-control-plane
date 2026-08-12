package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Operation is the database view of a long-running operation row.
type Operation struct {
	ID             uuid.UUID
	ResourceType   string
	ResourceID     uuid.UUID
	ResourceName   string
	Verb           string
	TargetRevision int64
	State          string
	Error          string
	Deadline       time.Time
	CreatedAt      time.Time
	FinishedAt     *time.Time
}

// Terminal reports whether the operation state is final. Terminal results
// are immutable: TerminalizeOperation refuses to touch them.
func (o *Operation) Terminal() bool {
	switch o.State {
	case "DONE", "FAILED", "SUPERSEDED", "DEADLINE_EXCEEDED":
		return true
	}
	return false
}

// Envelope is a stored idempotency envelope.
type Envelope struct {
	Key          uuid.UUID
	Method       string
	APIVersion   string
	ResourceType string
	ResourceName string
	RequestHash  []byte
	OperationID  *uuid.UUID
	CreatedAt    time.Time
}

// ErrEnvelopeMismatch: same idempotency key, different method or request —
// never silently answered with an unrelated operation (D3).
var ErrEnvelopeMismatch = errors.New("store: idempotency key reused with a different request")

// ErrEnvelopeIncomplete: the envelope row exists but its winner has not
// committed an operation yet (concurrent create in flight, or the winner
// rolled back). Callers retry briefly, then surface a retryable error.
var ErrEnvelopeIncomplete = errors.New("store: envelope exists without a committed operation")

const opColumns = `id, resource_type, resource_id, resource_name, verb,
	target_revision, state, error, deadline, created_at, finished_at`

// ClaimEnvelope is the serialization point for a mutating verb (D3): it
// inserts the envelope row, or — on conflict — compares the canonical
// request hash and returns the winner's operation.
//
// Returns (nil, nil) when this caller inserted the envelope and owns the
// verb: it must create the operation and CompleteEnvelope inside the SAME
// transaction, so the envelope never points at nothing after commit.
func (s *Store) ClaimEnvelope(ctx context.Context, tx pgx.Tx, key uuid.UUID, method, apiVersion, resourceType, resourceName string, requestHash []byte) (*Operation, error) {
	tag, err := tx.Exec(ctx, `
		INSERT INTO idempotency_envelopes (key, method, api_version, resource_type, resource_name, request_hash)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (key) DO NOTHING`,
		key, method, apiVersion, resourceType, resourceName, requestHash)
	if err != nil {
		return nil, err
	}
	if tag.RowsAffected() == 1 {
		return nil, nil // we own the verb
	}

	// Conflict: read the committed envelope and compare.
	var env Envelope
	err = tx.QueryRow(ctx, `
		SELECT key, method, api_version, resource_type, resource_name, request_hash, operation_id, created_at
		FROM idempotency_envelopes WHERE key = $1`, key).
		Scan(&env.Key, &env.Method, &env.APIVersion, &env.ResourceType,
			&env.ResourceName, &env.RequestHash, &env.OperationID, &env.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		// The conflicting insert has not committed yet (or rolled back):
		// invisible under READ COMMITTED. The caller retries briefly.
		return nil, ErrEnvelopeIncomplete
	}
	if err != nil {
		return nil, err
	}
	if env.Method != method || env.APIVersion != apiVersion ||
		env.ResourceType != resourceType || env.ResourceName != resourceName ||
		!hashEqual(env.RequestHash, requestHash) {
		return nil, fmt.Errorf("%w: key %s first used by %s on %s/%s",
			ErrEnvelopeMismatch, key, env.Method, env.ResourceType, env.ResourceName)
	}
	if env.OperationID == nil {
		return nil, ErrEnvelopeIncomplete
	}
	return s.GetOperation(ctx, tx, *env.OperationID)
}

// CompleteEnvelope points the envelope at the operation it produced. Must
// run in the same transaction as ClaimEnvelope's insert and the operation
// insert.
func (s *Store) CompleteEnvelope(ctx context.Context, tx pgx.Tx, key, operationID uuid.UUID) error {
	tag, err := tx.Exec(ctx,
		`UPDATE idempotency_envelopes SET operation_id = $2 WHERE key = $1 AND operation_id IS NULL`,
		key, operationID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("envelope %s: already completed or missing", key)
	}
	return nil
}

// CreateOperation inserts a PENDING operation with its per-verb deadline.
func (s *Store) CreateOperation(ctx context.Context, q querier, op *Operation) (*Operation, error) {
	if q == nil {
		q = s.pool
	}
	row := q.QueryRow(ctx, `
		INSERT INTO operations (id, resource_type, resource_id, resource_name, verb, target_revision, state, deadline)
		VALUES ($1, $2, $3, $4, $5, $6, 'PENDING', $7)
		RETURNING `+opColumns,
		op.ID, op.ResourceType, op.ResourceID, op.ResourceName, op.Verb, op.TargetRevision, op.Deadline)
	return scanOperation(row)
}

// GetOperation fetches by id.
func (s *Store) GetOperation(ctx context.Context, q querier, id uuid.UUID) (*Operation, error) {
	if q == nil {
		q = s.pool
	}
	row := q.QueryRow(ctx, `SELECT `+opColumns+` FROM operations WHERE id = $1`, id)
	return scanOperation(row)
}

// TerminalizeOperation moves a non-terminal operation to a terminal state.
// Terminal results are immutable: a second terminalization is a no-op that
// returns the already-terminal row — late convergence can never rewrite a
// result (D3).
func (s *Store) TerminalizeOperation(ctx context.Context, q querier, id uuid.UUID, state, errMsg string) (*Operation, error) {
	if q == nil {
		q = s.pool
	}
	switch state {
	case "DONE", "FAILED", "SUPERSEDED", "DEADLINE_EXCEEDED":
	default:
		return nil, fmt.Errorf("terminalize: %q is not a terminal state", state)
	}
	row := q.QueryRow(ctx, `
		UPDATE operations SET state=$2, error=$3, finished_at=now()
		WHERE id=$1 AND state IN ('PENDING','RUNNING')
		RETURNING `+opColumns,
		id, state, errMsg)
	op, err := scanOperation(row)
	if errors.Is(err, ErrNotFound) {
		// Already terminal (immutability) or genuinely missing.
		existing, getErr := s.GetOperation(ctx, q, id)
		if getErr != nil {
			return nil, getErr
		}
		return existing, nil
	}
	return op, err
}

// ExpireOperations terminalizes every non-terminal operation whose
// database-clock deadline has passed — the mechanism that makes operations
// terminate even when no retry budget is being consumed. Returns the
// expired operations.
func (s *Store) ExpireOperations(ctx context.Context, q querier) ([]*Operation, error) {
	if q == nil {
		q = s.pool
	}
	rows, err := q.Query(ctx, `
		UPDATE operations SET state='DEADLINE_EXCEEDED',
			error='operation deadline exceeded; resource state unchanged (no unsafe cleanup)',
			finished_at=now()
		WHERE state IN ('PENDING','RUNNING') AND deadline < clock_timestamp()
		RETURNING `+opColumns)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*Operation
	for rows.Next() {
		op, err := scanOperation(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, op)
	}
	return out, rows.Err()
}

func scanOperation(row pgx.Row) (*Operation, error) {
	var op Operation
	err := row.Scan(&op.ID, &op.ResourceType, &op.ResourceID, &op.ResourceName,
		&op.Verb, &op.TargetRevision, &op.State, &op.Error, &op.Deadline,
		&op.CreatedAt, &op.FinishedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &op, nil
}

func hashEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	// Not secret material; constant-time comparison is unnecessary.
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
