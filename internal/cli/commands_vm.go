package cli

import (
	"context"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/spf13/cobra"
	"google.golang.org/grpc"

	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

// parseSize accepts "512MiB", "2GiB", "10GiB", or raw bytes.
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
	if mult > 1 && n > (^uint64(0))/mult {
		return 0, fmt.Errorf("size %q overflows uint64", s)
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
			// The key exists before the first attempt and is printed first, so
			// after an ambiguous failure the user can replay with
			// --idempotency-key and get the original operation back.
			if idemKey == "" {
				idemKey = uuid.NewString()
			}
			fmt.Fprintf(out, "idempotency-key: %s  (replay with --idempotency-key on ambiguous failures)\n", idemKey)
			return withConn(cmd, *server, 30*time.Second, func(ctx context.Context, conn *grpc.ClientConn) error {
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
			})
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
			return withConn(cmd, *server, 10*time.Second, func(ctx context.Context, conn *grpc.ClientConn) error {
				vm, err := vmcv1.NewVMServiceClient(conn).GetVm(ctx, &vmcv1.GetVmRequest{Name: args[1]})
				if err != nil {
					return err
				}
				printVMs(out, vm)
				return nil
			})
		},
	}
}

func newListCmd(out io.Writer, server *string) *cobra.Command {
	return &cobra.Command{
		Use:   "list vms",
		Short: "List VMs",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConn(cmd, *server, 10*time.Second, func(ctx context.Context, conn *grpc.ClientConn) error {
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
			})
		},
	}
}

func newPowerCmd(out io.Writer, server *string, verb string, power vmcv1.PowerState) *cobra.Command {
	return &cobra.Command{
		Use:   verb + " vm NAME",
		Short: strings.ToUpper(verb[:1]) + verb[1:] + " a VM (async; the loop converges it)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != "vm" {
				return fmt.Errorf("only 'vm' resources are supported, got %q", args[0])
			}
			return withConn(cmd, *server, 30*time.Second, func(ctx context.Context, conn *grpc.ClientConn) error {
				op, err := vmcv1.NewVMServiceClient(conn).UpdateVmPower(ctx, &vmcv1.UpdateVmPowerRequest{
					IdempotencyKey: uuid.NewString(), Name: args[1], Power: power,
				})
				if err != nil {
					return err
				}
				printOperation(out, op)
				return nil
			})
		},
	}
}

func newDeleteCmd(out io.Writer, server *string) *cobra.Command {
	return &cobra.Command{
		Use:   "delete vm NAME",
		Short: "Delete a VM (tombstone; teardown is reconciled)",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConn(cmd, *server, 30*time.Second, func(ctx context.Context, conn *grpc.ClientConn) error {
				op, err := vmcv1.NewVMServiceClient(conn).DeleteVm(ctx, &vmcv1.DeleteVmRequest{
					IdempotencyKey: uuid.NewString(),
					Name:           args[1],
				})
				if err != nil {
					return err
				}
				printOperation(out, op)
				return nil
			})
		},
	}
}
