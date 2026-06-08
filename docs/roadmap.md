# pve-cli Roadmap

This roadmap tracks the path to full Proxmox VE 9.2 API coverage in the `pve` CLI.

## Background

The `pve` CLI is built on the generated `pve-apiclient-go` v3 client, which exposes 716 service methods covering the complete Proxmox VE 9.2 REST surface (all 444 documented endpoints), including the 9.2 net-new features: SDN fabrics, storage identity, in-place token-secret rotation, custom CPU models, node location, and OCI container images.

Today the CLI surfaces only a subset of that client. Every capability listed below is backed by a method that already exists in the client, so the remaining work is command-surface wiring, validation, and end-to-end test coverage — no client regeneration is required.

Priorities run from P1 (highest impact, ship first) to P4 (specialized or low-frequency operations). P5 closes the remaining end-to-end test-coverage gaps so that every shipped command leaf is either exercised by a suite or formally deferred with a rationale. The **Status** column tracks delivery: `Planned`, `In progress`, or `Shipped`.

## Priority Overview

| Priority | Theme | Focus |
|---|---|---|
| P1 | Guest lifecycle operations | Clone, migrate, and disk management for VMs and containers |
| P2 | Operations and security | Firewalls, backups, high availability, authentication realms, storage transfer, cluster and node configuration |
| P3 | Platform management | Guest agent, package management, hardware, system config, Ceph, metrics, notifications, SDN extensions |
| P4 | Specialized workflows | Bulk actions, SDN fabrics and routing policy, and newer PVE 9.2 endpoints |
| P5 | Test coverage closure | Isolated end-to-end or deferral coverage for every shipped command leaf that neither suite yet exercises |

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
| ACME and Ceph flags | `pve cluster acme`, `pve cluster ceph flags` | ACME accounts and plugins, global Ceph flags | Shipped |
| Ceph management | `pve node ceph` | Status, configuration, OSD, pool, monitor, MDS, MGR, and filesystem control | Shipped |
| SDN extensions | `pve sdn controller\|ipam\|dns`, `pve sdn vnet set\|firewall` | Routing controllers, IPAM backends, DNS providers, VNet updates, and per-VNet firewalls | Shipped |
| Cluster storage | `pve storage create\|set\|get` | Datacenter-wide storage definitions with full per-backend attributes (dir, NFS, CIFS, LVM, ZFS, Ceph, PBS, iSCSI) and credential scrubbing on read | Shipped |
| Storage browsing | `pve storage file-restore`, `pve storage import-metadata`, `pve storage volume` | Backup file browsing and download, guest import metadata, and per-volume inspection, notes/protection update, and copy | Shipped |

## P4 — Specialized Workflows

Bulk and advanced features for larger or newer deployments.

| Feature | Commands | Notes | Status |
|---|---|---|---|
| Bulk actions | `pve cluster bulk start\|shutdown\|suspend\|migrate`, `pve node startall\|stopall\|suspendall\|migrateall\|wakeonlan` | Fleet-wide start, stop, suspend, and migrate across the cluster or a single node, plus Wake-on-LAN | Shipped |
| SDN preview and rollback | `pve sdn dry-run`, `pve sdn rollback` | Preview the running-vs-pending SDN diff for a node, and discard all pending SDN changes cluster-wide | Shipped |
| SDN fabrics and routing policy | `pve sdn fabric`, `pve sdn prefix-list`, `pve sdn route-map` | BGP fabric topology and routing policy | Shipped |
| PVE 9.2 endpoints | `pve node oci`, `pve node capabilities`, `pve cluster cpu-model` | OCI image import, capability queries, and custom CPU models | Shipped |

## P5 — Test Coverage Closure

Every command in the priorities above is shipped. P5 closed the 133 command leaves that neither the read-only sweep nor the destructive verb matrix exercised, driving the uncovered count in the test-coverage matrix (`docs/test-coverage-matrix.md`) to zero. A leaf counts as closed when it is either exercised by an isolated, namespaced check or formally deferred with a documented rationale, a `--yes` confirmation guard, and unit-test coverage of that guard and its argument contract.

The 133 leaves split into three bands, ordered by value gained per unit of risk and effort: read-only reads first (real coverage, zero mutation), then isolated mutate lifecycles (exercise real create-update-delete flows against `pve-cli`-owned resources), then deferral hardening for the destructive, interactive, and secret-bearing verbs that must never touch the shared lab. All new live checks reuse the existing isolation contract: tag and pool `pve-cli`, name prefix `pve-cli-`, SDN zone `pvecli`, VNet `pvecli0`, and subnet `172.30.0.0/24`. Secret values are never parsed, echoed, or logged; secret-bearing commands are exercised with throwaway dummy inputs.

### P5.1 — Read-only diagnostics sweep (36 leaves)

Idempotent reads added to the read-only sweep. Reads against `pve-cli`-owned objects are covered alongside the matching P5.2 lifecycle (`create` → `get` → `delete`); the rows below list the standalone reads.

| Area | Commands | Approach | Status |
|---|---|---|---|
| LXC diagnostics | `pve lxc feature`, `pve lxc metrics`, `pve lxc rrd`, `pve lxc migrate check`, `pve lxc snapshot show` | Inventory-gated reads mirroring the existing QEMU diagnostic checks | Shipped |
| Ceph inspection | `pve node ceph fs\|mds\|mgr\|mon list`, `pve node ceph osd\|pool get`, `pve node ceph pool status` | Reads gated on a Ceph-configured node | Shipped |
| Storage and host discovery | `pve node scan cifs\|iscsi\|lvmthin\|pbs`, `pve node query-url-metadata`, `pve node vzdump extract-config` | Read-only discovery and archive-config extraction | Shipped |
| Cluster inspection | `pve cluster ceph flags get`, `pve cluster ha status manager`, `pve cluster acme account get` | Unconditional cluster reads | Shipped |
| Namespaced object reads | `pve sdn controller\|dns\|fabric\|fabric node\|prefix-list\|prefix-list entry\|route-map\|route-map entry get\|list`, `pve cluster notifications sendmail\|smtp\|webhook\|matcher get`, `pve cluster mapping pci\|usb get` | Covered within their P5.2 lifecycle | Shipped |

### P5.2 — Isolated mutate lifecycle (51 leaves)

Namespaced create → inspect → update → delete sequences against `pve-cli`-owned resources, with teardown in every path.

| Area | Commands | Approach | Status |
|---|---|---|---|
| SDN objects | `pve sdn zone\|vnet\|subnet\|controller\|dns\|ipam\|fabric\|fabric node\|prefix-list\|prefix-list entry\|route-map\|route-map entry` create/set/delete, `pve sdn vnet firewall options\|rules set` | Stage against zone `pvecli` / VNet `pvecli0` / subnet `172.30.0.0/24`, then `pve sdn apply`; teardown reverts all staged changes | Shipped |
| Notification targets | `pve cluster notifications sendmail\|smtp\|webhook\|matcher create\|set\|delete`, `pve cluster notifications targets-test` | `pve-cli-` named targets with dummy credentials that are never echoed | Shipped |
| Device mappings | `pve cluster mapping pci\|usb create\|set\|delete` | `pve-cli-` named PCI and USB mappings | Shipped |
| Firewall rule edits | `pve cluster firewall rules\|alias\|group rule-update`, `pve node firewall rules update`, `pve qemu\|lxc firewall rules\|alias update` | Edits to `pve-cli-` owned rules, aliases, and groups by index | Shipped |
| Snapshot edits | `pve qemu snapshot update`, `pve lxc snapshot update` | Re-describe a snapshot on a `pve-cli-` guest | Shipped |
| Pool teardown | `pve pool delete` | Create-then-delete a `pve-cli` pool with `--yes` | Shipped |

### P5.3 — Deferral hardening (46 leaves)

Destructive, interactive, secret-bearing, or environment-bound verbs that must not run against the shared lab. Each gains a `--yes` confirmation guard (added where missing), unit-test coverage of the guard and argument contract via the `testhelper` fake server, and a `defer()` record in the harness so the matrix scores it deferred rather than uncovered.

| Area | Commands | Approach | Status |
|---|---|---|---|
| Ceph cluster operations | `pve node ceph fs\|mds\|mgr\|mon\|osd\|pool create\|delete\|set\|in\|out\|scrub`, `pve node ceph start\|stop` | Cluster-affecting; guard plus unit tests, deferred from live | Shipped |
| Host storage and network | `pve node disks create directory\|lvmthin\|zfs`, `pve node disks init-gpt`, `pve node network set\|delete\|revert` | Host-destructive; guard plus unit tests | Shipped |
| Host services and system | `pve node services start\|stop\|reload`, `pve node apt repositories enable`, `pve node subscription delete\|update`, `pve node cert acme delete\|renew`, `pve node cert custom delete`, `pve node console` | Host-state mutation; guard added to the unguarded service verbs, plus unit tests | Shipped |
| Cluster membership and HA | `pve cluster config join add`, `pve cluster config nodes delete`, `pve cluster ha resource relocate`, `pve cluster ha status arm`, `pve cluster acme account set\|delete` | Cluster-destructive; guard plus unit tests | Shipped |
| Guest agent | `pve qemu agent exec\|exec-status\|file-read\|file-write\|set-user-password` | Environment-bound to a running guest and agent; guard added to the password verb, plus unit tests | Shipped |
| Two-factor authentication | `pve access tfa create\|set\|delete` | Auth and secret-bearing; guard plus unit tests | Shipped |

## Endpoint-Level Completion

A second coverage pass closed the remaining endpoint-level gaps inside the groups above, wiring every user-facing client method that the earlier group-level work left unsurfaced. The following commands round out each area; destructive or cross-cluster operations carry `--yes` confirmation and are exercised by unit tests rather than the shared live lab.

| Area | Commands | Notes |
|---|---|---|
| QEMU guest agent | `pve qemu agent exec\|exec-status\|file-read\|file-write\|set-user-password` | Run commands and move files inside a running guest; the password is read from stdin and never echoed |
| QEMU and LXC diagnostics | `pve qemu metrics\|rrd\|feature\|migrate check`, `pve lxc metrics\|rrd\|feature\|migrate check\|config pending` | Time-series metrics, feature feasibility, and migration pre-flight checks |
| QEMU and LXC snapshots | `pve qemu snapshot show\|update`, `pve lxc snapshot show\|update` | Inspect and re-describe existing snapshots |
| QEMU low level | `pve qemu monitor`, `pve qemu sendkey`, `pve qemu remote-migrate`, `pve lxc remote-migrate` | Raw monitor passthrough, key injection, and cross-cluster migration |
| SDN | `pve sdn zone set`, `pve sdn vnet subnet set`, `pve sdn vnet ips create\|set\|delete`, `pve sdn fabric list-all`, `pve sdn lock acquire\|release` | Completes zone, subnet, and VNet IP management |
| Cluster | `pve cluster backup included-volumes`, `pve cluster backup-info not-backed-up`, `pve cluster notifications targets test`, `pve cluster notifications matcher-fields\|matcher-field-values`, `pve cluster jobs schedule-analyze`, `pve cluster ceph metadata`, `pve cluster firewall macros\|refs`, `pve cluster config apiversion\|qdevice\|totem` | Backup coverage audits, notification validation, schedule analysis, and cluster diagnostics |
| Access | `pve access tfa create\|set\|get-entry\|types`, `pve access openid list` | Two-factor enrollment and OpenID realm listing |
| Node | `pve node disks ls\|get\|delete`, `pve node rrddata`, `pve node netstat`, `pve node vzdump defaults\|extract-config`, `pve node capabilities qemu cpu-flags`, `pve node hardware pci mdev`, `pve node query-url-metadata`, `pve node services state` | Disk inventory and lifecycle, metrics, and capability inspection |
| Storage | `pve storage status\|identity\|rrddata\|rrd`, `pve storage volume alloc\|delete` | Per-storage usage and metrics, and volume allocation and deletion |

## Delivery Standard

Each feature ships only after it:

- builds cleanly (`go build ./...`),

- passes end-to-end tests against a live lab environment using an isolated, namespaced set of resources that never disrupt other workloads,

- and clears quality review and test-coverage review.

Destructive operations require explicit confirmation, and long-running operations support asynchronous task handling via the standard task UPID flow.
