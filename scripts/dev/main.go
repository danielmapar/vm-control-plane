// Command dev is the Tier-0 supervisor (`make dev`): embedded PostgreSQL, a
// control-plane, and one hypervisor-agent (two logical nodes, fake drivers),
// all as local processes with loopback listeners and stable binary paths.
//
// It is a tiny Go process supervisor on purpose: make is a convenience wrapper
// here, never a dependency.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "dev:", err)
		os.Exit(1)
	}
}

// options are the dev supervisor's flags.
type options struct {
	driver       string
	nodes        string
	storageRoot  string
	mgmtNetwork  string
	tenantBridge string
	sshKeyFile   string
	pinCPUSet    string
	noAgent      bool
	emulated     bool
}

func parseFlags() options {
	var o options
	flag.StringVar(&o.driver, "driver", "fake", "compute driver for the agent: fake | libvirt")
	flag.StringVar(&o.nodes, "nodes", "node-a,node-b", "logical node names")
	flag.StringVar(&o.storageRoot, "storage-root", "/var/lib/vmc", "libvirt storage root")
	flag.StringVar(&o.mgmtNetwork, "mgmt-network", "vmc-mgmt", "libvirt management network")
	flag.StringVar(&o.tenantBridge, "tenant-bridge", "", "libvirt OVS tenant bridge")
	flag.StringVar(&o.sshKeyFile, "ssh-key-file", "", "libvirt: SSH public key for guests")
	flag.StringVar(&o.pinCPUSet, "pin-cpuset", "", "libvirt: pin guest vCPU+emulator to these host cores (nested-VirtualBox stability)")
	flag.BoolVar(&o.noAgent, "no-agent", false, "run only embedded Postgres + control-plane (no agent) — for the distributed demo where the real libvirt agent runs on a separate hypervisor host and dials in")
	flag.BoolVar(&o.emulated, "emulated", false, "libvirt: run guests under QEMU TCG (software) instead of hardware KVM — stable where nested virtualization is not")
	flag.Parse()
	return o
}

func run() error {
	opts := parseFlags()

	// Catch SIGTERM as well as SIGINT: a process manager (or the demo's
	// `kill $PID`) sends SIGTERM, and without this the supervisor would be
	// killed abruptly — the deferred cleanup never runs and the children
	// orphan. Handling it lets ctx cancellation fan out to the children and the
	// embedded-Postgres teardown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := buildBinaries(ctx); err != nil {
		return err
	}

	apiPort, err := freePort()
	if err != nil {
		return err
	}
	listen := fmt.Sprintf("127.0.0.1:%d", apiPort)

	dbURL, stopPostgres, err := startPostgres()
	if err != nil {
		return err
	}
	defer stopPostgres()

	cp, err := startControlPlane(ctx, dbURL, listen)
	if err != nil {
		return err
	}

	// Distributed mode: control-plane + Postgres only. The real libvirt agent
	// runs on a separate hypervisor host (for example the nested-KVM VM) and
	// dials this control-plane over the network — the whole point of a
	// control-plane/agent split, and on nested VirtualBox it is also what lets a
	// guest boot: the DB and control-plane workload no longer share the fragile
	// nested-virt VM with the guest.
	if opts.noAgent {
		fmt.Printf("\ndev: control-plane up (no agent — distributed mode).\n  VMCTL_SERVER=%s\n  point a remote agent at this address, then: bin/vmctl create ...\nCtrl-C stops it.\n\n", listen)
		<-ctx.Done()
		fmt.Println("\ndev: shutting down")
		_ = cp.Wait()
		return nil
	}

	args, err := agentArgs(opts, listen)
	if err != nil {
		return err
	}
	ag := command(ctx, "agent", bin("hypervisor-agent"), args...)
	if err := ag.Start(); err != nil {
		return fmt.Errorf("start agent: %w", err)
	}

	fmt.Printf("\ndev: Tier 0 is up.\n  export VMCTL_SERVER=%s   (PowerShell: $env:VMCTL_SERVER='%s')\n  bin/vmctl create vm demo-1 --cpu 1 --memory 512MiB\n  bin/vmctl op wait <operation-id>\n  bin/vmctl list vms\nCtrl-C stops everything.\n\n", listen, listen)

	<-ctx.Done()
	fmt.Println("\ndev: shutting down")
	_ = cp.Wait()
	_ = ag.Wait()
	return nil
}

// buildBinaries compiles cmd/... into bin/ with stable paths.
func buildBinaries(ctx context.Context) error {
	fmt.Println("dev: building binaries into bin/ (stable paths)")
	build := exec.CommandContext(ctx, "go", "build", "-o", "bin/", "./cmd/...")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("build: %w", err)
	}
	return nil
}

// startPostgres launches embedded PostgreSQL and returns its URL and a stop
// function. Its data and runtime live on tmpfs (/dev/shm) when available, which
// keeps Postgres I/O off a shared disk and out of contention with anything else
// on it; it falls back to the temp dir off Linux. The demo database is tiny.
func startPostgres() (dbURL string, stop func(), err error) {
	pgPort, err := freePort()
	if err != nil {
		return "", nil, err
	}
	base := ""
	if fi, serr := os.Stat("/dev/shm"); serr == nil && fi.IsDir() {
		base = "/dev/shm"
	}
	dataDir, err := os.MkdirTemp(base, "vmc-dev-pg-*")
	if err != nil {
		return "", nil, err
	}
	fmt.Printf("dev: starting embedded PostgreSQL 16 on 127.0.0.1:%d (first run downloads pinned binaries once)\n", pgPort)
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V16).
		Encoding("UTF8").Locale("C").
		Port(uint32(pgPort)).
		DataPath(filepath.Join(dataDir, "data")).
		RuntimePath(filepath.Join(dataDir, "runtime")).
		Logger(nil))
	if err := pg.Start(); err != nil {
		_ = os.RemoveAll(dataDir)
		return "", nil, fmt.Errorf("embedded postgres (hint: run from a non-admin shell): %w", err)
	}
	stop = func() {
		fmt.Println("dev: stopping postgres")
		_ = pg.Stop()
		_ = os.RemoveAll(dataDir)
	}
	return fmt.Sprintf("postgres://postgres:postgres@127.0.0.1:%d/postgres", pgPort), stop, nil
}

// startControlPlane starts the control-plane and waits briefly for migrations
// and the listener to come up.
func startControlPlane(ctx context.Context, dbURL, listen string) (*exec.Cmd, error) {
	cp := command(ctx, "control-plane", bin("control-plane"), "--listen", listen, "--db-url", dbURL)
	if err := cp.Start(); err != nil {
		return nil, fmt.Errorf("start control-plane: %w", err)
	}
	time.Sleep(1500 * time.Millisecond) // migrations + listener
	return cp, nil
}

// agentArgs assembles the hypervisor-agent's flags for the chosen driver.
func agentArgs(opts options, listen string) ([]string, error) {
	args := []string{
		"--server", listen, "--host-id", "host-local", "--nodes", opts.nodes,
		"--driver", opts.driver,
	}
	if opts.driver != "libvirt" {
		debugPort, err := freePort()
		if err != nil {
			return nil, err
		}
		return append(args, "--debug-addr", fmt.Sprintf("127.0.0.1:%d", debugPort)), nil
	}
	args = append(args,
		"--storage-root", opts.storageRoot,
		"--mgmt-network", opts.mgmtNetwork,
		"--tenant-bridge", opts.tenantBridge)
	if opts.sshKeyFile != "" {
		args = append(args, "--ssh-key-file", opts.sshKeyFile)
	}
	if opts.pinCPUSet != "" {
		args = append(args, "--pin-cpuset", opts.pinCPUSet)
	}
	if opts.emulated {
		args = append(args, "--emulated")
	}
	return args, nil
}

// bin resolves a built binary path, tolerating the .exe suffix on Windows.
func bin(name string) string {
	p := filepath.Join("bin", name)
	if _, err := os.Stat(p + ".exe"); err == nil {
		return p + ".exe"
	}
	return p
}

// command wires a child with prefixed output that dies with the supervisor.
func command(ctx context.Context, prefix, path string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, path, args...)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	go prefixCopy(prefix, stdout)
	go prefixCopy(prefix+"!", stderr)
	return cmd
}

func prefixCopy(prefix string, r io.Reader) {
	if r == nil {
		return
	}
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		fmt.Printf("[%s] %s\n", prefix, sc.Text())
	}
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close() //nolint:errcheck // probe listener
	return l.Addr().(*net.TCPAddr).Port, nil
}
