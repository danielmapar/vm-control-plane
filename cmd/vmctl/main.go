// Command vmctl is the operator CLI. It speaks the same public gRPC API as
// any other client — no privileged side channel.
package main

import (
	"fmt"

	"github.com/sigtunnel/vm-control-plane/internal/version"
)

func main() {
	fmt.Println("vmctl", version.String())
}
