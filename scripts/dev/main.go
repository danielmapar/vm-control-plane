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
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
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
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
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

	dataDir, err := os.MkdirTemp("", "vmc-dev-pg-*")
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

	debugPort, err := freePort()
	if err != nil {
		return err
	}
	debugAddr := fmt.Sprintf("127.0.0.1:%d", debugPort)
	ag := command(ctx, "agent", bin("hypervisor-agent"),
		"--server", listen, "--host-id", "host-local", "--nodes", "node-a,node-b",
		"--debug-addr", debugAddr)
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
