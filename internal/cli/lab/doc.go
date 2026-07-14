// Package lab implements the `pmx lab` command group: manage per-member
// nested lab environments (SDN vnet, VM, storage, DNS, access, quota) inside
// a Proxmox VE cluster. Lab definitions are config-driven, resolved from
// ~/.config/pmx/config.yml's `labs`/`labs_dir`/`include` keys (see
// config.ResolveLabs), with CLI flags overriding individual fields per
// invocation.
package lab
