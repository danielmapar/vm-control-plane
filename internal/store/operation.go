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
	Verb           Verb
	TargetRevision int64
	State          OperationState
	Error          string
	Deadline       time.Time
	CreatedAt      time.Time
	FinishedAt     *time.Time
}

// Terminal reports whether the operation state is final. Terminal results are
// immutable: TerminalizeOperation refuses to touch them.
func (o *Operation) Terminal() bool { return o.State.terminal() }

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

// ErrEnvelopeMismatch means the same idempotency key was reused with a
// different method or request; it is never silently answered with an unrelated
// operation.
var ErrEnvelopeMismatch = errors.New("store: idempotency key reused with a different request")

// ErrEnvelopeIncomplete means the envelope row exists but its winner has not
// committed an operation yet (a concurrent create is in flight, or the winner
// rolled back). Callers retry briefly, then surface a retryable error.
var ErrEnvelopeIncomplete = errors.New("store: envelope exists without a committed operation")

const opColumns = `id, resource_type, resource_id, resource_name, verb,
	target_revision, state, error, deadline, created_at, finished_at`

// defaultOperationDeadline bounds an operation when the caller gives no per-verb
// budget.
const defaultOperationDeadline = 15 * time.Minute

// ClaimEnvelope is the serialization point for a mutating verb: it inserts the
// envelope row, or (on conflict) compares the canonical request hash and
// returns the winner's operation.
//
// It returns (nil, nil) when this caller inserted the envelope and owns the
// verb: the caller must then create the operation and call CompleteEnvelope in
// the same transaction, so the envelope never points at nothing after commit.
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
		// The conflicting insert has not committed yet (or rolled back), so it
		// is invisible under READ COMMITTED. The caller retries briefly.
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

// CompleteEnvelope points the envelope at the operation it produced. It must
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

// CreateOperationParams is the input to CreateOperation. Operation is the
// stored result; keeping them separate means a caller never populates
// result-only fields (State, Deadline) that creation would ignore.
type CreateOperationParams struct {
	ID             uuid.UUID
	ResourceType   string
	ResourceID     uuid.UUID
	ResourceName   string
	Verb           Verb
	TargetRevision int64
	// DeadlineBudget is the per-verb duration; the stored deadline is computed
	// from it against the database clock. Zero means defaultOperationDeadline;
	// a negative value is legal so tests can mint already-expired operations.
	DeadlineBudget time.Duration
}

// CreateOperation inserts a PENDING operation. The deadline is computed from
// the database clock plus the per-verb budget, so an API host with a skewed
// clock cannot lengthen or shorten operation lifetimes.
func (s *Store) CreateOperation(ctx context.Context, q querier, p CreateOperationParams) (*Operation, error) {
	if q == nil {
		q = s.pool
	}
	budget := p.DeadlineBudget
	if budget == 0 {
		budget = defaultOperationDeadline
	}
	row := q.QueryRow(ctx, `
		INSERT INTO operations (id, resource_type, resource_id, resource_name, verb, target_revision, state, deadline)
		VALUES ($1, $2, $3, $4, $5, $6, 'PENDING', clock_timestamp() + $7)
		RETURNING `+opColumns,
		p.ID, p.ResourceType, p.ResourceID, p.ResourceName, p.Verb, p.TargetRevision, budget)
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

// TerminalizeOperation moves a non-terminal operation to a terminal state. A
// second terminalization is a no-op that returns the already-terminal row, so
// late convergence can never rewrite a result.
func (s *Store) TerminalizeOperation(ctx context.Context, q querier, id uuid.UUID, state OperationState, errMsg string) (*Operation, error) {
	if q == nil {
		q = s.pool
	}
	if !state.terminal() {
		return nil, fmt.Errorf("terminalize: %q is not a terminal state", state)
	}
	row := q.QueryRow(ctx, `
		UPDATE operations SET state = $2, error = $3, finished_at=clock_timestamp()
		WHERE id = $1 AND state IN ('PENDING','RUNNING')
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

// TerminalizeRealizedOps completes the operations targeting exactly the
// realized revision as DONE, and supersedes any older still-open operations
// for the same resource. Every operation terminates. Runs inside the caller's
// transaction.
func (s *Store) TerminalizeRealizedOps(ctx context.Context, tx pgx.Tx, vmID uuid.UUID, realizedRevision int64) error {
	if _, err := tx.Exec(ctx, `
		UPDATE operations SET state = 'DONE', finished_at=clock_timestamp()
		WHERE resource_type = 'vm' AND resource_id = $1 AND target_revision = $2 AND state IN ('PENDING','RUNNING')`,
		vmID, realizedRevision); err != nil {
		return err
	}
	_, err := tx.Exec(ctx, `
		UPDATE operations SET state = 'SUPERSEDED',
			error = 'a newer desired revision was realized first', finished_at=clock_timestamp()
		WHERE resource_type = 'vm' AND resource_id = $1 AND target_revision<$2 AND state IN ('PENDING','RUNNING')`,
		vmID, realizedRevision)
	return err
}

// FindOpenOperationID returns the most recent still-open operation for a
// resource and target revision, or nil if there is none. It is best-effort:
// callers use it to attach a failure to the operation the client is awaiting.
func (s *Store) FindOpenOperationID(ctx context.Context, q querier, vmID uuid.UUID, revision int64) (*uuid.UUID, error) {
	if q == nil {
		q = s.pool
	}
	var id uuid.UUID
	err := q.QueryRow(ctx, `
		SELECT id FROM operations
		WHERE resource_id = $1 AND target_revision = $2 AND state IN ('PENDING','RUNNING')
		ORDER BY created_at DESC LIMIT 1`, vmID, revision).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &id, nil
}

// ExpireOperations terminalizes every non-terminal operation whose
// database-clock deadline has passed. This is the mechanism that makes
// operations terminate even when no retry budget is being consumed. It returns
// the expired operations.
func (s *Store) ExpireOperations(ctx context.Context, q querier) ([]*Operation, error) {
	if q == nil {
		q = s.pool
	}
	rows, err := q.Query(ctx, `
		UPDATE operations SET state = 'DEADLINE_EXCEEDED',
			error = 'operation deadline exceeded; resource state unchanged (no unsafe cleanup)',
			finished_at=clock_timestamp()
		WHERE state IN ('PENDING','RUNNING') AND deadline <= clock_timestamp()
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
	// Not secret material; a constant-time comparison is unnecessary.
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
