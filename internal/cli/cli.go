// Package cli implements vmctl: the operator CLI over the same public gRPC
// API as any other client — no privileged side channel (ADR-0001).
package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/types/known/durationpb"

	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

// New builds the vmctl command tree. Output goes to out so tests can
// capture it.
func New(out io.Writer) *cobra.Command {
	var server string

	root := &cobra.Command{
		Use:           "vmctl",
		Short:         "Operate the vm-control-plane",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVar(&server, "server", envOr("VMCTL_SERVER", "127.0.0.1:7070"), "control-plane gRPC address")

	root.AddCommand(newCreateCmd(out, &server))
	root.AddCommand(newGetCmd(out, &server))
	root.AddCommand(newListCmd(out, &server))
	root.AddCommand(newDeleteCmd(out, &server))
	root.AddCommand(newOpCmd(out, &server))
	return root
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func dial(server string) (*grpc.ClientConn, error) {
	return grpc.NewClient(server, grpc.WithTransportCredentials(insecure.NewCredentials()))
}

// parseSize accepts "512MiB", "2GiB", "10GiB" or raw bytes.
func parseSize(s string) (uint64, error) {
	s = strings.TrimSpace(s)
	mult := uint64(1)
	switch {
	case strings.HasSuffix(s, "GiB"):
		mult, s = 1<<30, strings.TrimSuffix(s, "GiB")
	case strings.HasSuffix(s, "MiB"):
		mult, s = 1<<20, strings.TrimSuffix(s, "MiB")
	}
	n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("size %q: %w", s, err)
	}
	return n * mult, nil
}

func newCreateCmd(out io.Writer, server *string) *cobra.Command {
	var (
		cpus    uint32
		memory  string
		image   string
		disk    string
		network string
		idemKey string
	)
	cmd := &cobra.Command{
		Use:   "create vm NAME",
		Short: "Create a VM (returns an async operation)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != "vm" {
				return fmt.Errorf("only 'vm' resources are supported, got %q", args[0])
			}
			mem, err := parseSize(memory)
			if err != nil {
				return err
			}
			dsk, err := parseSize(disk)
			if err != nil {
				return err
			}
			conn, err := dial(*server)
			if err != nil {
				return err
			}
			defer conn.Close() //nolint:errcheck // process exit follows

			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			// The key exists BEFORE the first attempt and is printed first:
			// after an ambiguous failure the user can replay with
			// --idempotency-key to get the original operation (D3).
			if idemKey == "" {
				idemKey = uuid.NewString()
			}
			fmt.Fprintf(out, "idempotency-key: %s  (replay with --idempotency-key on ambiguous failures)\n", idemKey)
			op, err := vmcv1.NewVMServiceClient(conn).CreateVm(ctx, &vmcv1.CreateVmRequest{
				IdempotencyKey: idemKey,
				Name:           args[1],
				Spec: &vmcv1.VmSpec{
					Cpus:          cpus,
					MemoryBytes:   mem,
					Image:         image,
					RootDiskBytes: dsk,
					Network:       network,
					Power:         vmcv1.PowerState_POWER_STATE_RUNNING,
				},
			})
			if err != nil {
				return err
			}
			printOperation(out, op)
			return nil
		},
	}
	cmd.Flags().Uint32Var(&cpus, "cpu", 1, "virtual CPUs")
	cmd.Flags().StringVar(&memory, "memory", "512MiB", "guest memory (e.g. 2GiB)")
	cmd.Flags().StringVar(&image, "image", "ubuntu-24.04", "backing image")
	cmd.Flags().StringVar(&disk, "disk", "10GiB", "root disk size")
	cmd.Flags().StringVar(&network, "network", "", "tenant network (optional)")
	cmd.Flags().StringVar(&idemKey, "idempotency-key", "", "replay a previous attempt's key (UUID)")
	return cmd
}

func newGetCmd(out io.Writer, server *string) *cobra.Command {
	return &cobra.Command{
		Use:   "get vm NAME",
		Short: "Show one VM",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := dial(*server)
			if err != nil {
				return err
			}
			defer conn.Close() //nolint:errcheck // process exit follows
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			vm, err := vmcv1.NewVMServiceClient(conn).GetVm(ctx, &vmcv1.GetVmRequest{Name: args[1]})
			if err != nil {
				return err
			}
			printVMs(out, vm)
			return nil
		},
	}
}

func newListCmd(out io.Writer, server *string) *cobra.Command {
	return &cobra.Command{
		Use:   "list vms",
		Short: "List VMs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := dial(*server)
			if err != nil {
				return err
			}
			defer conn.Close() //nolint:errcheck // process exit follows
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			client := vmcv1.NewVMServiceClient(conn)
			var all []*vmcv1.VirtualMachine
			token, prev := "", "-"
			for token != prev {
				prev = token
				resp, err := client.ListVms(ctx, &vmcv1.ListVmsRequest{PageToken: token})
				if err != nil {
					return err
				}
				all = append(all, resp.Vms...)
				token = resp.GetNextPageToken()
				if token == "" {
					break
				}
			}
			printVMs(out, all...)
			return nil
		},
	}
}

func newDeleteCmd(out io.Writer, server *string) *cobra.Command {
	return &cobra.Command{
		Use:   "delete vm NAME",
		Short: "Delete a VM (tombstone; teardown is reconciled)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := dial(*server)
			if err != nil {
				return err
			}
			defer conn.Close() //nolint:errcheck // process exit follows
			ctx, cancel := context.WithTimeout(cmd.Context(), 30*time.Second)
			defer cancel()
			op, err := vmcv1.NewVMServiceClient(conn).DeleteVm(ctx, &vmcv1.DeleteVmRequest{
				IdempotencyKey: uuid.NewString(),
				Name:           args[1],
			})
			if err != nil {
				return err
			}
			printOperation(out, op)
			return nil
		},
	}
}

func newOpCmd(out io.Writer, server *string) *cobra.Command {
	op := &cobra.Command{Use: "op", Short: "Operations"}

	var waitTimeout time.Duration
	wait := &cobra.Command{
		Use:   "wait ID",
		Short: "Wait for an operation to terminalize",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := dial(*server)
			if err != nil {
				return err
			}
			defer conn.Close() //nolint:errcheck // process exit follows
			// Defensive client deadline on top of the server's cap (D3):
			// op wait can never hang, even against a wedged server.
			ctx, cancel := context.WithTimeout(cmd.Context(), waitTimeout+15*time.Second)
			defer cancel()
			client := vmcv1.NewOperationServiceClient(conn)
			deadline := time.Now().Add(waitTimeout)
			for {
				op, err := client.WaitOperation(ctx, &vmcv1.WaitOperationRequest{
					Id:      args[0],
					Timeout: durationpb.New(time.Until(deadline)),
				})
				if err != nil {
					return err
				}
				if terminal(op.State) || time.Now().After(deadline) {
					printOperation(out, op)
					if op.State != vmcv1.OperationState_OPERATION_STATE_DONE {
						return fmt.Errorf("operation %s: %s %s", op.Id, op.State, op.Error)
					}
					return nil
				}
			}
		},
	}
	wait.Flags().DurationVar(&waitTimeout, "timeout", 2*time.Minute, "how long to wait before giving up")

	get := &cobra.Command{
		Use:   "get ID",
		Short: "Show an operation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			conn, err := dial(*server)
			if err != nil {
				return err
			}
			defer conn.Close() //nolint:errcheck // process exit follows
			ctx, cancel := context.WithTimeout(cmd.Context(), 10*time.Second)
			defer cancel()
			op, err := vmcv1.NewOperationServiceClient(conn).GetOperation(ctx, &vmcv1.GetOperationRequest{Id: args[0]})
			if err != nil {
				return err
			}
			printOperation(out, op)
			return nil
		},
	}

	op.AddCommand(wait, get)
	return op
}

func terminal(s vmcv1.OperationState) bool {
	switch s {
	case vmcv1.OperationState_OPERATION_STATE_DONE,
		vmcv1.OperationState_OPERATION_STATE_FAILED,
		vmcv1.OperationState_OPERATION_STATE_SUPERSEDED,
		vmcv1.OperationState_OPERATION_STATE_DEADLINE_EXCEEDED:
		return true
	}
	return false
}

// --- rendering --------------------------------------------------------------

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
