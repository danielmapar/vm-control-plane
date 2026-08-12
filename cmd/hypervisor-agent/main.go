// Command hypervisor-agent runs one process per physical host, advertising
// one or more logical scheduling nodes (see ADR-0004). Wiring lands with the
// host-daemon PR.
package main

import (
	"fmt"

	"github.com/sigtunnel/vm-control-plane/internal/version"
)

func main() {
	fmt.Println("hypervisor-agent", version.String())
}
