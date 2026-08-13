//go:build !linux

package main

import (
	"context"
	"fmt"

	"github.com/sigtunnel/vm-control-plane/internal/driver/compute"
)

func newLibvirtDriver(context.Context, libvirtDriverOpts) (compute.Driver, error) {
	return nil, fmt.Errorf("the libvirt driver requires Linux (this is %s); use --driver fake here", "windows/other")
}
