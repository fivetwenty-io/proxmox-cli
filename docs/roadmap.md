# pve-cli Roadmap

This roadmap tracks the path to full Proxmox VE 9.2 API coverage in the `pve` CLI.

## Background

The `pve` CLI is built on the generated `pve-apiclient-go` v3 client, which exposes 716 service methods covering the complete Proxmox VE 9.2 REST surface (all 444 documented endpoints), including the 9.2 net-new features: SDN fabrics, storage identity, in-place token-secret rotation, custom CPU models, node location, and OCI container images.

Today the CLI surfaces only a subset of that client. Every capability listed below is backed by a method that already exists in the client, so the remaining work is command-surface wiring, validation, and end-to-end test coverage — no client regeneration is required.

Priorities run from P1 (highest impact, ship first) to P4 (specialized or low-frequency operations). The **Status** column tracks delivery: `Planned`, `In progress`, or `Shipped`.

## Priority Overview

| Priority | Theme | Focus |
|---|---|---|
| P1 | Guest lifecycle operations | Clone, migrate, and disk management for VMs and containers |
| P2 | Operations and security | Firewalls, backups, high availability, authentication realms, storage transfer, cluster and node configuration |
| P3 | Platform management | Guest agent, package management, hardware, system config, Ceph, metrics, notifications, SDN extensions |
| P4 | Specialized workflows | Bulk actions, SDN fabrics and routing policy, and newer PVE 9.2 endpoints |

## P1 — Guest Lifecycle Operations

The most frequently requested day-to-day actions. These complete the create, run, and snapshot lifecycle already present in the CLI.

| Feature | Commands | Notes | Status |
|---|---|---|---|
| VM clone and migrate | `pve qemu clone`, `pve qemu migrate` | Asynchronous; supports online and offline migration | Shipped |
| VM disk management | `pve qemu disk resize`, `pve qemu disk move`, `pve qemu disk unlink` | Grow, relocate, and detach VM disks | Shipped |
| Container clone and migrate | `pve lxc clone`, `pve lxc migrate` | Local and remote migration | Shipped |
| Container disk management | `pve lxc disk resize`, `pve lxc disk move` | Grow and relocate container volumes | Shipped |

## P2 — Operations and Security

Capabilities that production operators depend on for protection, isolation, and cluster control.

| Feature | Commands | Notes | Status |
|---|---|---|---|
| VM firewall | `pve qemu firewall rules\|ipset\|alias\|options` | Per-VM rule, IP set, and alias management | Shipped |
| Container firewall | `pve lxc firewall rules\|ipset\|alias\|options` | Per-container firewall management | Shipped |
| Guest consoles | `pve qemu console`, `pve lxc console` | VNC, terminal, and SPICE proxy tickets | Shipped |
| Backup management | `pve cluster backup`, `pve node vzdump`, `pve storage prune` | Schedules, on-demand backups, coverage audits, and pruning | Shipped |
| High availability | `pve cluster ha resource\|group\|rule\|status` | Resource, group, and rule management, manual migrate and relocate, manager status, arm and disarm | Shipped |
| Authentication realms | `pve access domain` | Realm CRUD and user/group synchronization for ldap/ad realms | Shipped |
| Two-factor authentication | `pve access tfa` | List, inspect, delete TFA entries, and unlock locked-out users | Shipped |
| Cluster firewall | `pve cluster firewall rules\|group\|ipset\|alias\|options` | Cluster-wide security policy | Shipped |
| Node firewall | `pve node firewall rules\|options` | Per-node firewall management | Shipped |
| Cluster configuration | `pve cluster options`, `pve cluster config`, `pve cluster replication` | Global options, membership, and storage replication jobs | Shipped |
| Node network | `pve node network` | Interface and bridge configuration | Shipped |
| Storage transfer | `pve storage upload`, `pve storage download-url` | Push local files and pull ISOs or templates from URLs | Shipped |

## P3 — Platform Management

Administrative depth across guests, nodes, storage, and software-defined networking.

| Feature | Commands | Notes | Status |
|---|---|---|---|
| VM guest integration | `pve qemu agent`, `pve qemu cloudinit`, `pve qemu reset`, `pve qemu template` | Guest agent, cloud-init exposure, hard reset, template conversion | Shipped |
| Container interfaces | `pve lxc interfaces` | Network interface inspection | Shipped |
| Package management | `pve node apt` | Updates, versions, changelogs, and repositories | Shipped |
| Disks and hardware | `pve node disks`, `pve node scan`, `pve node hardware` | SMART data, storage discovery, and PCI/USB inventory | Shipped |
| Node system config | `pve node dns\|hosts\|time\|syslog\|journal\|report\|subscription` | Host-level configuration and diagnostics | Shipped |
| Certificates | `pve node cert` | Custom and ACME certificate management | Shipped |
| Node replication | `pve node replication` | Per-node replication view and on-demand runs | Shipped |
| Metrics and notifications | `pve cluster metrics`, `pve cluster notifications` | External metric targets and alert routing | Shipped |
| Device mapping and jobs | `pve cluster mapping`, `pve cluster jobs` | PCI, USB, and directory mappings, and scheduled realm sync | Shipped |
| ACME and Ceph flags | `pve cluster acme`, `pve cluster ceph flags` | ACME accounts and plugins, global Ceph flags | Planned |
| Ceph management | `pve node ceph` | Status, configuration, OSD, pool, monitor, MDS, MGR, and filesystem control | Planned |
| SDN extensions | `pve sdn controller\|ipam\|dns`, `pve sdn vnet` | Routing controllers, IPAM, DNS providers, VNet updates and firewalls | Planned |
| Cluster storage | `pve cluster storage` | Cluster-level storage definitions | Planned |
| Storage browsing | `pve storage file-restore`, `pve storage import-metadata`, `pve storage volume` | Backup browsing, import metadata, and volume copy and update | Planned |

## P4 — Specialized Workflows

Bulk and advanced features for larger or newer deployments.

| Feature | Commands | Notes | Status |
|---|---|---|---|
| Bulk actions | `pve cluster bulk`, `pve node startall\|stopall\|suspendall\|migrateall\|wakeonlan` | Fleet-wide start, stop, suspend, and migrate | Planned |
| SDN preview and rollback | `pve sdn dry-run`, `pve sdn rollback` | Preview and revert pending SDN changes | Planned |
| SDN fabrics and routing policy | `pve sdn fabric`, `pve sdn prefix-list`, `pve sdn route-map` | BGP fabric topology and routing policy | Planned |
| PVE 9.2 endpoints | `pve node oci`, `pve node capabilities`, `pve cluster cpu-model` | OCI image import, capability queries, and custom CPU models | Planned |

## Delivery Standard

Each feature ships only after it:

- builds cleanly (`go build ./...`),

- passes end-to-end tests against a live lab environment using an isolated, namespaced set of resources that never disrupt other workloads,

- and clears quality review and test-coverage review.

Destructive operations require explicit confirmation, and long-running operations support asynchronous task handling via the standard task UPID flow.
