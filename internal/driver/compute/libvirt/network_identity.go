//go:build linux

package libvirt

import (
	"fmt"
	"hash/fnv"

	"github.com/google/uuid"
)

// vlanFor maps a tenant network name to a stable VLAN id in [100, 4000).
func vlanFor(network string) uint16 {
	h := fnv.New32a()
	_, _ = h.Write([]byte(network))
	return uint16(100 + h.Sum32()%3900)
}

// tenantAddr derives a deterministic demo tenant IP (10.100.x.y/24) from
// the VM UUID. Real IPAM lands with the network resource.
func tenantAddr(id uuid.UUID) string {
	return fmt.Sprintf("10.100.%d.%d/24", id[14]%254+1, id[15]%253+2)
}
