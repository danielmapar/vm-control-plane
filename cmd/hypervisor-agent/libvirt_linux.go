//go:build linux

package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/sigtunnel/vm-control-plane/internal/driver/compute"
	libvirtdrv "github.com/sigtunnel/vm-control-plane/internal/driver/compute/libvirt"
)

type libvirtDriverOpts struct {
	Socket, StorageRoot, MgmtNetwork, TenantBridge, SSHKeyFile string
}

// newLibvirtDriver builds the real driver and ensures the management NAT
// network exists before the agent serves any intent.
func newLibvirtDriver(ctx context.Context, o libvirtDriverOpts) (compute.Driver, error) {
	var sshKey string
	if o.SSHKeyFile != "" {
		b, err := os.ReadFile(o.SSHKeyFile)
		if err != nil {
			return nil, fmt.Errorf("read ssh key: %w", err)
		}
		sshKey = strings.TrimSpace(string(b))
	}
	cache := libvirtdrv.FileImageCache{Root: o.StorageRoot}
	d := libvirtdrv.New(libvirtdrv.Config{
		Socket:           o.Socket,
		StorageRoot:      o.StorageRoot,
		MgmtNetwork:      o.MgmtNetwork,
		TenantBridge:     o.TenantBridge,
		SSHAuthorizedKey: sshKey,
		ResolveBacking:   cache.Resolve,
	})
	if err := d.EnsureMgmtNetwork(ctx); err != nil {
		return nil, fmt.Errorf("ensure management network: %w", err)
	}
	return d, nil
}
