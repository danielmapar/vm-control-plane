// Command vmctl is the operator CLI. It speaks the same public gRPC API as
// any other client — no privileged side channel.
package main

import (
	"fmt"
	"os"

	"github.com/sigtunnel/vm-control-plane/internal/cli"
)

func main() {
	if err := cli.New(os.Stdout).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "vmctl:", err)
		os.Exit(1)
	}
}
