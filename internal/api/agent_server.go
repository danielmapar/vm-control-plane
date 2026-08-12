package api

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"github.com/sigtunnel/vm-control-plane/internal/store"
	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

// AgentServer implements vmc.v1.AgentService — the daemon-facing surface.
// It is a thin fence around the store's protocols: registration, leases,
// intent snapshots, grants, ordered reports, teardown receipts, and
// durable action-retry pacing.
type AgentServer struct {
	vmcv1.UnimplementedAgentServiceServer
	st *store.Store
}

func NewAgentServer(st *store.Store) *AgentServer { return &AgentServer{st: st} }

func (a *AgentServer) RegisterHost(ctx context.Context, req *vmcv1.RegisterHostRequest) (*vmcv1.RegisterHostResponse, error) {
	const maxBytes = int64(1) << 50 // 1 PiB — practical ceiling, guards uint64→int64 wrap
	toInt64 := func(v uint64) (int64, bool) { return int64(v), v <= uint64(maxBytes) }
	quotas := make([]store.NodeQuota, 0, len(req.GetNodes()))
	for _, n := range req.GetNodes() {
		mem, okM := toInt64(n.GetMemoryBytes())
		disk, okD := toInt64(n.GetDiskBytes())
		if n.GetCpus() < 1 || n.GetCpus() > 4096 || !okM || !okD || mem < 1 || disk < 1 {
			return nil, status.Error(codes.InvalidArgument, "node quota out of range")
		}
		quotas = append(quotas, store.NodeQuota{
			Name: n.GetName(), CPUs: n.GetCpus(),
			MemoryBytes: mem, DiskBytes: disk, Labels: n.GetLabels(),
		})
	}
	hMem, okHM := toInt64(req.GetMemoryBytes())
	hDisk, okHD := toInt64(req.GetDiskBytes())
	if req.GetCpus() < 1 || !okHM || !okHD {
		return nil, status.Error(codes.InvalidArgument, "host capacity out of range")
	}
	lease := 15 * time.Second
	if d := req.GetLease(); d != nil {
		if v := d.AsDuration(); v > 0 {
			lease = min(v, 60*time.Second) // server-capped: a client cannot keep a dead node Ready forever
		}
	}
	if req.GetHostId() == "" || len(quotas) == 0 {
		return nil, status.Error(codes.InvalidArgument, "host_id and at least one node are required")
	}
	sessions, err := a.st.RegisterHost(ctx, store.HostCapacity{
		HostID: req.GetHostId(), CPUs: req.GetCpus(),
		MemoryBytes: hMem, DiskBytes: hDisk,
	}, quotas, lease)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrQuotaExceedsHost):
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		case errors.Is(err, store.ErrHostMismatch):
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	resp := &vmcv1.RegisterHostResponse{}
	for _, s := range sessions {
		resp.Sessions = append(resp.Sessions, &vmcv1.NodeSession{
			NodeName: s.NodeName, SessionId: s.SessionID.String(), SessionGeneration: s.Generation,
		})
	}
	return resp, nil
}

func (a *AgentServer) Heartbeat(ctx context.Context, req *vmcv1.HeartbeatRequest) (*vmcv1.HeartbeatResponse, error) {
	sess, err := sessionFromProto(req.GetSession())
	if err != nil {
		return nil, err
	}
	lease := 15 * time.Second
	if d := req.GetLease(); d != nil && d.AsDuration() > 0 {
		lease = d.AsDuration()
	}
	if err := a.st.Heartbeat(ctx, sess, lease); err != nil {
		if errors.Is(err, store.ErrStaleSession) {
			// The daemon's signal to HALT substrate actions (§6.3).
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &vmcv1.HeartbeatResponse{}, nil
}

func (a *AgentServer) PollIntents(ctx context.Context, req *vmcv1.PollIntentsRequest) (*vmcv1.PollIntentsResponse, error) {
	names := make([]string, 0, len(req.GetSessions()))
	for _, s := range req.GetSessions() {
		names = append(names, s.GetNodeName())
	}
	rows, err := a.st.ListIntentsForNodes(ctx, names)
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	resp := &vmcv1.PollIntentsResponse{
		SnapshotToken: uuid.NewString(),
		Complete:      true, // single-query snapshot: always complete (GC-legal)
	}
	for _, row := range rows {
		notBeforeMS, token, err := a.st.NextActionAttempt(ctx, row.VM.ID, row.VM.PlacementEpoch, row.VM.DesiredRevision)
		if err != nil {
			return nil, status.Error(codes.Internal, err.Error())
		}
		resp.Intents = append(resp.Intents, &vmcv1.Intent{
			VmId: row.VM.ID.String(), VmName: row.VM.Name, NodeName: row.NodeName,
			PlacementEpoch: row.VM.PlacementEpoch, DesiredRevision: row.VM.DesiredRevision,
			Deleted: row.VM.DeletedAt != nil, Spec: row.VM.Spec,
			NotBeforeMs: notBeforeMS, AttemptToken: token.String(),
		})
	}
	return resp, nil
}

func (a *AgentServer) RequestGrant(ctx context.Context, req *vmcv1.RequestGrantRequest) (*vmcv1.RequestGrantResponse, error) {
	sess, err := sessionFromProto(req.GetSession())
	if err != nil {
		return nil, err
	}
	vmID, err := uuid.Parse(req.GetVmId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "vm_id must be a UUID")
	}
	err = a.st.GrantExecution(ctx, sess, vmID, req.GetPlacementEpoch())
	var denied *store.ErrGrantDenied
	switch {
	case err == nil:
		return &vmcv1.RequestGrantResponse{Granted: true}, nil
	case errors.As(err, &denied):
		return &vmcv1.RequestGrantResponse{Granted: false, Reason: string(denied.Reason)}, nil
	case errors.Is(err, store.ErrNotFound):
		return &vmcv1.RequestGrantResponse{Granted: false, Reason: "vm-not-found"}, nil
	default:
		return nil, status.Error(codes.Internal, err.Error())
	}
}

func (a *AgentServer) Report(ctx context.Context, req *vmcv1.ReportRequest) (*vmcv1.ReportResponse, error) {
	sess, err := sessionFromProto(req.GetSession())
	if err != nil {
		return nil, err
	}
	vmID, err := uuid.Parse(req.GetVmId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "vm_id must be a UUID")
	}
	stateName := map[vmcv1.ObservedState]string{
		vmcv1.ObservedState_OBSERVED_STATE_RUNNING: "RUNNING",
		vmcv1.ObservedState_OBSERVED_STATE_SHUTOFF: "SHUTOFF",
		vmcv1.ObservedState_OBSERVED_STATE_ABSENT:  "ABSENT",
	}[req.GetState()]
	if stateName == "" {
		return nil, status.Error(codes.InvalidArgument, "state must be specified")
	}
	err = a.st.ApplyReport(ctx, store.Report{
		Session: sess, VMID: vmID, Epoch: req.GetPlacementEpoch(),
		Seq: req.GetReportSeq(), AppliedRevision: req.GetAppliedRevision(),
		State: stateName, Detail: req.GetDetail(),
	})
	if errors.Is(err, store.ErrStaleReport) {
		// Fenced, not fatal: the daemon should not retry this report.
		return nil, status.Error(codes.FailedPrecondition, err.Error())
	}
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &vmcv1.ReportResponse{}, nil
}

func (a *AgentServer) TeardownReceipt(ctx context.Context, req *vmcv1.TeardownReceiptRequest) (*vmcv1.TeardownReceiptResponse, error) {
	sess, err := sessionFromProto(req.GetSession())
	if err != nil {
		return nil, err
	}
	vmID, err := uuid.Parse(req.GetVmId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "vm_id must be a UUID")
	}
	// Receipts are the one message a STALE session may deliver — but only
	// for a placement its node actually owned (§6.4).
	p, err := a.st.GetPlacement(ctx, nil, vmID, req.GetPlacementEpoch())
	if errors.Is(err, store.ErrNotFound) {
		return &vmcv1.TeardownReceiptResponse{}, nil // already finalized: idempotent
	}
	if err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	if p.NodeName != sess.NodeName {
		return nil, status.Error(codes.PermissionDenied, "receipt from a node that never owned this placement")
	}
	if err := a.st.MarkTeardownComplete(ctx, vmID, req.GetPlacementEpoch()); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &vmcv1.TeardownReceiptResponse{}, nil
}

func (a *AgentServer) ActionFailed(ctx context.Context, req *vmcv1.ActionFailedRequest) (*vmcv1.ActionFailedResponse, error) {
	vmID, err := uuid.Parse(req.GetVmId())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "vm_id must be a UUID")
	}
	token, err := uuid.Parse(req.GetAttemptToken())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "attempt_token must be a UUID")
	}
	// Exponential-ish pacing server-side; the daemon never decides backoff.
	if err := a.st.RecordActionFailure(ctx, vmID, req.GetPlacementEpoch(), req.GetDesiredRevision(), token, 2000); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &vmcv1.ActionFailedResponse{}, nil
}

func sessionFromProto(p *vmcv1.NodeSession) (store.Session, error) {
	if p == nil {
		return store.Session{}, status.Error(codes.InvalidArgument, "session is required")
	}
	id, err := uuid.Parse(p.GetSessionId())
	if err != nil {
		return store.Session{}, status.Error(codes.InvalidArgument, "session_id must be a UUID")
	}
	return store.Session{NodeName: p.GetNodeName(), SessionID: id, Generation: p.GetSessionGeneration()}, nil
}
