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

// AgentServer implements vmc.v1.AgentService, the daemon-facing surface. It is
// a thin fence around the store's protocols: registration, leases, intent
// snapshots, grants, ordered reports, teardown receipts, and durable
// action-retry pacing.
type AgentServer struct {
	vmcv1.UnimplementedAgentServiceServer
	st *store.Store
}

// NewAgentServer returns an AgentServer backed by the given store.
func NewAgentServer(st *store.Store) *AgentServer { return &AgentServer{st: st} }

// RegisterHost registers the physical host and its logical nodes, returning one
// session per node. Node quotas must be in range and their sum must fit the
// host's capacity.
func (a *AgentServer) RegisterHost(ctx context.Context, req *vmcv1.RegisterHostRequest) (*vmcv1.RegisterHostResponse, error) {
	quotas, err := nodeQuotas(req.GetNodes())
	if err != nil {
		return nil, err
	}
	hMem, hMemOK := checkedBytes(req.GetMemoryBytes())
	hDisk, hDiskOK := checkedBytes(req.GetDiskBytes())
	if req.GetCpus() < 1 || !hMemOK || !hDiskOK {
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

// Heartbeat renews a node's lease. A superseded session is rejected, which is
// the daemon's signal to halt substrate actions.
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
			// The daemon's signal to halt substrate actions.
			return nil, status.Error(codes.FailedPrecondition, err.Error())
		}
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &vmcv1.HeartbeatResponse{}, nil
}

// PollIntents returns the authoritative desired-state snapshot for the caller's
// nodes, including tombstones (which drive teardown), each stamped with its
// current attempt pacing.
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

// RequestGrant admits (or denies) execution for a placement. A denial carries
// the reason and means the daemon must not touch the substrate for it.
func (a *AgentServer) RequestGrant(ctx context.Context, req *vmcv1.RequestGrantRequest) (*vmcv1.RequestGrantResponse, error) {
	sess, err := sessionFromProto(req.GetSession())
	if err != nil {
		return nil, err
	}
	vmID, err := vmIDArg(req.GetVmId())
	if err != nil {
		return nil, err
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

// Report records one piece of ordered observed-state evidence. A stale report
// (superseded session or epoch) is fenced with FailedPrecondition.
func (a *AgentServer) Report(ctx context.Context, req *vmcv1.ReportRequest) (*vmcv1.ReportResponse, error) {
	sess, err := sessionFromProto(req.GetSession())
	if err != nil {
		return nil, err
	}
	vmID, err := vmIDArg(req.GetVmId())
	if err != nil {
		return nil, err
	}
	state, ok := observedFromProto[req.GetState()]
	if !ok {
		return nil, status.Error(codes.InvalidArgument, "state must be specified")
	}
	err = a.st.ApplyReport(ctx, store.Report{
		Session: sess, VMID: vmID, Epoch: req.GetPlacementEpoch(),
		Seq: req.GetReportSeq(), AppliedRevision: req.GetAppliedRevision(),
		State: state, Detail: req.GetDetail(),
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

// TeardownReceipt records that a placement's substrate was torn down, releasing
// its reserved capacity. It is the one message a stale session may still deliver.
func (a *AgentServer) TeardownReceipt(ctx context.Context, req *vmcv1.TeardownReceiptRequest) (*vmcv1.TeardownReceiptResponse, error) {
	sess, err := sessionFromProto(req.GetSession())
	if err != nil {
		return nil, err
	}
	vmID, err := vmIDArg(req.GetVmId())
	if err != nil {
		return nil, err
	}
	// Receipts are the one message a stale session may deliver, but only for a
	// placement its node actually owned.
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

// actionRetryBackoffMS is the server-controlled backoff between substrate
// action attempts.
const actionRetryBackoffMS = 2000

// ActionFailed records a failed substrate action so the server can pace the
// next attempt; the daemon never decides its own backoff.
func (a *AgentServer) ActionFailed(ctx context.Context, req *vmcv1.ActionFailedRequest) (*vmcv1.ActionFailedResponse, error) {
	vmID, err := vmIDArg(req.GetVmId())
	if err != nil {
		return nil, err
	}
	token, err := uuid.Parse(req.GetAttemptToken())
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "attempt_token must be a UUID")
	}
	if err := a.st.RecordActionFailure(ctx, vmID, req.GetPlacementEpoch(), req.GetDesiredRevision(), token, actionRetryBackoffMS); err != nil {
		return nil, status.Error(codes.Internal, err.Error())
	}
	return &vmcv1.ActionFailedResponse{}, nil
}

// checkedBytes converts a byte count to int64, accepting only values in
// [1, 1 PiB]. The ceiling is far above any real capacity and guards the
// uint64->int64 conversion against wrapping to a negative value.
func checkedBytes(v uint64) (int64, bool) {
	const maxBytes = uint64(1) << 50 // 1 PiB
	return int64(v), v >= 1 && v <= maxBytes
}

// vmIDArg parses the vm_id field shared by the agent RPCs.
func vmIDArg(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, status.Error(codes.InvalidArgument, "vm_id must be a UUID")
	}
	return id, nil
}

// nodeQuotas converts and range-checks the advertised logical-node quotas.
func nodeQuotas(nodes []*vmcv1.NodeQuota) ([]store.NodeQuota, error) {
	quotas := make([]store.NodeQuota, 0, len(nodes))
	for _, n := range nodes {
		mem, memOK := checkedBytes(n.GetMemoryBytes())
		disk, diskOK := checkedBytes(n.GetDiskBytes())
		if n.GetCpus() < 1 || n.GetCpus() > 4096 || !memOK || !diskOK {
			return nil, status.Error(codes.InvalidArgument, "node quota out of range")
		}
		quotas = append(quotas, store.NodeQuota{
			Name: n.GetName(), CPUs: n.GetCpus(),
			MemoryBytes: mem, DiskBytes: disk, Labels: n.GetLabels(),
		})
	}
	return quotas, nil
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
