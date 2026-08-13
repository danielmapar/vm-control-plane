package cli

import (
	"context"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func dial(server string) (*grpc.ClientConn, error) {
	return grpc.NewClient(server, grpc.WithTransportCredentials(insecure.NewCredentials()))
}

// withConn dials the server, bounds the work with a timeout, runs fn, then
// closes the connection. It centralizes the dial/timeout/close boilerplate
// every command shares.
func withConn(cmd *cobra.Command, server string, timeout time.Duration, fn func(ctx context.Context, conn *grpc.ClientConn) error) error {
	conn, err := dial(server)
	if err != nil {
		return err
	}
	defer conn.Close() //nolint:errcheck // process exit follows
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()
	return fn(ctx, conn)
}
