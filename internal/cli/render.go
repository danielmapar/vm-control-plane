package cli

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"

	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

func printVMs(out io.Writer, vms ...*vmcv1.VirtualMachine) {
	w := tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tPHASE\tNODE\tEPOCH\tCPU\tMEMORY\tIMAGE\tREVISION")
	for _, vm := range vms {
		fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\t%s\t%s\t%d/%d\n",
			vm.GetMeta().GetName(),
			strings.TrimPrefix(vm.GetStatus().GetPhase().String(), "PHASE_"),
			vm.GetStatus().GetNode(),
			vm.GetStatus().GetPlacementEpoch(),
			vm.GetSpec().GetCpus(),
			humanSize(vm.GetSpec().GetMemoryBytes()),
			vm.GetSpec().GetImage(),
			vm.GetStatus().GetAppliedRevision(),
			vm.GetMeta().GetDesiredRevision(),
		)
	}
	_ = w.Flush()
}

func printOperation(out io.Writer, op *vmcv1.Operation) {
	w := tabwriter.NewWriter(out, 2, 4, 2, ' ', 0)
	fmt.Fprintln(w, "OPERATION\tVERB\tRESOURCE\tSTATE\tTARGET-REV\tERROR")
	fmt.Fprintf(w, "%s\t%s\t%s/%s\t%s\t%d\t%s\n",
		op.GetId(),
		strings.TrimPrefix(op.GetVerb().String(), "VERB_"),
		op.GetResourceType(), op.GetResourceName(),
		strings.TrimPrefix(op.GetState().String(), "OPERATION_STATE_"),
		op.GetTargetRevision(),
		op.GetError(),
	)
	_ = w.Flush()
}

func humanSize(b uint64) string {
	switch {
	case b >= 1<<30 && b%(1<<30) == 0:
		return fmt.Sprintf("%dGiB", b/(1<<30))
	case b >= 1<<20:
		return fmt.Sprintf("%dMiB", b/(1<<20))
	default:
		return strconv.FormatUint(b, 10)
	}
}
