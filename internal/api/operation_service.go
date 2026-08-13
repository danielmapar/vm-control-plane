package api

import (
	"context"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

// GetOperation returns one operation by id.
func (s *Server) GetOperation(ctx context.Context, req *vmcv1.GetOperationRequest) (*vmcv1.Operation, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "operation id must be a UUID")
	}
	op, err := s.st.GetOperation(ctx, nil, id)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	return operationToProto(op), nil
}

// WaitOperation polls until the operation is terminal or the requested wait
// elapses. The server caps the wait; clients keep their own defensive deadline.
func (s *Server) WaitOperation(ctx context.Context, req *vmcv1.WaitOperationRequest) (*vmcv1.Operation, error) {
	id, err := uuid.Parse(req.GetId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "operation id must be a UUID")
	}
	wait, err := serverWait(req.GetTimeout())
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(wait)
	tick := time.NewTicker(200 * time.Millisecond)
	defer tick.Stop()
	for {
		op, err := s.st.GetOperation(ctx, nil, id)
		if err != nil {
			return nil, mapStoreErr(err)
		}
		if op.Terminal() || time.Now().After(deadline) {
			return operationToProto(op), nil
		}
		select {
		case <-ctx.Done():
			return nil, status.FromContextError(ctx.Err()).Err()
		case <-tick.C:
		}
	}
}

// serverWait clamps the requested wait to a server-side maximum.
func serverWait(d *durationpb.Duration) (time.Duration, error) {
	const maxWait = 2 * time.Minute
	wait := 30 * time.Second
	if d == nil {
		return wait, nil
	}
	if err := d.CheckValid(); err != nil {
		return 0, status.Error(codes.InvalidArgument, "timeout: "+err.Error())
	}
	v := d.AsDuration()
	if v < 0 {
		return 0, status.Error(codes.InvalidArgument, "timeout must not be negative")
	}
	if v > 0 {
		wait = min(v, maxWait)
	}
	return wait, nil
}
