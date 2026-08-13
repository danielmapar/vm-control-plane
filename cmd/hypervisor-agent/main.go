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
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/sigtunnel/vm-control-plane/internal/agent"
	"github.com/sigtunnel/vm-control-plane/internal/driver/compute"
	computefake "github.com/sigtunnel/vm-control-plane/internal/driver/compute/fake"
	"github.com/sigtunnel/vm-control-plane/internal/version"
	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

// options holds the parsed command-line flags.
type options struct {
	server        string
	hostID        string
	stateDir      string
	nodes         string
	nodeCPUs      int64
	nodeMemoryGiB uint64
	nodeDiskGiB   uint64
	driver        string
	debug         string
	storageRoot   string
	mgmtNetwork   string
	tenantBridge  string
	sshKeyFile    string
	libvirtSock   string
	pinCPUSet     string
	pollInterval  time.Duration
	emulated      bool
}

func main() {
	opts := parseFlags()

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	log.Info("hypervisor-agent starting", "version", version.String(), "host", opts.hostID, "driver", opts.driver)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	drv, err := buildDriver(ctx, opts)
	if err != nil {
		log.Error("compute driver", "err", err)
		os.Exit(2)
	}

	specs := nodeSpecs(opts)

	conn, err := grpc.NewClient(opts.server, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		log.Error("dial control plane", "err", err)
		os.Exit(1)
	}
	defer conn.Close() //nolint:errcheck // process exit follows

	d := agent.New(agent.Config{
		HostID:   opts.hostID,
		StateDir: opts.stateDir,
		Nodes:    specs,
		// Host allocatable: the quota sum exactly (no overcommit in v0.1).
		HostCPUs:     opts.nodeCPUs * int64(len(specs)),
		HostMemory:   opts.nodeMemoryGiB << 30 * uint64(len(specs)),
		HostDisk:     opts.nodeDiskGiB << 30 * uint64(len(specs)),
		Compute:      drv,
		Log:          log,
		DebugAddr:    opts.debug,
		PollInterval: opts.pollInterval,
	}, vmcv1.NewAgentServiceClient(conn))

	if err := d.Run(ctx); err != nil && ctx.Err() == nil {
		log.Error("agent exiting", "err", err)
		os.Exit(1)
	}
	fmt.Println("agent stopped")
}

// parseFlags declares, parses, and snapshots the command-line flags.
func parseFlags() options {
	var (
		server        = flag.String("server", "127.0.0.1:7070", "control-plane gRPC address")
		hostID        = flag.String("host-id", "host-local", "persistent physical-host identity")
		stateDir      = flag.String("state-dir", "", "host lock directory (default: OS temp)")
		nodes         = flag.String("nodes", "node-a,node-b", "comma-separated logical node names")
		nodeCPUs      = flag.Int64("node-cpus", 4, "cpu quota per logical node")
		nodeMemoryGiB = flag.Uint64("node-memory-gib", 8, "memory quota per logical node (GiB)")
		nodeDiskGiB   = flag.Uint64("node-disk-gib", 100, "disk quota per logical node (GiB)")
		driver        = flag.String("driver", "fake", "compute driver: fake | libvirt (libvirt is Linux-only)")
		debug         = flag.String("debug-addr", "", "loopback-only debug surface (drift injection; fake tier)")
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

	return options{
		server:        *server,
		hostID:        *hostID,
		stateDir:      *stateDir,
		nodes:         *nodes,
		nodeCPUs:      *nodeCPUs,
		nodeMemoryGiB: *nodeMemoryGiB,
		nodeDiskGiB:   *nodeDiskGiB,
		driver:        *driver,
		debug:         *debug,
		storageRoot:   *storageRoot,
		mgmtNetwork:   *mgmtNetwork,
		tenantBridge:  *tenantBridge,
		sshKeyFile:    *sshKeyFile,
		libvirtSock:   *libvirtSock,
		pinCPUSet:     *pinCPUSet,
		pollInterval:  *pollInterval,
		emulated:      *emulated,
	}
}

// buildDriver constructs the compute driver named by the flags. The caller
// decides how to react to an error.
func buildDriver(ctx context.Context, opts options) (compute.Driver, error) {
	switch opts.driver {
	case "fake":
		return computefake.New(), nil
	case "libvirt":
		return newLibvirtDriver(ctx, libvirtDriverOpts{
			Socket:       opts.libvirtSock,
			StorageRoot:  opts.storageRoot,
			MgmtNetwork:  opts.mgmtNetwork,
			TenantBridge: opts.tenantBridge,
			SSHKeyFile:   opts.sshKeyFile,
			CPUSet:       opts.pinCPUSet,
			Emulated:     opts.emulated,
		})
	default:
		return nil, fmt.Errorf("unknown driver %q", opts.driver)
	}
}

// nodeSpecs parses the comma-separated node names into logical node specs,
// each carrying the per-node quota from the flags.
func nodeSpecs(opts options) []agent.NodeSpec {
	names := strings.Split(opts.nodes, ",")
	specs := make([]agent.NodeSpec, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if n == "" {
			continue
		}
		specs = append(specs, agent.NodeSpec{
			Name: n, CPUs: opts.nodeCPUs,
			MemoryBytes: opts.nodeMemoryGiB << 30, DiskBytes: opts.nodeDiskGiB << 30,
		})
	}
	return specs
}
