// Command control-plane hosts the gRPC API, the reconciler, and the
// scheduler behind role flags (ADR-0001). The API and reconciler share only
// the store — they never call each other.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"

	"google.golang.org/grpc"

	"github.com/sigtunnel/vm-control-plane/internal/api"
	"github.com/sigtunnel/vm-control-plane/internal/reconciler"
	"github.com/sigtunnel/vm-control-plane/internal/store"
	"github.com/sigtunnel/vm-control-plane/internal/version"
	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"

	"github.com/jackc/pgx/v5/pgxpool"
)

func main() {
	var (
		listen = flag.String("listen", "127.0.0.1:7070", "gRPC listen address (loopback by default — D15)")
		dbURL  = flag.String("db-url", os.Getenv("VMC_DB_URL"), "PostgreSQL URL (or VMC_DB_URL)")
		role   = flag.String("role", "api,controller", "comma-separated roles to run")
	)
	flag.Parse()

	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	log.Info("control-plane starting", "version", version.String(), "listen", *listen, "role", *role)

	if *dbURL == "" {
		log.Error("no database URL: pass --db-url or set VMC_DB_URL")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := run(ctx, log, *listen, *dbURL, *role); err != nil {
		log.Error("control-plane exiting", "err", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, log *slog.Logger, listen, dbURL, role string) error {
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("connect: %w", err)
	}
	defer pool.Close()

	if err := store.Migrate(ctx, pool); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	st := store.New(pool)
	srv := api.NewServer(st, log)

	lis, err := net.Listen("tcp", listen)
	if err != nil {
		return fmt.Errorf("listen %s (hint: check Hyper-V port exclusions with 'netsh interface ipv4 show excludedportrange protocol=tcp'): %w", listen, err)
	}

	g := grpc.NewServer()
	vmcv1.RegisterVMServiceServer(g, srv)
	vmcv1.RegisterOperationServiceServer(g, srv)
	vmcv1.RegisterAgentServiceServer(g, api.NewAgentServer(st))

	if strings.Contains(role, "controller") {
		loop := reconciler.New(st, reconciler.Config{Owner: "control-plane", Log: log})
		go loop.Run(ctx)
	}

	go func() {
		<-ctx.Done()
		log.Info("shutting down")
		g.GracefulStop()
	}()

	log.Info("serving", "addr", lis.Addr().String())
	return g.Serve(lis)
}
