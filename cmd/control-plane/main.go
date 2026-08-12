// Command control-plane hosts the gRPC API, the reconciler, and the
// scheduler behind role flags (see ADR-0001). Wiring lands with the API
// server PR; this placeholder keeps `go build ./...` meaningful from the
// first scaffolding commit.
package main

import (
	"fmt"

	"github.com/sigtunnel/vm-control-plane/internal/version"
)

func main() {
	fmt.Println("control-plane", version.String())
}
