// Command golibvirt-probe establishes, with the exact client library the
// agent will use, the fact the connection supervisor is designed
// around: a go-libvirt mutation can land on the server while the client
// never sees the response — and the only safe recovery is to poison the
// transport, reconnect, and re-observe before retrying.
//
// Probe sequence:
//
//  1. lifecycle: define → lookup (idempotent re-check) → start → destroy →
//     undefine of a run-scoped trivial domain.
//  2. gated mutation: through a response-gating proxy, issue
//     DomainDefineXML and withhold the server's response. An independent
//     observer connection confirms the domain landed while the caller is
//     still blocked — the ambiguous-outcome window made visible.
//  3. poison + re-observe: close the proxied transport; assert the blocked
//     call fails promptly; a fresh connection re-observes the domain and
//     cleans it up.
//
// Run-scoped names (vmc-probe-<pid>-<nonce>) make reruns safe: the probe
// never touches resources it did not create this run.
package main

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/digitalocean/go-libvirt"
	"github.com/digitalocean/go-libvirt/socket/dialers"
)

const socketPath = "/var/run/libvirt/libvirt-sock"

func domainXML(name string) string {
	return fmt.Sprintf(`<domain type='kvm'>
  <name>%s</name>
  <memory unit='MiB'>128</memory>
  <vcpu>1</vcpu>
  <os><type arch='x86_64'>hvm</type><boot dev='hd'/></os>
  <devices><console type='pty'/></devices>
</domain>`, name)
}

func main() {
	if err := run(); err != nil {
		log.Fatalf("probe: %v", err)
	}
	fmt.Println("golibvirt-probe: OK")
}

func run() error {
	if _, err := os.Stat(socketPath); err != nil {
		return fmt.Errorf("libvirt socket not present (run inside the substrate host): %w", err)
	}
	nonce := make([]byte, 4)
	if _, err := rand.Read(nonce); err != nil {
		return err
	}
	runID := fmt.Sprintf("vmc-probe-%d-%s", os.Getpid(), hex.EncodeToString(nonce))

	if err := lifecycleProbe(runID + "-a"); err != nil {
		return fmt.Errorf("lifecycle: %w", err)
	}
	if err := gatedMutationProbe(runID + "-b"); err != nil {
		return fmt.Errorf("gated mutation: %w", err)
	}
	return nil
}

func lifecycleProbe(name string) error {
	l, err := connect()
	if err != nil {
		return err
	}
	defer l.Disconnect() //nolint:errcheck // best-effort teardown

	dom, err := l.DomainDefineXML(domainXML(name))
	if err != nil {
		return fmt.Errorf("define: %w", err)
	}
	// Idempotent re-check: define-if-absent must find the same UUID.
	again, err := l.DomainLookupByName(name)
	if err != nil || again.UUID != dom.UUID {
		return fmt.Errorf("define-if-absent re-check: %v", err)
	}
	if err := l.DomainCreate(dom); err != nil {
		_ = l.DomainUndefine(dom)
		return fmt.Errorf("start: %w", err)
	}
	if err := l.DomainDestroy(dom); err != nil {
		return fmt.Errorf("destroy: %w", err)
	}
	if err := l.DomainUndefine(dom); err != nil {
		return fmt.Errorf("undefine: %w", err)
	}
	return nil
}

// gatedMutationProbe proves the ambiguous-outcome window exists and that
// poison + re-observe recovers from it.
func gatedMutationProbe(name string) error {
	gate := newGate()
	proxied, err := gate.dialThrough(socketPath)
	if err != nil {
		return err
	}
	client := libvirt.NewWithDialer(connDialer{proxied})
	if err := client.Connect(); err != nil {
		gate.closeAll()
		return fmt.Errorf("connect through proxy: %w", err)
	}

	// Withhold server→client bytes from this point on: the define request
	// still reaches libvirtd; its response never reaches the client.
	gate.engage()

	defineErr := make(chan error, 1)
	go func() {
		_, err := client.DomainDefineXML(domainXML(name))
		defineErr <- err
	}()

	// Independent observer: the mutation must land while the caller blocks.
	observer, err := connect()
	if err != nil {
		gate.closeAll()
		return err
	}
	landed := false
	for i := 0; i < 40; i++ { // up to 4s
		if _, err := observer.DomainLookupByName(name); err == nil {
			landed = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !landed {
		gate.closeAll()
		return errors.New("mutation did not land while response was withheld — proxy setup invalid")
	}
	select {
	case err := <-defineErr:
		gate.closeAll()
		return fmt.Errorf("caller returned (%v) while its response was withheld — gate leaked", err)
	default:
		// Blocked, as designed around: the caller cannot know its define
		// succeeded. This is exactly why retries must re-observe.
	}

	// Poison the transport (the supervisor's only recourse) and require the
	// blocked call to fail promptly.
	gate.closeAll()
	select {
	case err := <-defineErr:
		if err == nil {
			return errors.New("blocked call reported success after poison — unexpected")
		}
	case <-time.After(5 * time.Second):
		return errors.New("blocked call did not fail after transport close — supervisor design invalid")
	}

	// Re-observe on the untouched observer connection and clean up our
	// run-scoped domain (never anything else).
	dom, err := observer.DomainLookupByName(name)
	if err != nil {
		return fmt.Errorf("re-observe after poison: %w", err)
	}
	if err := observer.DomainUndefine(dom); err != nil {
		return fmt.Errorf("cleanup undefine: %w", err)
	}
	_ = observer.Disconnect()
	return nil
}

func connect() (*libvirt.Libvirt, error) {
	l := libvirt.NewWithDialer(dialers.NewLocal(
		dialers.WithSocket(socketPath),
		dialers.WithLocalTimeout(2*time.Second),
	))
	if err := l.Connect(); err != nil {
		return nil, fmt.Errorf("libvirt connect: %w", err)
	}
	return l, nil
}

// connDialer hands a pre-established connection to go-libvirt.
type connDialer struct{ c net.Conn }

func (d connDialer) Dial() (net.Conn, error) { return d.c, nil }

// gate is a response-gating proxy: client→server bytes always flow;
// server→client bytes are silently discarded once engaged. Engagement is
// mutex-guarded — no racy state shared with Read paths.
type gate struct {
	mu      sync.Mutex
	engaged bool
	conns   []net.Conn
}

func newGate() *gate { return &gate{} }

func (g *gate) engage() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.engaged = true
}

func (g *gate) isEngaged() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.engaged
}

func (g *gate) closeAll() {
	g.mu.Lock()
	defer g.mu.Unlock()
	for _, c := range g.conns {
		_ = c.Close()
	}
	g.conns = nil
}

// dialThrough connects to the real socket and returns the client half of an
// in-process pipe whose server half is pumped by the proxy goroutines.
func (g *gate) dialThrough(path string) (net.Conn, error) {
	server, err := net.DialTimeout("unix", path, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("dial libvirt for proxy: %w", err)
	}
	clientSide, proxySide := net.Pipe()
	g.mu.Lock()
	g.conns = append(g.conns, server, clientSide, proxySide)
	g.mu.Unlock()

	// client → server: always forwarded.
	go func() {
		_, _ = io.Copy(server, proxySide)
		_ = server.Close()
	}()
	// server → client: forwarded until engaged, then discarded.
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := server.Read(buf)
			if n > 0 && !g.isEngaged() {
				if _, werr := proxySide.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				_ = proxySide.Close()
				return
			}
		}
	}()
	return clientSide, nil
}
