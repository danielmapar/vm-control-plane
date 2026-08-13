// Command dev is the Tier-0 supervisor (`make dev`): embedded PostgreSQL +
// control-plane + one hypervisor-agent (two logical nodes, fake drivers),
// all as local processes with loopback listeners and stable binary paths
// (no Windows Firewall prompt churn — plan §8).
//
// It is a tiny Go process supervisor on purpose: make is a convenience
// wrapper here, never a dependency.
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
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

func run() error {
	driver := flag.String("driver", "fake", "compute driver for the agent: fake | libvirt")
	nodes := flag.String("nodes", "node-a,node-b", "logical node names")
	storageRoot := flag.String("storage-root", "/var/lib/vmc", "libvirt storage root")
	mgmtNetwork := flag.String("mgmt-network", "vmc-mgmt", "libvirt management network")
	tenantBridge := flag.String("tenant-bridge", "", "libvirt OVS tenant bridge")
	sshKeyFile := flag.String("ssh-key-file", "", "libvirt: SSH public key for guests")
	pinCPUSet := flag.String("pin-cpuset", "", "libvirt: pin guest vCPU+emulator to these host cores (nested-VirtualBox stability)")
	noAgent := flag.Bool("no-agent", false, "run only embedded Postgres + control-plane (no agent) — for the DISTRIBUTED demo where the real libvirt agent runs on a separate hypervisor host and dials in")
	flag.Parse()

	// Catch SIGTERM as well as SIGINT: a process manager (or the demo's
	// `kill $PID`) sends SIGTERM, and without this the supervisor is killed
	// abruptly — the deferred pg.Stop() never runs and the child processes
	// orphan. Handling it lets ctx cancellation fan out to the ctx-bound
	// children and the embedded-Postgres teardown.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Println("dev: building binaries into bin/ (stable paths)")
	build := exec.CommandContext(ctx, "go", "build", "-o", "bin/", "./cmd/...")
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		return fmt.Errorf("build: %w", err)
	}

	pgPort, err := freePort()
	if err != nil {
		return err
	}
	apiPort, err := freePort()
	if err != nil {
		return err
	}

	// Put PostgreSQL's data + runtime on tmpfs (RAM) when available. On the
	// nested-KVM substrate, embedded Postgres' disk writes contend with the
	// guest's boot reads on the same virtual disk, and that I/O contention
	// destabilizes VirtualBox's nested VT-x — enough to WEDGE a booting guest
	// (and heavy I/O guru-meditates the whole VM). RAM-backed storage removes
	// the contention entirely; the demo DB is tiny. Falls back to the default
	// temp dir off-Linux (Windows Tier-0 has no nested-virt concern).
	pgBase := ""
	if fi, serr := os.Stat("/dev/shm"); serr == nil && fi.IsDir() {
		pgBase = "/dev/shm"
	}
	dataDir, err := os.MkdirTemp(pgBase, "vmc-dev-pg-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dataDir) //nolint:errcheck // best-effort cleanup

	fmt.Printf("dev: starting embedded PostgreSQL 16 on 127.0.0.1:%d (first run downloads pinned binaries once)\n", pgPort)
	pg := embeddedpostgres.NewDatabase(embeddedpostgres.DefaultConfig().
		Version(embeddedpostgres.V16).
		Encoding("UTF8").Locale("C").
		Port(uint32(pgPort)).
		DataPath(filepath.Join(dataDir, "data")).
		RuntimePath(filepath.Join(dataDir, "runtime")).
		Logger(nil))
	if err := pg.Start(); err != nil {
		return fmt.Errorf("embedded postgres (hint: run from a NON-admin shell): %w", err)
	}
	defer func() {
		fmt.Println("dev: stopping postgres")
		_ = pg.Stop()
	}()

	dbURL := fmt.Sprintf("postgres://postgres:postgres@127.0.0.1:%d/postgres", pgPort)
	listen := fmt.Sprintf("127.0.0.1:%d", apiPort)

	bin := func(name string) string {
		p := filepath.Join("bin", name)
		if _, err := os.Stat(p + ".exe"); err == nil {
			return p + ".exe"
		}
		return p
	}

	cp := command(ctx, "control-plane", bin("control-plane"),
		"--listen", listen, "--db-url", dbURL)
	if err := cp.Start(); err != nil {
		return fmt.Errorf("start control-plane: %w", err)
	}
	time.Sleep(1500 * time.Millisecond) // migrations + listener

	// Distributed mode: control-plane + Postgres only. The real libvirt agent
	// runs on a separate hypervisor host (e.g. the nested-KVM VM) and dials
	// this control-plane over the network — which is the whole point of a
	// control-plane/agent split, and on nested VirtualBox it is also what
	// lets a guest boot: the DB/control-plane workload is no longer sharing
	// the fragile nested-virt VM with the guest.
	if *noAgent {
		fmt.Printf("\ndev: control-plane up (NO agent — distributed mode).\n  VMCTL_SERVER=%s\n  point a remote agent at this address, then: bin/vmctl create ...\nCtrl-C stops it.\n\n", listen)
		<-ctx.Done()
		fmt.Println("\ndev: shutting down")
		_ = cp.Wait()
		return nil
	}

	agentArgs := []string{
		"--server", listen, "--host-id", "host-local", "--nodes", *nodes,
		"--driver", *driver,
	}
	if *driver == "libvirt" {
		agentArgs = append(agentArgs,
			"--storage-root", *storageRoot,
			"--mgmt-network", *mgmtNetwork,
			"--tenant-bridge", *tenantBridge)
		if *sshKeyFile != "" {
			agentArgs = append(agentArgs, "--ssh-key-file", *sshKeyFile)
		}
		if *pinCPUSet != "" {
			agentArgs = append(agentArgs, "--pin-cpuset", *pinCPUSet)
		}
	} else {
		debugPort, derr := freePort()
		if derr != nil {
			return derr
		}
		agentArgs = append(agentArgs, "--debug-addr", fmt.Sprintf("127.0.0.1:%d", debugPort))
	}
	ag := command(ctx, "agent", bin("hypervisor-agent"), agentArgs...)
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

// command wires a child with prefixed output that dies with the supervisor.
func command(ctx context.Context, prefix, path string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, path, args...)
	stdout, _ := cmd.StdoutPipe()
	stderr, _ := cmd.StderrPipe()
	go prefixCopy(prefix, stdout)
	go prefixCopy(prefix+"!", stderr)
	return cmd
}

func prefixCopy(prefix string, r interface{ Read([]byte) (int, error) }) {
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
