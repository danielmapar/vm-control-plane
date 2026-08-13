// Command hypervisor-agent runs one process per physical host, advertising
// one or more logical scheduling nodes. Tier 0 uses the fake
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
		driver   = flag.String("driver", "fake", "compute driver: fake | libvirt (libvirt is Linux-only)")
		debug    = flag.String("debug-addr", "", "loopback-only debug surface (drift injection; fake tier)")
		// Real-driver (libvirt) options — ignored by the fake driver.
		storageRoot  = flag.String("storage-root", "/var/lib/vmc", "libvirt: qcow2/seed storage root")
		mgmtNetwork  = flag.String("mgmt-network", "vmc-mgmt", "libvirt: management NAT network name")
		tenantBridge = flag.String("tenant-bridge", "", "libvirt: OVS integration bridge (empty = mgmt-only)")
		sshKeyFile   = flag.String("ssh-key-file", "", "libvirt: SSH public key injected into guests")
		libvirtSock  = flag.String("libvirt-socket", "", "libvirt: unix socket (default system socket)")
		pinCPUSet    = flag.String("pin-cpuset", "", "libvirt: pin guest vCPU+emulator to these host cores (e.g. 8-15); needed on nested VirtualBox")
		pollInterval = flag.Duration("poll-interval", 0, "work-claim poll interval (0 = default 300ms); raise it (e.g. 3s) on nested VirtualBox so the daemon's steady-state churn does not disturb a booting guest")
		emulated     = flag.Bool("emulated", false, "libvirt: run guests under QEMU TCG (software) instead of hardware KVM — no /dev/kvm or nested VT-x needed; slower but stable where nested virtualization is not")
	)
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	log.Info("hypervisor-agent starting", "version", version.String(), "host", *hostID, "driver", *driver)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	var drv compute.Driver
	switch *driver {
	case "fake":
		drv = computefake.New()
	case "libvirt":
		var err error
		drv, err = newLibvirtDriver(ctx, libvirtDriverOpts{
			Socket:       *libvirtSock,
			StorageRoot:  *storageRoot,
			MgmtNetwork:  *mgmtNetwork,
			TenantBridge: *tenantBridge,
			SSHKeyFile:   *sshKeyFile,
			CPUSet:       *pinCPUSet,
			Emulated:     *emulated,
		})
		if err != nil {
			log.Error("libvirt driver", "err", err)
			os.Exit(2)
		}
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

	d := agent.New(agent.Config{
		HostID:   *hostID,
		StateDir: *stateDir,
		Nodes:    specs,
		// Host allocatable: the quota sum exactly (no overcommit in v0.1).
		HostCPUs:     *nodeCPUs * int64(len(specs)),
		HostMemory:   *nodeMem << 30 * uint64(len(specs)),
		HostDisk:     *nodeDisk << 30 * uint64(len(specs)),
		Compute:      drv,
		Log:          log,
		DebugAddr:    *debug,
		PollInterval: *pollInterval,
	}, vmcv1.NewAgentServiceClient(conn))

	if err := d.Run(ctx); err != nil && ctx.Err() == nil {
		log.Error("agent exiting", "err", err)
		os.Exit(1)
	}
	fmt.Println("agent stopped")
}
