package config

// Lab describes one nested (or, in a future mode, hardware) lab environment:
// its network, compute, storage, DNS, provisioning, and access settings.
type Lab struct {
	// Name is the lab's display name. Defaults to its map key in Config.Labs.
	Name string `yaml:"name,omitempty"`

	// Mode selects how the lab is realized: "nested" (VM-in-VM, v1 only) or
	// "hardware" (bare metal, reserved for future use).
	Mode string `yaml:"mode"`

	// Owner is the pve user this lab is assigned to ("user@realm"); "~" or ""
	// means no owner.
	Owner string `yaml:"owner"`

	// Network holds the SDN zone/vnet/subnet configuration for the lab.
	Network LabNetwork `yaml:"network"`

	// Compute holds the CPU and memory configuration for the lab's VM.
	Compute LabCompute `yaml:"compute"`

	// Storage holds the ZFS pool and disk sizing for the lab.
	Storage LabStorage `yaml:"storage"`

	// DNS holds the DNS zone configuration for the lab.
	DNS LabDNS `yaml:"dns"`

	// Provisioning holds the guest provisioning configuration for the lab.
	Provisioning LabProvisioning `yaml:"provisioning"`

	// Access holds the pve realm/pool/role granted to the lab owner.
	Access LabAccess `yaml:"access"`
}

// LabNetwork describes the SDN vnet, VXLAN tag, and subnet layout for a lab.
type LabNetwork struct {
	// VnetID is the SDN vnet identifier: at most 8 alphanumeric characters,
	// no hyphen (enforced by validation, not by this type).
	VnetID string `yaml:"vnet_id"`

	// VnetAlias is a human-readable label for the vnet.
	VnetAlias string `yaml:"vnet_alias"`

	// VxlanTag is the VXLAN tag assigned to the vnet.
	VxlanTag int `yaml:"vxlan_tag"`

	// CIDR is the overall subnet range allocated to the lab.
	CIDR string `yaml:"cidr"`

	// Mgmt holds the management subnet, host IP, and gateway for the lab.
	Mgmt LabMgmt `yaml:"mgmt"`

	// BoshBloc is the subnet range reserved for BOSH-deployed VMs in the lab.
	BoshBloc string `yaml:"bosh_bloc"`

	// MTU is the maximum transmission unit for the vnet.
	MTU int `yaml:"mtu"`
}

// LabMgmt describes the lab's management subnet.
type LabMgmt struct {
	// Subnet is the management subnet CIDR: an address-plan reservation
	// within LabNetwork.CIDR marking which slice is set aside for
	// management-plane hosts. It is NOT an interface prefix: the lab host's
	// interface must be addressed with LabNetwork.CIDR's own prefix length
	// (e.g. host_ip/16 for a /16 lab, even when Subnet is a /24). A narrower
	// interface prefix makes the host route replies to on-link guests in the
	// wider CIDR via the gateway, which drops them as out-of-state.
	Subnet string `yaml:"subnet"`

	// HostIP is the management-plane IP address of the lab host.
	HostIP string `yaml:"host_ip"`

	// Gateway is the management subnet's gateway address.
	Gateway string `yaml:"gateway"`
}

// LabCompute describes the CPU and memory configuration for a lab's VM.
type LabCompute struct {
	// VCPU is the number of virtual CPUs assigned to the VM.
	VCPU int `yaml:"vcpu"`

	// CPUType is the QEMU CPU model presented to the guest.
	CPUType string `yaml:"cpu_type"`

	// NUMA enables NUMA topology awareness for the VM.
	NUMA bool `yaml:"numa"`

	// Machine is the QEMU machine type.
	Machine string `yaml:"machine"`

	// Firmware is the VM firmware type (e.g. "ovmf" for UEFI, "seabios" for legacy BIOS).
	Firmware string `yaml:"firmware"`

	// Memory holds the VM's minimum and maximum memory sizing.
	Memory LabMemory `yaml:"memory"`
}

// LabMemory describes the memory ballooning range for a lab's VM.
type LabMemory struct {
	// MinGB is the VM's minimum (guaranteed) memory in gigabytes.
	MinGB int `yaml:"min_gb"`

	// MaxGB is the VM's maximum (ballooned) memory in gigabytes. Schema
	// default is 96; a given lab may deploy up to 128 per its own needs.
	MaxGB int `yaml:"max_gb"`
}

// LabStorage describes the ZFS pool and disk sizing for a lab's VM.
type LabStorage struct {
	// Pool is the ZFS storage pool the lab's disks live on.
	Pool string `yaml:"pool"`

	// OSDiskGB is the size of the OS disk in gigabytes.
	OSDiskGB int `yaml:"os_disk_gb"`

	// DataDiskGB is the size of the data disk in gigabytes.
	DataDiskGB int `yaml:"data_disk_gb"`

	// RefquotaGB is the ZFS refquota enforced on the lab's dataset, in gigabytes.
	RefquotaGB int `yaml:"refquota_gb"`

	// Controller is the disk controller type (e.g. "virtio-scsi-single").
	Controller string `yaml:"controller"`

	// IOThread enables a dedicated I/O thread for the disk.
	IOThread bool `yaml:"iothread"`

	// Discard enables discard/TRIM passthrough for the disk.
	Discard bool `yaml:"discard"`

	// SSD marks the disk as SSD-backed to the guest.
	SSD bool `yaml:"ssd"`
}

// LabDNS describes the DNS zone associated with a lab.
type LabDNS struct {
	// Zone is the DNS zone name for the lab.
	Zone string `yaml:"zone"`
}

// LabProvisioning describes how a lab's guest is provisioned.
type LabProvisioning struct {
	// Mode selects the provisioning method (e.g. "cloud-init").
	Mode string `yaml:"mode"`

	// AnswerTemplate is the path to the answer-file template used to
	// provision the guest.
	AnswerTemplate string `yaml:"answer_template"`

	// SSHKeys lists the SSH public keys injected into the guest.
	SSHKeys []string `yaml:"ssh_keys"`
}

// LabAccess describes the pve realm, pool, and role granted to a lab's owner.
type LabAccess struct {
	// Realm is the pve authentication realm the owner is granted access under.
	Realm string `yaml:"realm"`

	// Pool is the pve resource pool the lab's role grant is scoped to.
	Pool string `yaml:"pool"`

	// Role is the pve role granted to the owner on Pool.
	Role string `yaml:"role"`
}
