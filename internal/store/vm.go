// Package store is the single source of truth shared by the API and the
// reconciler; they never talk to each other directly, only through this
// package. It is the only package that runs SQL. Every row mutation
// compare-and-sets resource_version, so a stale writer loses cleanly instead
// of silently overwriting.
package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/protobuf/encoding/protojson"

	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

// Sentinel errors callers branch on.
var (
	ErrNotFound   = errors.New("store: not found")
	ErrStaleWrite = errors.New("store: stale write (resource_version mismatch)")
	ErrDuplicate  = errors.New("store: duplicate")
)

// VM is the database view of a VirtualMachine row.
type VM struct {
	ID              uuid.UUID
	Name            string
	Spec            *vmcv1.VmSpec
	Status          *vmcv1.VmStatus
	Phase           Phase
	SpecGeneration  int64
	ResourceVersion int64
	DesiredRevision int64
	NodeName        *string
	PlacementEpoch  int64
	ObservedState   ObservedState
	AppliedRevision int64
	DeletedAt       *time.Time
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// Store wraps the connection pool. It is the only type that touches SQL.
type Store struct {
	pool *pgxpool.Pool
}

// New returns a Store backed by pool.
func New(pool *pgxpool.Pool) *Store { return &Store{pool: pool} }

// Pool exposes the underlying pool so higher layers can open a transaction and
// combine several store methods atomically.
func (s *Store) Pool() *pgxpool.Pool { return s.pool }

// querier is the subset of pgx shared by *pgxpool.Pool and pgx.Tx. Methods
// that accept a querier can compose inside a caller's transaction (pass the
// tx) or run standalone (pass nil, which falls back to the pool); methods that
// manage their own transaction take no querier and use the pool directly.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

var (
	protoJSON          = protojson.MarshalOptions{UseProtoNames: false}
	protoJSONUnmarshal = protojson.UnmarshalOptions{DiscardUnknown: true}
)

// vmColumnNames is the single source of truth for the vms column order. Every
// SELECT list and every Scan destination derives from it, so the query and the
// scan can never drift apart.
var vmColumnNames = []string{
	"id", "name", "spec", "status", "phase", "spec_generation",
	"resource_version", "desired_revision", "node_name", "placement_epoch",
	"observed_state", "applied_revision", "deleted_at", "created_at", "updated_at",
}

// vmColumns is vmColumnNames as a SELECT list.
var vmColumns = strings.Join(vmColumnNames, ", ")

// prefixedVMColumns is vmColumns with each column qualified by a table alias.
func prefixedVMColumns(alias string) string {
	qualified := make([]string, len(vmColumnNames))
	for i, col := range vmColumnNames {
		qualified[i] = alias + "." + col
	}
	return strings.Join(qualified, ", ")
}

// CreateVM inserts a new VM row (phase PENDING, generation 1, revision 1).
// Pass a tx to compose it with the operation insertion in one transaction.
func (s *Store) CreateVM(ctx context.Context, q querier, id uuid.UUID, name string, spec *vmcv1.VmSpec) (*VM, error) {
	if q == nil {
		q = s.pool
	}
	specJSON, err := protoJSON.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}
	row := q.QueryRow(ctx, `
		INSERT INTO vms (id, name, spec, status)
		VALUES ($1, $2, $3, '{}')
		RETURNING `+vmColumns,
		id, name, specJSON)
	vm, err := scanVM(row)
	if isUniqueViolation(err) {
		return nil, fmt.Errorf("%w: vm %q", ErrDuplicate, name)
	}
	return vm, err
}

// GetVM fetches by name. Tombstoned rows are returned: deletion is a state,
// not an absence, until teardown finalizes.
func (s *Store) GetVM(ctx context.Context, q querier, name string) (*VM, error) {
	if q == nil {
		q = s.pool
	}
	row := q.QueryRow(ctx, `SELECT `+vmColumns+` FROM vms WHERE name = $1`, name)
	return scanVM(row)
}

// GetVMByID fetches by UUID.
func (s *Store) GetVMByID(ctx context.Context, q querier, id uuid.UUID) (*VM, error) {
	if q == nil {
		q = s.pool
	}
	row := q.QueryRow(ctx, `SELECT `+vmColumns+` FROM vms WHERE id = $1`, id)
	return scanVM(row)
}

// ListVMs returns rows ordered by name after the given name (keyset
// pagination; empty after = first page).
func (s *Store) ListVMs(ctx context.Context, q querier, after string, limit int) ([]*VM, error) {
	if q == nil {
		q = s.pool
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := q.Query(ctx, `
		SELECT `+vmColumns+` FROM vms
		WHERE name > $1 ORDER BY name LIMIT $2`, after, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*VM
	for rows.Next() {
		vm, err := scanVM(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, vm)
	}
	return out, rows.Err()
}

// UpdateVMStatus writes status and phase under a resource_version CAS. The
// caller passes the version it read; a concurrent writer makes this return
// ErrStaleWrite, and the caller re-reads and recomputes. Level-triggered loops
// make that safe by construction.
func (s *Store) UpdateVMStatus(ctx context.Context, q querier, id uuid.UUID, expectVersion int64, phase Phase, status *vmcv1.VmStatus) (*VM, error) {
	if q == nil {
		q = s.pool
	}
	statusJSON, err := protoJSON.Marshal(status)
	if err != nil {
		return nil, fmt.Errorf("marshal status: %w", err)
	}
	row := q.QueryRow(ctx, `
		UPDATE vms SET status = $3, phase = $4,
			resource_version = resource_version + 1, updated_at = now()
		WHERE id = $1 AND resource_version = $2
		RETURNING `+vmColumns,
		id, expectVersion, statusJSON, phase)
	vm, err := scanVM(row)
	if errors.Is(err, ErrNotFound) {
		return nil, staleOrMissing(ctx, q, id)
	}
	return vm, err
}

// TombstoneVM marks deletion: it sets deleted_at and bumps the desired
// revision, so deletion becomes one more desired state driven through the same
// reconciliation machinery. The API owns only the tombstone; the reconciler
// owns phase. Set-once: a replay against an already-tombstoned row returns it
// unchanged, with no version bump and no CAS conflict.
func (s *Store) TombstoneVM(ctx context.Context, q querier, id uuid.UUID, expectVersion int64) (*VM, error) {
	if q == nil {
		q = s.pool
	}
	row := q.QueryRow(ctx, `
		UPDATE vms SET
			deleted_at = now(),
			desired_revision = desired_revision + 1,
			resource_version = resource_version + 1,
			next_attempt_at = clock_timestamp(),
			updated_at = now()
		WHERE id = $1 AND resource_version = $2 AND deleted_at IS NULL
		RETURNING `+vmColumns,
		id, expectVersion)
	vm, err := scanVM(row)
	if !errors.Is(err, ErrNotFound) {
		return vm, err
	}
	// Zero rows: already tombstoned (idempotent replay), stale, or missing.
	existing, exErr := s.GetVMByID(ctx, q, id)
	if exErr != nil {
		return nil, exErr
	}
	if existing.DeletedAt != nil {
		return existing, nil
	}
	if existing.ResourceVersion != expectVersion {
		return nil, ErrStaleWrite
	}
	return nil, fmt.Errorf("tombstone: unexpected zero-row update for vm %s", id)
}

// UpdateVMPower rewrites the spec's power field (the only mutable spec field in
// v0.1) under the CAS: it bumps spec_generation and desired_revision and wakes
// the queue. The caller passes the whole updated spec (power flipped); the
// protojson blob is rewritten atomically with the version bump.
func (s *Store) UpdateVMPower(ctx context.Context, q querier, id uuid.UUID, expectVersion int64, spec *vmcv1.VmSpec) (*VM, error) {
	if q == nil {
		q = s.pool
	}
	specJSON, err := protoJSON.Marshal(spec)
	if err != nil {
		return nil, fmt.Errorf("marshal spec: %w", err)
	}
	row := q.QueryRow(ctx, `
		UPDATE vms SET spec = $3,
			spec_generation = spec_generation + 1,
			desired_revision = desired_revision + 1,
			next_attempt_at = clock_timestamp(),
			resource_version = resource_version + 1,
			updated_at = now()
		WHERE id = $1 AND resource_version = $2 AND deleted_at IS NULL
		RETURNING `+vmColumns,
		id, expectVersion, specJSON)
	vm, err := scanVM(row)
	if errors.Is(err, ErrNotFound) {
		return nil, staleOrMissing(ctx, q, id)
	}
	return vm, err
}

// DeleteVMRow removes a VM row inside the caller's transaction. It is the final
// step of finalization, once teardown is proven.
func (s *Store) DeleteVMRow(ctx context.Context, tx pgx.Tx, id uuid.UUID) error {
	_, err := tx.Exec(ctx, `DELETE FROM vms WHERE id = $1`, id)
	return err
}

// staleOrMissing disambiguates a zero-row CAS update: stale version vs a
// genuinely absent row.
func staleOrMissing(ctx context.Context, q querier, id uuid.UUID) error {
	var exists bool
	if err := q.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM vms WHERE id = $1)`, id).Scan(&exists); err != nil {
		return err
	}
	if exists {
		return ErrStaleWrite
	}
	return ErrNotFound
}

// vmScanDest returns the Scan destinations for vmColumnNames, in order. Both
// scanVM and scanClaimRow build their Scan call from it so the destination
// order stays locked to the column order.
func vmScanDest(vm *VM, specJSON, statusJSON *[]byte) []any {
	return []any{
		&vm.ID, &vm.Name, specJSON, statusJSON, &vm.Phase,
		&vm.SpecGeneration, &vm.ResourceVersion, &vm.DesiredRevision,
		&vm.NodeName, &vm.PlacementEpoch, &vm.ObservedState, &vm.AppliedRevision,
		&vm.DeletedAt, &vm.CreatedAt, &vm.UpdatedAt,
	}
}

// decodeVMBlobs unmarshals the spec/status protojson columns into vm.
func decodeVMBlobs(vm *VM, specJSON, statusJSON []byte) error {
	vm.Spec = &vmcv1.VmSpec{}
	if err := protoJSONUnmarshal.Unmarshal(specJSON, vm.Spec); err != nil {
		return fmt.Errorf("unmarshal spec: %w", err)
	}
	vm.Status = &vmcv1.VmStatus{}
	if len(statusJSON) > 0 && string(statusJSON) != "{}" {
		if err := protoJSONUnmarshal.Unmarshal(statusJSON, vm.Status); err != nil {
			return fmt.Errorf("unmarshal status: %w", err)
		}
	}
	return nil
}

func scanVM(row pgx.Row) (*VM, error) {
	var (
		vm         VM
		specJSON   []byte
		statusJSON []byte
	)
	err := row.Scan(vmScanDest(&vm, &specJSON, &statusJSON)...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := decodeVMBlobs(&vm, specJSON, statusJSON); err != nil {
		return nil, err
	}
	return &vm, nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
