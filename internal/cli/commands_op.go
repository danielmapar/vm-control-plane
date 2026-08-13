package cli

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/durationpb"

	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

func newOpCmd(out io.Writer, server *string) *cobra.Command {
	op := &cobra.Command{Use: "op", Short: "Operations"}
	op.AddCommand(newOpWaitCmd(out, server), newOpGetCmd(out, server))
	return op
}

func newOpWaitCmd(out io.Writer, server *string) *cobra.Command {
	var waitTimeout time.Duration
	cmd := &cobra.Command{
		Use:   "wait ID",
		Short: "Wait for an operation to terminalize",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			// A defensive client deadline on top of the server's cap, so op wait
			// can never hang, even against a wedged server.
			return withConn(cmd, *server, waitTimeout+15*time.Second, func(ctx context.Context, conn *grpc.ClientConn) error {
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
			})
		},
	}
	cmd.Flags().DurationVar(&waitTimeout, "timeout", 2*time.Minute, "how long to wait before giving up")
	return cmd
}

func newOpGetCmd(out io.Writer, server *string) *cobra.Command {
	return &cobra.Command{
		Use:   "get ID",
		Short: "Show an operation",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return withConn(cmd, *server, 10*time.Second, func(ctx context.Context, conn *grpc.ClientConn) error {
				op, err := vmcv1.NewOperationServiceClient(conn).GetOperation(ctx, &vmcv1.GetOperationRequest{Id: args[0]})
				if err != nil {
					return err
				}
				printOperation(out, op)
				return nil
			})
		},
	}
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
