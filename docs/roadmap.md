# pmx-cli Roadmap

This roadmap tracks the path to full Proxmox VE 9.2 API coverage in the `pmx` CLI.

## Background

The `pmx` CLI is built on the generated `proxmox-apiclient-go` v3 client, which exposes 716 service methods covering the complete Proxmox VE 9.2 REST surface (all 444 documented endpoints), including the 9.2 net-new features: SDN fabrics, storage identity, in-place token-secret rotation, custom CPU models, node location, and OCI container images.

The CLI now surfaces the complete user-facing command set. Every capability listed below is shipped and backed by a method that already exists in the client, so no client regeneration is required. The remaining work is test-coverage maintenance: keeping each shipped leaf either exercised by a live suite or formally deferred with a documented rationale.

Priorities run from P1 (highest impact, ship first) to P4 (specialized or low-frequency operations). P5 closed the remaining end-to-end test-coverage gaps so that every shipped command leaf is either exercised by a suite or formally deferred with a rationale. P6 then shrank the deferred bucket itself by finding live strategies for verbs that earlier passes had set aside, leaving only those that are genuinely impossible to exercise on the shared lab. The **Status** column tracks delivery: `Planned`, `In progress`, or `Shipped`.

## Context management

The `pmx context` group (alias: `pmx ctx`) ships as a first-class command group
for managing named PVE endpoint configurations. It is separate from the API
coverage priorities below because it operates on the local config file rather
than the Proxmox VE API.

| Feature | Commands | Notes | Status |
|---|---|---|---|
| Context CRUD | `pmx context add`, `pmx context ls`, `pmx context show`, `pmx context rm` | Add, list, inspect, and remove named contexts | Shipped |
| Context switching | `pmx context select`, `pmx context previous` | Set the active context; `-` arg toggles to the previous context | Shipped |
| Context copy and edit | `pmx context copy`, `pmx context edit` | Duplicate a context to a new name; open a context in `$EDITOR` | Shipped |
| Context field update | `pmx context update` (alias `set`) | Change individual fields of an existing context via flags, no `$EDITOR`; result validated before save | Shipped |
| Context validation | `pmx context validate` | Structural checks (host, auth type, port, protocol, fingerprint); `--all` validates every context; no network connect in v1 | Shipped |
| Config migration | `scripts/migrate-config.py` | One-shot renamer: `targets:`/`current-target:` → `contexts:`/`current-context:` for pre-rename configs | Shipped |

## Priority Overview

| Priority | Theme | Focus |
|---|---|---|
| P1 | Guest lifecycle operations | Clone, migrate, and disk management for VMs and containers |
| P2 | Operations and security | Firewalls, backups, high availability, authentication realms, storage transfer, cluster and node configuration |
| P3 | Platform management | Guest agent, package management, hardware, system config, Ceph, metrics, notifications, SDN extensions |
| P4 | Specialized workflows | Bulk actions, SDN fabrics and routing policy, and newer PVE 9.2 endpoints |
| P5 | Test coverage closure | Isolated end-to-end or deferral coverage for every shipped command leaf that neither suite yet exercises |
| P6 | Deferred-coverage recovery | Live strategies that move formerly deferred verbs into the suites, leaving only genuinely blocked leaves deferred |

## P1 — Guest Lifecycle Operations

The most frequently requested day-to-day actions. These complete the create, run, and snapshot lifecycle already present in the CLI.

| Feature | Commands | Notes | Status |
|---|---|---|---|
| VM clone and migrate | `pmx qemu clone`, `pmx qemu migrate` | Asynchronous; supports online and offline migration | Shipped |
| VM disk management | `pmx qemu disk resize`, `pmx qemu disk move`, `pmx qemu disk unlink` | Grow, relocate, and detach VM disks | Shipped |
| Container clone and migrate | `pmx lxc clone`, `pmx lxc migrate` | Local and remote migration | Shipped |
| Container disk management | `pmx lxc disk resize`, `pmx lxc disk move` | Grow and relocate container volumes | Shipped |

## P2 — Operations and Security

Capabilities that production operators depend on for protection, isolation, and cluster control.

| Feature | Commands | Notes | Status |
|---|---|---|---|
| VM firewall | `pmx qemu firewall rules\|ipset\|alias\|options` | Per-VM rule, IP set, and alias management | Shipped |
| Container firewall | `pmx lxc firewall rules\|ipset\|alias\|options` | Per-container firewall management | Shipped |
| Guest consoles | `pmx qemu console`, `pmx lxc console` | VNC, terminal, and SPICE proxy tickets | Shipped |
| Backup management | `pmx cluster backup`, `pmx node vzdump`, `pmx storage prune` | Schedules, on-demand backups, coverage audits, and pruning | Shipped |
| High availability | `pmx cluster ha resource\|group\|rule\|status` | Resource, group, and rule management, manual migrate and relocate, manager status, arm and disarm | Shipped |
| Authentication realms | `pmx access domain` | Realm CRUD and user/group synchronization for ldap/ad realms | Shipped |
| Two-factor authentication | `pmx access tfa` | List, inspect, delete TFA entries, and unlock locked-out users | Shipped |
| Cluster firewall | `pmx cluster firewall rules\|group\|ipset\|alias\|options` | Cluster-wide security policy | Shipped |
| Node firewall | `pmx node firewall rules\|options` | Per-node firewall management | Shipped |
| Cluster configuration | `pmx cluster options`, `pmx cluster config`, `pmx cluster replication` | Global options, membership, and storage replication jobs | Shipped |
| Node network | `pmx node network` | Interface and bridge configuration | Shipped |
| Storage transfer | `pmx storage upload`, `pmx storage download-url` | Push local files and pull ISOs or templates from URLs | Shipped |

## P3 — Platform Management

Administrative depth across guests, nodes, storage, and software-defined networking.

| Feature | Commands | Notes | Status |
|---|---|---|---|
| VM guest integration | `pmx qemu agent`, `pmx qemu cloudinit`, `pmx qemu reset`, `pmx qemu template` | Guest agent, cloud-init exposure, hard reset, template conversion | Shipped |
| Container interfaces | `pmx lxc interfaces` | Network interface inspection | Shipped |
| Package management | `pmx node apt` | Updates, versions, changelogs, and repositories | Shipped |
| Disks and hardware | `pmx node disks`, `pmx node scan`, `pmx node hardware` | SMART data, storage discovery, and PCI/USB inventory | Shipped |
| Node system config | `pmx node dns\|hosts\|time\|syslog\|journal\|report\|subscription` | Host-level configuration and diagnostics | Shipped |
| Certificates | `pmx node cert` | Custom and ACME certificate management | Shipped |
| Node replication | `pmx node replication` | Per-node replication view and on-demand runs | Shipped |
| Metrics and notifications | `pmx cluster metrics`, `pmx cluster notifications` | External metric targets and alert routing | Shipped |
| Device mapping and jobs | `pmx cluster mapping`, `pmx cluster jobs` | PCI, USB, and directory mappings, and scheduled realm sync | Shipped |
| ACME and Ceph flags | `pmx cluster acme`, `pmx cluster ceph flags` | ACME accounts and plugins, global Ceph flags | Shipped |
| Ceph management | `pmx node ceph` | Status, configuration, OSD, pool, monitor, MDS, MGR, and filesystem control | Shipped |
| SDN extensions | `pmx sdn controller\|ipam\|dns`, `pmx sdn vnet set\|firewall` | Routing controllers, IPAM backends, DNS providers, VNet updates, and per-VNet firewalls | Shipped |
| Cluster storage | `pmx storage create\|set\|get` | Datacenter-wide storage definitions with full per-backend attributes (dir, NFS, CIFS, LVM, ZFS, Ceph, PBS, iSCSI) and credential scrubbing on read | Shipped |
| Storage browsing | `pmx storage file-restore`, `pmx storage import-metadata`, `pmx storage volume` | Backup file browsing and download, guest import metadata, and per-volume inspection, notes/protection update, and copy | Shipped |

## P4 — Specialized Workflows

Bulk and advanced features for larger or newer deployments.

| Feature | Commands | Notes | Status |
|---|---|---|---|
| Bulk actions | `pmx cluster bulk start\|shutdown\|suspend\|migrate`, `pmx node startall\|stopall\|suspendall\|migrateall\|wakeonlan` | Fleet-wide start, stop, suspend, and migrate across the cluster or a single node, plus Wake-on-LAN | Shipped |
| SDN preview and rollback | `pmx sdn dry-run`, `pmx sdn rollback` | Preview the running-vs-pending SDN diff for a node, and discard all pending SDN changes cluster-wide | Shipped |
| SDN fabrics and routing policy | `pmx sdn fabric`, `pmx sdn prefix-list`, `pmx sdn route-map` | BGP fabric topology and routing policy | Shipped |
| PVE 9.2 endpoints | `pmx node oci`, `pmx node capabilities`, `pmx cluster cpu-model` | OCI image import, capability queries, and custom CPU models | Shipped |

## P5 — Test Coverage Closure

Every command in the priorities above is shipped. P5 closed the 133 command leaves that neither the read-only sweep nor the destructive verb matrix exercised, driving the uncovered count in the test-coverage matrix (`docs/test-coverage-matrix.md`) to zero. A leaf counts as closed when it is either exercised by an isolated, namespaced check or formally deferred with a documented rationale, a `--yes` confirmation guard, and unit-test coverage of that guard and its argument contract.

The 133 leaves split into three bands, ordered by value gained per unit of risk and effort: read-only reads first (real coverage, zero mutation), then isolated mutate lifecycles (exercise real create-update-delete flows against `pmx-cli`-owned resources), then deferral hardening for the destructive, interactive, and secret-bearing verbs that must never touch the shared lab. All new live checks reuse the existing isolation contract: tag and pool `pmx-cli`, name prefix `pmx-cli-`, SDN zone `pmxcli`, VNet `pmxcli0`, and subnet `172.30.0.0/24`. Secret values are never parsed, echoed, or logged; secret-bearing commands are exercised with throwaway dummy inputs.

### P5.1 — Read-only diagnostics sweep (36 leaves)

Idempotent reads added to the read-only sweep. Reads against `pmx-cli`-owned objects are covered alongside the matching P5.2 lifecycle (`create` → `get` → `delete`); the rows below list the standalone reads.

| Area | Commands | Approach | Status |
|---|---|---|---|
| LXC diagnostics | `pmx lxc feature`, `pmx lxc metrics`, `pmx lxc rrd`, `pmx lxc migrate check`, `pmx lxc snapshot show` | Inventory-gated reads mirroring the existing QEMU diagnostic checks | Shipped |
| Ceph inspection | `pmx node ceph fs\|mds\|mgr\|mon list`, `pmx node ceph osd\|pool get`, `pmx node ceph pool status` | Reads gated on a Ceph-configured node | Shipped |
| Storage and host discovery | `pmx node scan cifs\|iscsi\|lvmthin\|pbs`, `pmx node query-url-metadata`, `pmx node vzdump extract-config` | Read-only discovery and archive-config extraction | Shipped |
| Cluster inspection | `pmx cluster ceph flags get`, `pmx cluster ha status manager`, `pmx cluster acme account get` | Unconditional cluster reads | Shipped |
| Namespaced object reads | `pmx sdn controller\|dns\|fabric\|fabric node\|prefix-list\|prefix-list entry\|route-map\|route-map entry get\|list`, `pmx cluster notifications sendmail\|smtp\|webhook\|matcher get`, `pmx cluster mapping pci\|usb get` | Covered within their P5.2 lifecycle | Shipped |

### P5.2 — Isolated mutate lifecycle (51 leaves)

Namespaced create → inspect → update → delete sequences against `pmx-cli`-owned resources, with teardown in every path.

| Area | Commands | Approach | Status |
|---|---|---|---|
| SDN objects | `pmx sdn zone\|vnet\|subnet\|controller\|dns\|ipam\|fabric\|fabric node\|prefix-list\|prefix-list entry\|route-map\|route-map entry` create/set/delete, `pmx sdn vnet firewall options\|rules set` | Stage against zone `pmxcli` / VNet `pmxcli0` / subnet `172.30.0.0/24`, then `pmx sdn apply`; teardown reverts all staged changes | Shipped |
| Notification targets | `pmx cluster notifications sendmail\|smtp\|webhook\|matcher create\|set\|delete`, `pmx cluster notifications targets-test` | `pmx-cli-` named targets with dummy credentials that are never echoed | Shipped |
| Device mappings | `pmx cluster mapping pci\|usb create\|set\|delete` | `pmx-cli-` named PCI and USB mappings | Shipped |
| Firewall rule edits | `pmx cluster firewall rules\|alias\|group rule-update`, `pmx node firewall rules update`, `pmx qemu\|lxc firewall rules\|alias update` | Edits to `pmx-cli-` owned rules, aliases, and groups by index | Shipped |
| Snapshot edits | `pmx qemu snapshot update`, `pmx lxc snapshot update` | Re-describe a snapshot on a `pmx-cli-` guest | Shipped |
| Pool teardown | `pmx pool delete` | Create-then-delete a `pmx-cli` pool with `--yes` | Shipped |

### P5.3 — Deferral hardening (46 leaves)

Destructive, interactive, secret-bearing, or environment-bound verbs that must not run against the shared lab. Each gains a `--yes` confirmation guard (added where missing), unit-test coverage of the guard and argument contract via the `testhelper` fake server, and a `defer()` record in the harness so the matrix scores it deferred rather than uncovered.

This table records the deferral set as it stood at the close of P5. P6 (below) later found safe live strategies for many of these verbs — the guest-agent, TFA, node-network, node-services, subscription-delete, and most node-disks rows are now exercised live — so the matrix is the source of truth for what remains deferred today.

| Area | Commands | Approach | Status |
|---|---|---|---|
| Ceph cluster operations | `pmx node ceph fs\|mds\|mgr\|mon\|osd\|pool create\|delete\|set\|in\|out\|scrub`, `pmx node ceph start\|stop` | Cluster-affecting; guard plus unit tests, deferred from live | Shipped |
| Host storage and network | `pmx node disks create directory\|lvmthin\|zfs`, `pmx node disks init-gpt`, `pmx node network set\|delete\|revert` | Host-destructive; guard plus unit tests | Shipped |
| Host services and system | `pmx node services start\|stop\|reload`, `pmx node apt repositories enable`, `pmx node subscription delete\|update`, `pmx node cert acme delete\|renew`, `pmx node cert custom delete`, `pmx node console` | Host-state mutation; guard added to the unguarded service verbs, plus unit tests | Shipped |
| Cluster membership and HA | `pmx cluster config join add`, `pmx cluster config nodes delete`, `pmx cluster ha resource relocate`, `pmx cluster ha status arm`, `pmx cluster acme account set\|delete` | Cluster-destructive; guard plus unit tests | Shipped |
| Guest agent | `pmx qemu agent exec\|exec-status\|file-read\|file-write\|set-user-password` | Environment-bound to a running guest and agent; guard added to the password verb, plus unit tests | Shipped |
| Two-factor authentication | `pmx access tfa create\|set\|delete` | Auth and secret-bearing; guard plus unit tests | Shipped |

## Endpoint-Level Completion

A second coverage pass closed the remaining endpoint-level gaps inside the groups above, wiring every user-facing client method that the earlier group-level work left unsurfaced. The following commands round out each area; destructive or cross-cluster operations carry `--yes` confirmation and are exercised by unit tests rather than the shared live lab.

| Area | Commands | Notes |
|---|---|---|
| QEMU guest agent | `pmx qemu agent exec\|exec-status\|file-read\|file-write\|set-user-password` | Run commands and move files inside a running guest; the password is read from stdin and never echoed |
| QEMU and LXC diagnostics | `pmx qemu metrics\|rrd\|feature\|migrate check`, `pmx lxc metrics\|rrd\|feature\|migrate check\|config pending` | Time-series metrics, feature feasibility, and migration pre-flight checks |
| QEMU and LXC snapshots | `pmx qemu snapshot show\|update`, `pmx lxc snapshot show\|update` | Inspect and re-describe existing snapshots |
| QEMU low level | `pmx qemu monitor`, `pmx qemu sendkey`, `pmx qemu remote-migrate`, `pmx lxc remote-migrate` | Raw monitor passthrough, key injection, and cross-cluster migration |
| SDN | `pmx sdn zone set`, `pmx sdn vnet subnet set`, `pmx sdn vnet ips create\|set\|delete`, `pmx sdn fabric list-all`, `pmx sdn lock acquire\|release` | Completes zone, subnet, and VNet IP management |
| Cluster | `pmx cluster backup included-volumes`, `pmx cluster backup-info not-backed-up`, `pmx cluster notifications targets test`, `pmx cluster notifications matcher-fields\|matcher-field-values`, `pmx cluster jobs schedule-analyze`, `pmx cluster ceph metadata`, `pmx cluster firewall macros\|refs`, `pmx cluster config apiversion\|qdevice\|totem` | Backup coverage audits, notification validation, schedule analysis, and cluster diagnostics |
| Access | `pmx access tfa create\|set\|get-entry\|types`, `pmx access openid list` | Two-factor enrollment and OpenID realm listing |
| Node | `pmx node disks ls\|get\|delete`, `pmx node rrddata`, `pmx node netstat`, `pmx node vzdump defaults\|extract-config`, `pmx node capabilities qemu cpu-flags`, `pmx node hardware pci mdev`, `pmx node query-url-metadata`, `pmx node services state` | Disk inventory and lifecycle, metrics, and capability inspection |
| Storage | `pmx storage status\|identity\|rrddata\|rrd`, `pmx storage volume alloc\|delete` | Per-storage usage and metrics, and volume allocation and deletion |

## P6 — Deferred-Coverage Recovery

After P5 brought the uncovered count to zero, a follow-on pass revisited the deferred bucket itself. Every leaf that an earlier pass had formally deferred was re-examined for a live strategy that exercises it without disrupting the shared lab. This drove the deferred count from 108 leaves down to 54: of **556** leaves, **502** are now exercised by at least one live suite, **0** are uncovered, and **54** remain deferred with a specific, accurate rationale in the test-coverage matrix (`docs/test-coverage-matrix.md`).

### Recovered leaves

The recovered verbs use host-side fixture staging over root SSH to satisfy the environment dependencies that previously forced deferral. Every staged fixture is torn down in all paths, and the existing isolation contract (tag and pool `pmx-cli`, name prefix `pmx-cli-`, SDN zone `pmxcli`, VNet `pmxcli0`, subnet `172.30.0.0/24`) is honored throughout.

| Area | Verbs recovered | Live strategy | Status |
|---|---|---|---|
| QEMU guest agent | `pmx qemu agent exec\|exec-status\|file-read\|file-write\|set-user-password` | Bake `qemu-guest-agent` into a cloud image with `virt-customize`, provision the throwaway VM host-side (the API token cannot `import-from` arbitrary paths), then drive the agent over the CLI; the password is piped via stdin | Shipped |
| Node disks | `pmx node disks init-gpt`, `create\|delete directory\|lvm\|lvmthin\|zfs` | Run against a single spare NVMe pinned by serial and hard-asserted unused (via `disks list` plus a host-side `wipefs`/holders/`zpool` probe); each create is paired with a `delete --cleanup-disks`, and a finally block zaps residue over root SSH | Shipped |
| Remote-storage scans | `pmx node scan cifs\|iscsi\|nfs\|pbs` | Point cifs and iscsi at the node's own services; answer nfs with a temporary `nfs-kernel-server` export (purged afterward) and pbs with a host-local HTTPS stub whose self-signed cert is pinned by fingerprint | Shipped |
| Two-factor authentication | `pmx access tfa create\|set\|delete` | Drive a password-login ticket session for a throwaway realm user with offline RFC 6238 TOTP (the `/access/tfa` endpoints reject API-token auth) | Shipped |
| Storage import metadata | `pmx storage import-metadata` | Stage a crafted OVF and backing VMDK in the node's import directory over root SSH, read the metadata, then remove the fixture | Shipped |
| SDN DNS | `pmx sdn dns create\|get\|set\|delete` | Satisfy the PowerDNS connectivity check with a host-local HTTP stub; the full CRUD is staged and never applied to the running SDN | Shipped |
| Host services and network | `pmx node services reload\|restart\|stop\|start`, `pmx node network create\|set\|delete\|revert`, `pmx node subscription delete\|update`, `pmx node oci pull` | Cycle a benign non-control-plane service (chrony) and restore it; stage and revert network interface changes; run idempotent subscription and OCI operations | Shipped |
| Cluster and template setup | `pmx cluster mapping pci\|usb create\|get\|set\|delete`, `pmx cluster bulk suspend`, `pmx node startall\|stopall\|suspendall`, `pmx node hosts set`, `pmx qemu template` | Exercise against synthetic mapping hints and `pmx-cli`-owned guests, with teardown in every path | Shipped |

### Remaining deferrals

The 54 leaves that remain deferred are genuinely impossible to exercise on the shared single-node lab. Each is grouped below by why it cannot run; every leaf carries an accurate rationale in the matrix and is covered by unit tests of its argument contract and `--yes` guard.

| Category | Leaves | Why it stays deferred |
|---|---|---|
| Ceph cluster operations | 21 | The lab has no Ceph cluster, and these create, destroy, restart, or reconfigure Ceph daemons, OSDs, pools, and filesystems |
| Multi-node or second-cluster topology | 9 | A single-node lab has no peer node or second cluster for cross-node and cross-cluster migration, cluster join, node add and delete, `migrateall`, and `wakeonlan` |
| Environment-bound or no-op | 6 | Needs a configured job, key, or IPAM backend, or would discard all pending state (`apt repositories add\|enable`, `subscription set`, `replication run`, `sdn ipam set`, `sdn rollback`) |
| root@pam-only endpoints | 5 | The suite authenticates with an API token, which PVE forbids on these endpoints (`acme account create\|set\|delete`, `disks wipe`, `storage volume copy`) |
| External CA or live TLS | 5 | Contacts a real ACME CA or replaces the node's live API certificate (`cert acme delete\|order\|renew`, `cert custom delete\|upload`) |
| HA stack arm and disarm | 2 | Would disrupt every HA-managed resource on the shared lab |
| Host network or firewall cutover | 2 | Applying the change could sever the suite's own connection to the node (`network apply`, `firewall options set`) |
| No Proxmox Backup Server | 2 | The lab has no PBS storage to browse (`file-restore list\|download`) |
| Interactive terminals | 2 | `console` and `shell` open a live SSH or VNC session that cannot be driven head-less |

A recurring lesson from this pass: live de-risking before writing harness code caught five verbs that read-only inspection had mislabelled recoverable — local-node `wakeonlan`, the three `acme account` verbs, and `disks wipe` all turned out to be rejected by API-token auth or single-node topology, and were reclassified as blocked with corrected rationales.

## Delivery Standard

Each feature ships only after it:

- builds cleanly (`go build ./...`),

- passes end-to-end tests against a live lab environment using an isolated, namespaced set of resources that never disrupt other workloads,

- and clears quality review and test-coverage review.

Destructive operations require explicit confirmation, and long-running operations support asynchronous task handling via the standard task UPID flow.
