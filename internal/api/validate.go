package api

import (
	"fmt"
	"regexp"

	vmcv1 "github.com/sigtunnel/vm-control-plane/proto/vmc/v1"
)

// dnsLabel: what we accept as a resource name (also becomes a libvirt
// domain name and file-path component later — strict input validation here
// is part of the containment story).
var dnsLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

func validateName(name string) error {
	if !dnsLabel.MatchString(name) {
		return fmt.Errorf("name %q must be a DNS label (lowercase alphanumerics and '-', max 63 chars)", name)
	}
	return nil
}

func validateSpec(spec *vmcv1.VmSpec) error {
	if spec == nil {
		return fmt.Errorf("spec is required")
	}
	if spec.Cpus < 1 || spec.Cpus > 64 {
		return fmt.Errorf("cpus must be in [1,64], got %d", spec.Cpus)
	}
	const minMem, maxMem = 64 << 20, 256 << 30
	if spec.MemoryBytes < minMem || spec.MemoryBytes > maxMem {
		return fmt.Errorf("memory_bytes must be in [64MiB,256GiB], got %d", spec.MemoryBytes)
	}
	if spec.Image == "" {
		return fmt.Errorf("image is required")
	}
	if spec.RootDiskBytes < 1<<30 {
		return fmt.Errorf("root_disk_bytes must be >= 1GiB, got %d", spec.RootDiskBytes)
	}
	if spec.Network != "" {
		if err := validateName(spec.Network); err != nil {
			return fmt.Errorf("network: %w", err)
		}
	}
	switch spec.Power {
	case vmcv1.PowerState_POWER_STATE_RUNNING, vmcv1.PowerState_POWER_STATE_STOPPED:
	default:
		return fmt.Errorf("power: unknown value %d", spec.Power)
	}
	return nil
}

// normalizeSpec applies defaults on a clone before hashing — the caller's
// request is never mutated, and the hash sees the same bytes a defaulted
// replay would send.
func normalizeSpec(spec *vmcv1.VmSpec) {
	if spec == nil {
		return
	}
	if spec.Power == vmcv1.PowerState_POWER_STATE_UNSPECIFIED {
		spec.Power = vmcv1.PowerState_POWER_STATE_RUNNING
	}
}
