package main

// libvirtDriverOpts carries the libvirt-driver flags from the command line to
// newLibvirtDriver. The type is untagged so it compiles on every platform,
// while the functions that consume it stay behind the libvirt build tags.
type libvirtDriverOpts struct {
	Socket       string
	StorageRoot  string
	MgmtNetwork  string
	TenantBridge string
	SSHKeyFile   string
	CPUSet       string
	Emulated     bool
}
