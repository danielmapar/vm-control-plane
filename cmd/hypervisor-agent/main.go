// Command hypervisor-agent runs ONE process per physical host, advertising
// one or more logical scheduling nodes (ADR-0004). Tier 0 uses the fake
// compute driver; real drivers are Linux-only and selected explicitly.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/sigtunnel/vm-control-plane/internal/agent"
	"github.com/sigtunnel/vm-control-plane/internal/driver/compute"
	computefake "github.com/sigtunnel/vm-control-plane/internal/driver/compute/fake"
	"github.com/sigtunnel/vm-control-plane/internal/version"
	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

func main() {
	var (
		server   = flag.String("server", "127.0.0.1:7070", "control-plane gRPC address")
		hostID   = flag.String("host-id", "host-local", "persistent physical-host identity")
		stateDir = flag.String("state-dir", "", "host lock directory (default: OS temp)")
		nodes    = flag.String("nodes", "node-a,node-b", "comma-separated logical node names")
		nodeCPUs = flag.Int64("node-cpus", 4, "cpu quota per logical node")
		nodeMem  = flag.Uint64("node-memory-gib", 8, "memory quota per logical node (GiB)")
		nodeDisk = flag.Uint64("node-disk-gib", 100, "disk quota per logical node (GiB)")
		driver   = flag.String("driver", "fake", "compute driver (fake; libvirt lands with the real-substrate PRs)")
		debug    = flag.String("debug-addr", "", "loopback-only debug surface (drift injection; fake tier)")
	)
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	log.Info("hypervisor-agent starting", "version", version.String(), "host", *hostID)

	var drv compute.Driver
	switch *driver {
	case "fake":
		drv = computefake.New()
	default:
		log.Error("unknown driver", "driver", *driver)
		os.Exit(2)
	}

	names := strings.Split(*nodes, ",")
	specs := make([]agent.NodeSpec, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		specs = append(specs, agent.NodeSpec{
			Name: n, CPUs: *nodeCPUs,
			MemoryBytes: *nodeMem << 30, DiskBytes: *nodeDisk << 30,
		})
	}

	conn, err := grpc.NewClient(*server, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Error("dial control plane", "err", err)
		os.Exit(1)
	}
	defer conn.Close() //nolint:errcheck // process exit follows

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	d := agent.New(agent.Config{
		HostID:   *hostID,
		StateDir: *stateDir,
		Nodes:    specs,
		// Host allocatable: the quota sum exactly (no overcommit in v0.1).
		HostCPUs:   *nodeCPUs * int64(len(specs)),
		HostMemory: *nodeMem << 30 * uint64(len(specs)),
		HostDisk:   *nodeDisk << 30 * uint64(len(specs)),
		Compute:    drv,
		Log:        log,
		DebugAddr:  *debug,
	}, vmcv1.NewAgentServiceClient(conn))

	if err := d.Run(ctx); err != nil && ctx.Err() == nil {
		log.Error("agent exiting", "err", err)
		os.Exit(1)
	}
	fmt.Println("agent stopped")
}
