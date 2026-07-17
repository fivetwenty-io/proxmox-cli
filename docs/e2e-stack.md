# Nested e2e stack (PBS + PDM, optional PVE)

`scripts/stack` stands up and tears down the guests the full e2e suite needs
on top of the lab PVE cluster, so the opt-in `pbs` and `pdm` command trees in
`scripts/e2e` run against live servers instead of being skipped.

## What it provisions

For each product enabled in `config/stack.toml`:

- a guest VM full-cloned from a Debian cloud-init template on the lab cluster,
  configured with a static IP from the config and root SSH via your public key

- the product installed inside the guest from the Proxmox apt repository
  (`pbs-no-subscription`, `pdm-test`, or `pve-no-subscription` — the component
  is configurable per product)

- an API token (`root@pam!e2e` by default) with an admin role on `/`,
  regenerated on every `up` so the secret is always known

- a `pmx` context per product (`pbs-e2e`, `pdm-e2e`, ...), validated live with
  `pmx context validate --connect`

PBS guests additionally get a datastore named `e2e` at `/srv/pbs-e2e`, so
datastore-scoped commands have a target.

The nested PVE product is optional and disabled by default — the lab cluster
itself is already the PVE target for the sweep. Enable it only when a fully
isolated stack is needed; note it reboots the guest into the PVE kernel during
provisioning.

## Workflow

```bash
scripts/stack init      # write a commented config/stack.toml, then edit it
scripts/stack up        # idempotent: converges guests, tokens, contexts
scripts/stack status    # per-product state at a glance
scripts/stack e2e       # full sweep with --pbs-context/--pdm-context wired in
scripts/stack down      # destroy the guests, remove the contexts
```

Make targets wrap the same verbs: `make stack-up`, `make stack-down`,
`make stack-status`, and `make test-e2e-stack`.

`scripts/stack env` prints `PMX_E2E_PBS_CONTEXT` / `PMX_E2E_PDM_CONTEXT`
exports for running `scripts/e2e` directly:

```bash
eval "$(scripts/stack env)"
scripts/e2e
```

## Requirements

- a working `lab` context (or whatever `[lab].context` names) against the PVE
  cluster that hosts the stack guests

- a Debian cloud-init template on that cluster (e.g. a
  `debian-13-genericcloud` image imported as a template); its VMID goes in
  `[lab].template`

- static guest IPs that fit the lab address plan, reachable from the machine
  running the script (provisioning connects over SSH as root)

Creating the template is a one-time step on the target node, e.g.:

```bash
wget https://cloud.debian.org/images/cloud/trixie/latest/debian-13-genericcloud-amd64.qcow2
qm create 9002 --name debian-13-template --ostype l26 --memory 2048 --cores 2 \
  --net0 virtio,bridge=vmbr0 --scsihw virtio-scsi-single --serial0 socket --vga serial0
qm set 9002 --scsi0 <storage>:0,import-from=$PWD/debian-13-genericcloud-amd64.qcow2,discard=on
qm set 9002 --ide2 <storage>:cloudinit --boot order=scsi0
qm disk resize 9002 scsi0 8G
qm template 9002
```

## Notes

- Every phase is idempotent. Re-running `up` skips guests that exist,
  re-applies cloud-init config, and re-generates tokens (secrets are only
  visible at creation, so convergence means regeneration).

- `config/stack.toml` is gitignored: it holds lab addresses and context
  names. `scripts/stack init` writes a commented example to start from.

- PDM has no user/token CLI (`proxmox-datacenter-manager-admin` only covers
  acme, remotes, and reports), so the stack provisions the PDM token through
  the localhost API. That flow needs PAM ticket auth, so `up` sets a random
  throwaway root password inside the PDM guest; key-based SSH remains the
  access path.

- Several PBS and PDM endpoints (PBS `GET /nodes` and the tape scans, PDM
  `GET /nodes` and the remote inventories) accept only ticket auth and 403
  any API token regardless of its ACLs. The e2e trees record those checks as
  skips (`skip_on`) under the stack's token contexts rather than failures.

- After `up`, the PDM instance has no remotes configured. Add the lab cluster
  as a remote (`pmx pdm remote ...` against the `pdm-e2e` context) if remote
  -dependent checks should exercise more than the empty-inventory paths.
