// Command golibvirt-probe verifies, with the exact client library the agent
// will use, the two facts the connection supervisor is designed around
// (plan D8): go-libvirt mutation RPCs take no context and cannot be
// cancelled in place, and after any ambiguous outcome the only safe recovery
// is: poison the connection, reconnect, and re-observe before retrying.
//
// It runs three probes against qemu:///system:
//
//  1. lifecycle: define → start → destroy → undefine of a trivial domain,
//     asserting each step is idempotently re-checkable (define-if-absent).
//  2. deadline: a call issued over a deliberately blackholed connection must
//     be abandoned by closing the transport (there is no other way), and the
//     client must be replaced.
//  3. re-observe: after an abandoned DomainDefineXML, a fresh connection
//     lists domains to decide whether the mutation landed.
//
// Exit code 0 = all probes behaved as the supervisor design expects.
package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"time"

	"github.com/digitalocean/go-libvirt"
	"github.com/digitalocean/go-libvirt/socket/dialers"
)

const testDomainXML = `<domain type='kvm'>
  <name>vmc-probe</name>
  <memory unit='MiB'>128</memory>
  <vcpu>1</vcpu>
  <os><type arch='x86_64'>hvm</type><boot dev='hd'/></os>
  <devices><console type='pty'/></devices>
</domain>`

const socketPath = "/var/run/libvirt/libvirt-sock"

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

	// Probe 1: lifecycle with idempotent re-checks.
	l, err := connect()
	if err != nil {
		return err
	}
	defer l.Disconnect() //nolint:errcheck // best-effort teardown

	// Clean slate: undefine any leftover from a previous run.
	if d, err := l.DomainLookupByName("vmc-probe"); err == nil {
		_ = l.DomainDestroy(d) // may not be running; best effort
		if err := l.DomainUndefine(d); err != nil {
			return fmt.Errorf("cleanup undefine: %w", err)
		}
	}

	dom, err := l.DomainDefineXML(testDomainXML)
	if err != nil {
		return fmt.Errorf("define: %w", err)
	}
	// Idempotency check: looking the domain up again must find the same UUID.
	again, err := l.DomainLookupByName("vmc-probe")
	if err != nil || again.UUID != dom.UUID {
		return fmt.Errorf("define-if-absent re-check failed: %v", err)
	}
	if err := l.DomainCreate(dom); err != nil {
		return fmt.Errorf("start: %w", err)
	}
	if err := l.DomainDestroy(dom); err != nil {
		return fmt.Errorf("destroy: %w", err)
	}
	if err := l.DomainUndefine(dom); err != nil {
		return fmt.Errorf("undefine: %w", err)
	}

	// Probe 2: deadline-by-transport-close. Dial the socket, wrap it so all
	// reads hang (a blackhole), and confirm the only way to unblock a call
	// is closing the connection out from under the client.
	raw, err := net.Dial("unix", socketPath)
	if err != nil {
		return fmt.Errorf("dial for blackhole probe: %w", err)
	}
	bh := &blackhole{Conn: raw}
	bl := libvirt.NewWithDialer(connDialer{bh})
	if err := bl.Connect(); err == nil {
		// Connect handshake got through before we engaged the blackhole; engage now.
		bh.engage()
		done := make(chan error, 1)
		go func() { _, err := bl.ConnectGetLibVersion(); done <- err }()
		select {
		case <-done:
			return errors.New("blackholed call returned without transport close — unexpected")
		case <-time.After(500 * time.Millisecond):
			// Blocked, as designed around. Poison the transport.
			_ = raw.Close()
		}
		select {
		case err := <-done:
			if err == nil {
				return errors.New("call over closed transport succeeded — unexpected")
			}
		case <-time.After(5 * time.Second):
			return errors.New("call did not fail after transport close — supervisor design invalid")
		}
	} else {
		_ = raw.Close()
	}

	// Probe 3: fresh connection re-observes cleanly after the poisoned one.
	l2, err := connect()
	if err != nil {
		return fmt.Errorf("reconnect after poison: %w", err)
	}
	defer l2.Disconnect() //nolint:errcheck // best-effort teardown
	if _, _, err := l2.ConnectListAllDomains(1, 0); err != nil {
		return fmt.Errorf("re-observe after poison: %w", err)
	}
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

// connDialer hands a pre-established (possibly wrapped) connection to
// go-libvirt — used to interpose the blackhole between client and daemon.
type connDialer struct{ c net.Conn }

func (d connDialer) Dial() (net.Conn, error) { return d.c, nil }

// blackhole wraps a net.Conn; once engaged, reads hang until the underlying
// connection is closed — simulating a wedged libvirtd or a half-open link.
type blackhole struct {
	net.Conn
	engaged chan struct{}
}

func (b *blackhole) engage() {
	if b.engaged == nil {
		b.engaged = make(chan struct{})
	}
	close(b.engaged)
}

func (b *blackhole) Read(p []byte) (int, error) {
	if b.engaged != nil {
		select {
		case <-b.engaged:
			// Hang until Close unblocks the underlying read.
			buf := make([]byte, 1)
			_, err := b.Conn.Read(buf)
			_ = buf
			if err != nil {
				return 0, err
			}
			// Swallow real data while engaged: still a blackhole.
			return 0, os.ErrDeadlineExceeded
		default:
		}
	}
	return b.Conn.Read(p)
}
