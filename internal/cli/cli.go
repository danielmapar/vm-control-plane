// Package cli implements vmctl: the operator CLI, talking to the same public
// gRPC API as any other client (no privileged side channel).
package cli

import (
	"io"
	"os"

	"github.com/spf13/cobra"

	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

// New builds the vmctl command tree. Output goes to out so tests can capture
// it.
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
	root.AddCommand(newPowerCmd(out, &server, "start", vmcv1.PowerState_POWER_STATE_RUNNING))
	root.AddCommand(newPowerCmd(out, &server, "stop", vmcv1.PowerState_POWER_STATE_STOPPED))
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
