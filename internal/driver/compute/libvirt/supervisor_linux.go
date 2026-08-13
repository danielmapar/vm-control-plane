//go:build linux

// Package libvirt is the real compute driver. The connection supervisor
// exists because go-libvirt's RPCs take no context: a hung call
// can only be abandoned by closing the transport, after which the client
// must reconnect and re-observe before retrying an ambiguous mutation. The
// M0 probe (scripts/spike/golibvirt-probe) demonstrates the exact failure
// this is designed around.
package libvirt

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	golibvirt "github.com/digitalocean/go-libvirt"
)

// supervisor owns a single libvirt connection to a unix socket, guarded by
// a mutex. It enforces per-call deadlines by poisoning the transport, and
// distinguishes a well-formed libvirt server error (transport healthy —
// keep the connection) from a transport failure or timeout (reconnect).
type supervisor struct {
	socket string
	mu     sync.Mutex
	conn   net.Conn
	l      *golibvirt.Libvirt
}

func newSupervisor(socket string) *supervisor {
	if socket == "" {
		socket = "/var/run/libvirt/libvirt-sock"
	}
	return &supervisor{socket: socket}
}

// connDialer hands a pre-established connection to go-libvirt so the
// supervisor keeps a reference it can force-close.
type connDialer struct{ c net.Conn }

func (d connDialer) Dial() (net.Conn, error) { return d.c, nil }

func (s *supervisor) get() (*golibvirt.Libvirt, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.l != nil {
		return s.l, nil
	}
	c, err := net.DialTimeout("unix", s.socket, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("libvirt dial: %w", err)
	}
	l := golibvirt.NewWithDialer(connDialer{c})
	if err := l.Connect(); err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("libvirt connect: %w", err)
	}
	s.conn = c
	s.l = l
	return l, nil
}

// poison force-closes the transport and discards the client — the next
// call reconnects.
func (s *supervisor) poison() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.conn != nil {
		_ = s.conn.Close()
	}
	s.conn = nil
	s.l = nil
}

// call runs fn against the connection with a deadline. On timeout it
// poisons the transport (unblocking the hung RPC) and returns ctx.Err(); on
// a transport-level error it also poisons; a well-formed libvirt Error
// leaves the connection intact (a logical failure like not-found is normal).
func (s *supervisor) call(ctx context.Context, fn func(*golibvirt.Libvirt) error) error {
	l, err := s.get()
	if err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- fn(l) }()
	select {
	case err := <-done:
		if err != nil && !isServerError(err) {
			s.poison()
		}
		return err
	case <-ctx.Done():
		s.poison()
		<-done // let the goroutine unwind on the closed transport
		return fmt.Errorf("libvirt call timed out (transport poisoned, will reconnect): %w", ctx.Err())
	}
}

// isServerError reports whether err is a well-formed libvirt server error
// (transport healthy) rather than a transport/timeout failure.
func isServerError(err error) bool {
	var le golibvirt.Error
	return errors.As(err, &le)
}
