package agent

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"
)

// serveDebug exposes the fake-tier debug surface: POST /drift?vm=NAME stops
// the named VM's domain out of band (the virsh-destroy analog for demos and
// tests). Loopback only — a non-loopback bind is refused outright.
func (d *Daemon) serveDebug(ctx context.Context) error {
	if d.cfg.DebugAddr == "" {
		return nil
	}
	host, _, err := net.SplitHostPort(d.cfg.DebugAddr)
	if err != nil || (host != "127.0.0.1" && host != "localhost" && host != "::1") {
		return fmt.Errorf("debug surface must bind loopback, got %q", d.cfg.DebugAddr)
	}

	drifter, ok := d.cfg.Compute.(interface {
		DriftStop(vmID string, epoch int64) bool
	})
	if !ok {
		return fmt.Errorf("compute driver has no drift injection (fake tier only)")
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /drift", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimSpace(r.URL.Query().Get("vm"))
		d.mu.Lock()
		var vmID string
		var epoch int64
		for id, in := range d.lastApplied {
			if in.GetVmName() == name {
				vmID, epoch = id, in.GetPlacementEpoch()
				break
			}
		}
		d.mu.Unlock()
		if vmID == "" {
			http.Error(w, "unknown vm "+name, http.StatusNotFound)
			return
		}
		if !drifter.DriftStop(vmID, epoch) {
			http.Error(w, "no domain for "+name, http.StatusConflict)
			return
		}
		fmt.Fprintf(w, "drift injected: %s (epoch %d) stopped out of band\n", name, epoch)
	})

	srv := &http.Server{Addr: d.cfg.DebugAddr, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			d.cfg.Log.Warn("debug surface failed", "err", err)
		}
	}()
	d.cfg.Log.Info("debug surface serving (loopback only)", "addr", d.cfg.DebugAddr)
	return nil
}
