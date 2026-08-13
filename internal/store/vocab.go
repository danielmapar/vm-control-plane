package store

// This file defines the domain vocabulary as named string types with a fixed
// set of constants, so the same value is never spelled as a bare string in two
// places. The string values match the text stored in Postgres exactly; SQL
// literals inside queries use the same spellings.

// Phase is the reconciler-owned lifecycle phase of a VM row. The API never
// writes it; it is derived from reported evidence during reconciliation.
type Phase string

const (
	PhasePending      Phase = "PENDING"
	PhaseScheduling   Phase = "SCHEDULING"
	PhaseProvisioning Phase = "PROVISIONING"
	PhaseRunning      Phase = "RUNNING"
	PhaseStopped      Phase = "STOPPED"
	PhaseUnknown      Phase = "UNKNOWN"
	PhaseFailed       Phase = "FAILED"
	PhaseDeleting     Phase = "DELETING"
)

// OperationState is the state of a long-running operation. The last four are
// terminal and immutable.
type OperationState string

const (
	OpPending          OperationState = "PENDING"
	OpRunning          OperationState = "RUNNING"
	OpDone             OperationState = "DONE"
	OpFailed           OperationState = "FAILED"
	OpSuperseded       OperationState = "SUPERSEDED"
	OpDeadlineExceeded OperationState = "DEADLINE_EXCEEDED"
)

// terminal reports whether an operation state is final.
func (s OperationState) terminal() bool {
	switch s {
	case OpDone, OpFailed, OpSuperseded, OpDeadlineExceeded:
		return true
	}
	return false
}

// Verb is the mutation an operation represents.
type Verb string

const (
	VerbCreate             Verb = "CREATE"
	VerbUpdatePower        Verb = "UPDATE_POWER"
	VerbDelete             Verb = "DELETE"
	VerbSnapshotCreate     Verb = "SNAPSHOT_CREATE"
	VerbSnapshotRestore    Verb = "SNAPSHOT_RESTORE"
	VerbAdminRetryCleanup  Verb = "ADMIN_RETRY_CLEANUP"
	VerbAdminClearRecovery Verb = "ADMIN_CLEAR_RECOVERY"
)

// PlacementState tracks a placement through the grant/fencing ledger:
// assigned -> granted -> torn_down.
type PlacementState string

const (
	PlacementAssigned PlacementState = "assigned"
	PlacementGranted  PlacementState = "granted"
	PlacementTornDown PlacementState = "torn_down"
)

// ObservedState is the substrate power state an agent reports, as stored on the
// VM row. The empty value means "no evidence yet".
type ObservedState string

const (
	ObservedRunning ObservedState = "RUNNING"
	ObservedShutoff ObservedState = "SHUTOFF"
	ObservedAbsent  ObservedState = "ABSENT"
)
