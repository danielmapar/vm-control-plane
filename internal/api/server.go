// Package api implements the public gRPC services. The API owns spec and
// tombstones and never writes phase or status; those belong to the reconciler.
// The two share only the store.
package api

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sigtunnel/vm-control-plane/internal/store"
	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

// Verb deadlines (database-clock): the bound after which an operation
// terminalizes as DEADLINE_EXCEEDED even if nothing is retrying.
const (
	createDeadline = 15 * time.Minute
	deleteDeadline = 30 * time.Minute
)

// Envelope claim retry: under READ COMMITTED a concurrent winner's row may not
// be visible yet, so we retry briefly, then surface a retryable ABORTED.
const (
	envelopeRetries = 5
	envelopeBackoff = 50 * time.Millisecond
)

// Server implements vmc.v1.VMService and vmc.v1.OperationService.
type Server struct {
	vmcv1.UnimplementedVMServiceServer
	vmcv1.UnimplementedOperationServiceServer

	st  *store.Store
	log *slog.Logger
}

// NewServer returns a Server backed by the given store.
func NewServer(st *store.Store, log *slog.Logger) *Server {
	return &Server{st: st, log: log}
}

// parseIdempotencyKey validates the client-minted key shared by every mutating
// verb.
func parseIdempotencyKey(raw string) (uuid.UUID, error) {
	key, err := uuid.Parse(raw)
	if err != nil || key == uuid.Nil {
		return uuid.Nil, status.Error(codes.InvalidArgument,
			"idempotency_key must be a non-nil UUID (minted client-side before the first attempt)")
	}
	return key, nil
}

// runIdempotent runs one attempt of a mutating verb, retrying the standard
// races: an envelope whose winner has not committed yet, and — when retryStale
// is set (no user-supplied precondition) — a lost resource_version CAS. Store
// errors are translated to gRPC status codes.
func (s *Server) runIdempotent(retryStale bool, attempt func() (*vmcv1.Operation, error)) (*vmcv1.Operation, error) {
	for i := 0; ; i++ {
		op, err := attempt()
		switch {
		case err == nil:
			return op, nil
		case errors.Is(err, store.ErrEnvelopeIncomplete) && i < envelopeRetries:
			time.Sleep(envelopeBackoff)
		case errors.Is(err, store.ErrEnvelopeIncomplete):
			return nil, status.Error(codes.Aborted, "concurrent request with the same idempotency key is in flight; retry")
		case retryStale && errors.Is(err, store.ErrStaleWrite) && i < envelopeRetries:
			// Transparent CAS retry: level-triggered re-read makes it safe.
		default:
			return nil, mapStoreErr(err)
		}
	}
}

// withEnvelope runs a mutating verb inside a fresh transaction after claiming
// its idempotency envelope. On replay it returns the original operation without
// invoking work. Otherwise work runs inside the winning transaction and must
// return the operation it created; withEnvelope completes the envelope and
// commits.
func (s *Server) withEnvelope(ctx context.Context, key uuid.UUID, method, name string, hash []byte,
	work func(tx pgx.Tx) (*store.Operation, error)) (*vmcv1.Operation, error) {
	tx, err := s.st.Pool().Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after commit

	existing, err := s.st.ClaimEnvelope(ctx, tx, key, method, "v1", "vm", name, hash)
	if err != nil {
		return nil, err
	}
	if existing != nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return operationToProto(existing), nil // replay
	}

	op, err := work(tx)
	if err != nil {
		return nil, err
	}
	if err := s.st.CompleteEnvelope(ctx, tx, key, op.ID); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return operationToProto(op), nil
}
