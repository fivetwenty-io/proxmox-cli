# pmx: Our Whole Proxmox Fleet From One Terminal

A read-aloud talk script with a live demo. Target length is 20–25 minutes plus questions. The audience is Proxmox VE and PBS users who live in `qm`, `pct`, `pvesh`, and the web UI today.

Everything in this script is real, shipping behavior. The goal is to land the first "wow" inside the first two minutes, then keep stacking them until people are installing it during Q&A.

---

## Pre-flight checklist

Do these before anyone is watching. A live demo that fumbles auth in minute one kills the talk.

- [ ] Contexts already configured and validated: at least one PVE cluster (call it `lab`) and one PBS server (call it `backup`). Run `pmx context validate --connect` beforehand — it probes each host live and confirms reachability, product, and auth in one table.

- [ ] Venue variables in `demo-cheat-sheet.md` exported (hosts, username, token id, guest, node, datastore) so every block copy-pastes unchanged.

- [ ] A running VM with a memorable name (this script uses `web-01`) and a stopped VM ready to start on stage.

- [ ] `pve`, `pbs`, and `pdm` symlinks installed next to `pmx` (`make install` creates all three).

- [ ] Optional: a Proxmox Datacenter Manager instance with a context (call it `dcmgr`), needed only for the PDM segment. Skip that section cleanly without one.

- [ ] Optional: if showing the `pmx lab` segment, the demo cluster needs SDN enabled and a ZFS pool for lab storage (the config defaults assume `tank`). Pick a throwaway lab name, dry-run `pmx lab create` beforehand, and plan to `pmx lab destroy --yes --purge` afterward.

- [ ] `jq` installed.

- [ ] Terminal font large, prompt short, `PMX_OUTPUT` unset so tables render by default.

- [ ] Do a full dry run of every command in `demo-cheat-sheet.md` against the demo cluster the same day.

Known constraints — respect these on stage:

- `pmx auth login`, `auth refresh`, and `auth whoami` work with all products (PVE, PBS, PDM). The one wrinkle: `--otp` is PVE-only. PBS and PDM take `--tfa-challenge` for the second factor. Token-based contexts (`pmx auth set-token`) and `pmx auth status` are also worth showing for variety.

- Tab completion covers node names, context names, and `--product` values, but does not complete VMIDs or guest names. Don't showboat completion on a guest name.

- PVE commands refuse a PBS context and vice versa. That's a safety feature: narrate it as one if we trip it; the error even names the persona or context to use instead.

- `pmx lab` lives only under the `pmx` name. It is not hoisted onto the `pve` persona root. Typing `pve lab` on stage will fail, so always say and type `pmx lab`.

- The project is pre-1.0. Say so plainly in the closing; it builds trust.

---

## 0:00 — Cold open: the pain (say this before touching the keyboard)

> Quick show of hands: who's got a terminal SSH'd into a Proxmox node right now?
>
> Here's how we all manage Proxmox today. We SSH into a node. Then we have to remember *which* node, because `qm` only sees the guests on the box we're standing on. VMs are `qm`. Containers are `pct`. Storage is `pvesm`. Everything else is `pvesh`, with a path we half-remember. Backups run through a different product with a different tool, and now Datacenter Manager is a third product with a third UI. Run more than one cluster? Multiply all of that by the number of clusters.
>
> The web UI is great, until we want to do the same thing to twenty VMs, or put it in a script, or check something from CI.
>
> Let's try something different: one binary, on our laptop, that speaks the Proxmox API. Both products, every cluster, no SSH required.

## 1:00 — The first wow: start a VM by name, from our laptop

Type this. Nothing else. Let the silence do the work while it runs.

```console
$ pve qemu start web-01
```

> That's it. Notice what we didn't do. We didn't SSH anywhere. We didn't look up a VMID. We didn't even tell it which node the VM lives on. It asked the cluster and resolved the name and the node itself. It also waited: it blocked until the Proxmox task actually finished. When our prompt comes back, the VM is already running. If we're scripting and want fire-and-forget instead, `--async` hands us the task UPID immediately.

Then show the fleet view:

```console
$ pve qemu list
```

> Every VM, every node, one table, from our laptop. Same for containers with `pve lxc list`, same for nodes with `pve node list`.

## 3:00 — One binary, four names, any number of clusters

> The tool is called `pmx`, and it has a party trick: it looks at the name we called it by. Called as `pmx`, we get everything: PVE under `pmx pve`, Backup Server under `pmx pbs`, Datacenter Manager under `pmx pdm`. But `pve`, `pbs`, and `pdm` are just symlinks to the same binary. Call it as `pve`, and the VE commands move to the top level. Muscle memory stays short: `pve qemu start`, `pbs datastore ls`.

```console
$ ls -la $(which pve) $(which pbs) $(which pdm)
$ pve --help | head -20
$ pbs --help | head -20
```

> Now, clusters. Everything runs against a named *context* (host, credentials, TLS settings) stored in one config file on our machine. Watch us move between a lab cluster and production:

```console
$ pmx ctx ls
$ pmx context validate --connect
$ pmx ctx select prod
$ pve qemu list
$ pmx ctx select -
```

> That `validate --connect` line is our morning coffee command. It probes every context live and gives us reachability, actual product, and auth in one table. That way, we know the whole fleet answers before we need it to. `ctx select -` toggles back to the previous context, exactly like `cd -`. And for one-off commands there's a `-c` flag, so a single script can talk to five clusters without ever "switching" anything. Context names tab-complete, so switching takes a few keystrokes.
>
> One more thing contexts buy us: safety. A context knows whether it's a PVE cluster, a PBS server, or a Datacenter Manager. The CLI refuses to run VE commands against a backup server, or the reverse. We can't fat-finger a prune command into the wrong product. And when we do trip the guard, the error tells us which persona or context we probably meant. It even catches the subtler mistake: point a context at the wrong machine, and the connection error notices we've hit another product's default port. 8006, 8007, and 8443 are a fingerprint.

## 6:00 — Built for scripting, not screenshots

> Every command renders as a table for humans, or as JSON and YAML with real types for machines. Not scraped text: the actual API response.

```console
$ pve node list -o json | jq -r '.[].node'
$ pve qemu list -o json | jq -r '.[] | select(.status=="stopped") | .name'
```

> Exit codes are semantic, which our shell scripts will love: 0 is success, 3 is "couldn't connect", 4 is "auth failed", 5 is "not found", 6 is "locked or conflict". We can branch on *why* something failed, not just that it did.
>
> Long-running operations are first-class. Everything that creates a Proxmox task blocks until the task completes, or we add `--async` and get the UPID back instantly. There's a `task wait` that blocks on any UPID, so we can kick off work across the cluster, then wait for all of it.

```console
$ pve qemu shutdown web-01 --async
$ pve task list
```

> And when the typed commands don't cover some brand-new endpoint, there's an escape hatch: raw API access with the same auth, same context, same output formats:

```console
$ pmx api get /cluster/resources -o json | jq 'length'
```

## 10:00 — Things the web UI makes tedious

Pick two of these three depending on the audience. The security audit is the strongest general-purpose wow.

### Cluster-wide security audit

> Here's our favorite. How do we answer the question, "which of our VMs have no firewall, no TPM, no Secure Boot, and an open door?" Today, we click through every single VM in the UI. Here:

```console
$ pve qemu security list
```

> One table, whole cluster: protection flag, Secure Boot, TPM state, confidential-computing status, guest agent, per-NIC firewall. The risky ones get flagged. There's a matching `pve lxc security` tree for container capabilities, including presets for hardening. There is no stock single-command equivalent to this anywhere in `qm`, `pvesh`, or the UI.

### Run commands inside guests — no SSH to the guest

```console
$ pve qemu exec web-01 -- uptime
```

> That went laptop → API → node → QEMU guest agent → inside the VM. The guest doesn't need sshd, and we never left our terminal.

### The cloud-init snippet workaround

> Anyone who automates cloud-init on Proxmox knows the API famously can't upload snippets. That's Bugzilla #2208, open for years. `pmx` just… handles it:

```console
$ pve storage upload local --content snippets --file ./user-data.yaml --node pve1
```

> It detects the gap and streams the file over SSH for us, using the same node resolution. Honest plumbing where the API falls short. And speaking of SSH: when we genuinely need a node shell, `pmx ssh <node>` and `pmx rsync` resolve the node's address from our context. We never keep a hosts file for our clusters again.

```console
$ pmx ssh pve1 -- uptime
```

## 13:00 — One command, a whole lab (optional — needs SDN and a ZFS pool on the demo cluster)

> Here's the newest trick, and it's the one for anyone who runs a shared cluster. We all know the ritual when a new teammate needs their own sandbox: carve a vnet and a subnet, create a VM, make a resource pool, grant them access, set a quota so they can't eat the disk. Six visits to the web UI, and we'd do it slightly differently every time. `pmx lab` turns that whole ritual into a YAML block and one verb.

```console
$ pmx lab config add demo --vxlan-tag 5099 --cidr 10.99.0.0/16
$ pmx lab create demo --node pve1 --dry-run
$ pmx lab create demo --node pve1
$ pmx lab net apply demo
$ pmx lab access grant demo demo@pve
$ pmx lab status demo
```

> A lab is a self-contained slice of the cluster: its own SDN vnet and subnet inside a shared VXLAN zone, a VM, storage, a resource pool, an access grant, and a ZFS quota. Labs are config-driven: an inline map in the config file, or one small YAML file per lab in a `labs.d/` directory. That way each lab can live in its own reviewable, committable file. `config add` writes a new file and never rewrites our main config, so hand-written comments survive.
>
> Watch what `create` did. It's an ordered, idempotent plan: shared zone, then vnet and subnet, then storage, then pool, then the VM. It queries live state first and skips anything that already exists, so re-running it against a half-built lab is safe. Run it with `--dry-run` first, like we just did, and it shows us the plan without touching anything. The SDN pieces are staged, not live, until `net apply`, which always shows us the pending changeset before committing it. No surprise network changes.
>
> Two more details worth trusting. `access grant` creates the pool, the user, and the role if any are missing. The bootstrap password for a new user comes only from a config file that must be mode 0600, and it's redacted from every output format. And the quota: Proxmox has no API for ZFS dataset properties, so `pmx lab quota set` is honest about it and runs `zfs set refquota` over SSH.
>
> It also checks the network math for us. `create` and `config add` reject an address plan whose management addresses fall outside the lab's CIDR. And `status` reads each guest interface through the guest agent and warns when one carries a narrower prefix than the lab. That mismatch is the classic foot-gun where pings work but TCP silently times out.
>
> A tool that provisions and destroys environments is exactly the tool that will one day get pointed at the wrong thing. So every mutating lab verb refuses to act if the lab's VMID or any of its derived names collide with a protected production resource. And it re-checks the moment it learns the real VMID. Teardown is one line: `pmx lab destroy demo --yes`, with `--purge` to take the pool and storage definition with it.

## 15:00 — Trust, secrets, and the guardrails

> A tool that can delete VMs from our laptop had better be paranoid. Let's walk through the safety story, because that's where a lot of the engineering went.

- TLS
  System CA trust by default. For homelabs with self-signed certs there's opt-in trust-on-first-use pinning: the SSH `known_hosts` model, per context. It fails closed when there's no human at the terminal to approve a fingerprint.

- Secrets
  API token secrets resolve from environment variables, from the macOS keychain, or as literals. If we inline a literal, it warns us. The config file is written `0600`, atomically.

- Destructive commands
  Guarded by `--yes`. Config edits use the API's digest-based optimistic concurrency, so two admins can't silently clobber each other, and a config edit that won't parse rolls itself back.

- Audit trail
  Every invocation logs structured JSONL to `~/.pmx/logs/`, with tokens, tickets, and passwords redacted. We get a local flight recorder of everything we did to the fleet.

Show the login flow once, quickly:

```console
$ pmx auth login --username root@pam
$ pmx auth status
```

> Password logins get a proper session ticket with expiry tracked in the context. TOTP and OpenID Connect logins are supported too, and the same login flow works against PBS and Datacenter Manager contexts, not just PVE. `auth status` shows us the whole picture without touching the network: endpoint, product, auth type, where the secret comes from, and whether the ticket is still valid. For automation we'd use an API token on the context instead. No interactive step at all.

## 18:00 — The backup server is not a second-class citizen

> Everything we've seen (contexts, output formats, exit codes, JSON) works identically against Proxmox Backup Server. Same binary, `pbs` persona:

```console
$ pmx ctx select backup
$ pmx auth whoami
$ pbs datastore ls
$ pbs snapshot ls --store tank
$ pbs verify job ls
```

> Notice `auth whoami` worked there too. It asked the backup server who these credentials are and what they can touch, same command as on PVE. Datastores, snapshots, groups, prune and verify and sync jobs, with one-shot `run` commands so a cron job is one line. When's the last time we drove PBS and PVE from the same tool, with the same muscle memory?

## 20:00 — Datacenter Manager, already covered (optional — skip if we have no PDM instance)

> Proxmox's newest product is Datacenter Manager, the one pane of glass over all our clusters. It's early software, and `pmx` already speaks it: same binary, `pdm` persona, `product: pdm` context.

```console
$ pmx ctx select dcmgr
$ pdm remote ls
$ pdm resource ls --resource-type qemu
```

> That resource list is every VM across every remote PDM knows about: the aggregated view, from our terminal. And here's the part that should raise an eyebrow. We can drive the *remotes* through it. This next command talks to our laptop, which talks to the Datacenter Manager, which proxies to a PVE cluster it manages:

```console
$ pdm pve qemu ls pve-remote-1
```

> Remotes, aggregated resources, SDN, Ceph views, the subscription key pool, users and ACLs and realms, even automated-installation answers: the whole PDM API surface has typed commands. And auth works exactly like the other two products, interactive login or an API token on the context.

## 22:00 — Getting it, and the honest fine print

> It's a single static Go binary, and installing it is one line.

```console
$ brew install --cask fivetwenty-io/tap/pmx
```

> That cask brings the `pve`, `pbs`, and `pdm` symlinks, the man pages, and shell completions along with it. The macOS binaries are signed and notarized, so Gatekeeper accepts them on first launch. No right-click ritual. Not on Homebrew? The same release pipeline publishes archives for macOS, Linux, FreeBSD, and Windows, plus deb and rpm packages, all carrying completions for every persona. There's also `go install`, and building from source: `make install` drops `pmx` in place with the symlinks, man pages, and bash, zsh, and fish completions under `/usr/local`.

```console
$ go install github.com/fivetwenty-io/proxmox-cli/cmd/pmx@latest
```

> And it's documented like a real Unix citizen. Every command in all four personas has a man page and a `--help` with a long description and copy-paste examples: thousands of pages, generated from the same source as the CLI itself, so they can't drift.
>
> The honest fine print, because we'd find it anyway:
>
> It's pre-1.0. The surface is stable and it's what we use daily, but expect polish releases. Interactive `auth login` works with all products (PVE, PBS, Datacenter Manager). We can also use API tokens, which is what we'd want for automation. Keychain storage is macOS-only so far, so Linux folks use environment variables. Datacenter Manager support is the newest part of the tree, so it's the least battle-worn. And a handful of operations (container capability tuning, that snippet upload, and lab ZFS quotas) go over SSH because Proxmox exposes no API for them. The CLI is upfront about it when it happens.
>
> Under the hood it sits on a Go client generated from Proxmox's own `apidoc.json`, covering the full documented API. The repo carries a test suite nearly the size of the source, including a lifecycle harness that provisions a throwaway VM and drives it through every mutating command against a live cluster.

## 24:00 — Close

> Here's the pitch in one sentence: `pmx` is kubectl for Proxmox. One typed, scriptable binary drives our whole Proxmox fleet (VE, Backup Server, and Datacenter Manager) from our laptop, over the API. No more SSHing node-by-node into `qm`, `pct`, and `pvesh`.
>
> Install it, point a context at our lab, and the first time we type `pve qemu start <name>` from the couch, we'll get it.

---

## Likely questions, with answers

- "Does it replace `qm`/`pct`/`pvesh`?"
  For day-to-day driving, yes, from anywhere, not just on-node. The raw `pmx api` passthrough covers anything the typed commands don't. Node-local emergencies where the API is down are still SSH territory, and `pmx ssh` gets us there.

- "What about permissions? We don't run everything as root@pam."
  It's a pure API client, so PVE's ACLs apply exactly as they do in the UI. Make a token with the right role and put it on the context.

- "Multiple clusters at once?"
  Contexts are the unit. `-c <context>` per command, `PMX_CONTEXT` per shell, `ctx select` per session.

- "Windows?"
  Release archives include Windows; persona dispatch handles `.exe` correctly.

- "Is the API coverage really complete?"
  The underlying generated client matches Proxmox's `apidoc.json` endpoint-for-endpoint, and the CLI surfaces the full user-facing set. Don't quote a test-coverage percentage; say "extensive live e2e plus a full-lifecycle harness" and offer to show `docs/api-coverage-audit.md`.

- "Does it support Proxmox Datacenter Manager?"
  Yes. A `product: pdm` context enables the full `pmx pdm` tree: remotes, aggregated resources, SDN and Ceph views, subscription pool, access control, realms, automated installs, and proxied operations against managed PVE and PBS remotes. Interactive login and API tokens both work, same as the other products.

- "Can we use `pmx lab` for our team's sandboxes?"
  That's exactly what it's for: one YAML file per person under `labs.d/`, then `pmx lab create` provisions the vnet, subnet, VM, storage, pool, access grant, and quota idempotently. Every mutating verb takes `--dry-run`, and a built-in guard refuses anything that collides with protected production resources. It's the newest subtree, so kick the tires in a lab cluster first.

- "Why not just curl the API?"
  We can, for one call. `pmx` gives us auth ticket handling, TLS pinning, task blocking, name resolution, typed output, exit codes, and audit logs on every call. That's the difference between an API and a tool.
