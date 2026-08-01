# Demo cheat sheet

Copy-paste companion to `pmx-fleet-from-your-laptop.md`. Every block matches a talk section. Edit the venue variables below, then dry-run the whole file top to bottom against the demo cluster on the day of the talk.

Assumptions

- Contexts `lab` (PVE), `prod` (PVE), and `backup` (PBS) exist and validate. Optionally `dcmgr` (PDM) with at least one registered remote, needed only for the Datacenter Manager section.

- The guest named by `DEMO_GUEST` exists; it should be stopped before the talk starts so the opening command visibly starts it.

- The node named by `DEMO_NODE` exists in the active context.

- If demoing the `pmx lab` section: the demo cluster has SDN enabled and a ZFS pool for lab storage (`pmx lab config add` defaults to `tank`; override with `--pool` flags or in the lab file). `DEMO_LAB` is a throwaway name. Destroy it after the talk.

## Venue variables (edit these; everything else copy-pastes unchanged)

```sh
export DEMO_USERNAME="root@pam"
export DEMO_TOKEN_ID="automation"

export DEMO_PVE_HOST="pve.lab.example.com"        # `lab` context
export DEMO_PROD_HOST="pve.prod.example.com"      # `prod` context
export DEMO_PBS_HOST="pbs.lab.example.com"        # `backup` context
export DEMO_PDM_HOST="pdm.lab.example.com"        # `dcmgr` context (optional)

export DEMO_GUEST="web-01"          # VM the opener starts (stopped beforehand)
export DEMO_NODE="pve1"             # node for the ssh and upload demos
export DEMO_STORAGE="local"         # path-backed PVE storage with snippets enabled
export DEMO_STORE="tank"            # PBS datastore
export DEMO_REMOTE="pve-remote-1"   # PDM-managed PVE remote (optional)

export DEMO_LAB="demo"              # throwaway lab name for the pmx lab section (optional)
export DEMO_LAB_USER="demo@pve"     # pve-realm user granted access to the demo lab
export DEMO_LAB_VXLAN="5099"        # VXLAN tag not in use on the demo cluster
export DEMO_LAB_CIDR="10.99.0.0/16" # subnet not in use on the demo cluster

# Token secrets: export real values before the talk. The --secret flags below
# pass the ${...} references in single quotes on purpose — pmx stores the
# reference and resolves it at run time, so no secret lands in the config file.
export PMX_LAB_TOKEN="changeme"
export PMX_PROD_TOKEN="changeme"
export PMX_PBS_TOKEN="changeme"
export PMX_PDM_TOKEN="changeme"
```

## One-time setup (before the talk, not during)

```sh
pmx context add lab --host "$DEMO_PVE_HOST" --username "$DEMO_USERNAME" \
  --token-id "$DEMO_TOKEN_ID" --secret '${PMX_LAB_TOKEN}' --select
pmx context add prod --host "$DEMO_PROD_HOST" --username "$DEMO_USERNAME" \
  --token-id "$DEMO_TOKEN_ID" --secret '${PMX_PROD_TOKEN}'
pmx context add backup --host "$DEMO_PBS_HOST" --product pbs \
  --username "$DEMO_USERNAME" --token-id "$DEMO_TOKEN_ID" --secret '${PMX_PBS_TOKEN}'
# Optional, only if demoing the PDM section:
pmx context add dcmgr --host "$DEMO_PDM_HOST" --product pdm \
  --username "$DEMO_USERNAME" --token-id "$DEMO_TOKEN_ID" --secret '${PMX_PDM_TOKEN}'
pmx context validate --connect
pve qemu stop "$DEMO_GUEST"   # so the opener has something to start
```

## 1:00 — First wow

```sh
pve qemu start "$DEMO_GUEST"
pve qemu list
```

## 3:00 — Personas and contexts

```sh
ls -la "$(which pve)" "$(which pbs)" "$(which pdm)"
pve --help | head -20
pbs --help | head -20
pmx ctx ls
pmx context validate --connect
pmx ctx select prod
pve qemu list
pmx ctx select -
```

## 6:00 — Scripting

```sh
pve node list -o json | jq -r '.[].node'
pve qemu list -o json | jq -r '.[] | select(.status=="stopped") | .name'
pve qemu shutdown "$DEMO_GUEST" --async
pve task list
pmx api get /cluster/resources -o json | jq 'length'
pve qemu start "$DEMO_GUEST"   # restore state for later sections
```

## 10:00 — Web-UI-tedious tricks

```sh
pve qemu security list
pve qemu exec "$DEMO_GUEST" -- uptime
pve storage upload "$DEMO_STORAGE" --content snippets --file ./user-data.yaml --node "$DEMO_NODE"
pmx ssh "$DEMO_NODE" -- uptime
```

## 13:00 — One command, a whole lab (optional; needs SDN + ZFS pool)

```sh
pmx lab config add "$DEMO_LAB" --vxlan-tag "$DEMO_LAB_VXLAN" --cidr "$DEMO_LAB_CIDR"
pmx lab config show "$DEMO_LAB"
pmx lab create "$DEMO_LAB" --node "$DEMO_NODE" --dry-run
pmx lab create "$DEMO_LAB" --node "$DEMO_NODE"
pmx lab net apply "$DEMO_LAB"
pmx lab access grant "$DEMO_LAB" "$DEMO_LAB_USER"
pmx lab status "$DEMO_LAB"
```

After the talk (not on stage unless time allows: it stops and deletes the VM, and `--purge` removes the pool and storage definition too):

```sh
pmx lab destroy "$DEMO_LAB" --yes --purge
```

Notes for this block

- `pmx lab` exists only under the `pmx` name. `pve lab` is not a command.

- `create` stages the SDN pieces but does not commit them; `net apply` previews the pending changeset, then commits. Narrate the preview.

- `access grant` creates the pool, user, and role if missing; creating a missing user needs the top-level `default_user_password` key in a mode-0600 config.yml, or it refuses and says so. Either pre-create `DEMO_LAB_USER` or set that key beforehand.

- `pmx lab quota set "$DEMO_LAB" --refquota-gb 600` works only with SSH access to the node (it runs `zfs set refquota`; no PVE API exists). Skip it if the venue network blocks SSH.

- A `NETWORK_WARNING` row in `pmx lab status` is not an error: it means a guest interface carries a narrower prefix than the lab CIDR (the pings-work-but-TCP-times-out foot-gun). Narrate it as the CLI catching a real misconfiguration.

## 15:00 — Auth and guardrails

```sh
pmx auth login --username "$DEMO_USERNAME"
pmx auth status
ls ~/.pmx/logs/ | tail -3
```

`auth login`, `refresh`, `whoami`, and `--oidc` work against PVE, PBS, and PDM contexts alike; only `--otp` is PVE-specific (PBS and PDM take `--tfa-challenge`).

## 18:00 — PBS

```sh
pmx ctx select backup
pmx auth whoami
pbs datastore ls
pbs snapshot ls --store "$DEMO_STORE"
pbs verify job ls
```

## 20:00 — PDM (optional; skip cleanly if no PDM instance)

```sh
pmx ctx select dcmgr
pdm remote ls
pdm resource ls --resource-type qemu
pdm pve qemu ls "$DEMO_REMOTE"
pmx ctx select lab   # back to PVE for Q&A
```

If skipping this section, end the PBS block with `pmx ctx select lab` instead.

## 22:00 — Getting it

```sh
brew install --cask fivetwenty-io/tap/pmx
go install github.com/fivetwenty-io/proxmox-cli/cmd/pmx@latest
```

The tap (`fivetwenty-io/homebrew-tap`) and releases through v0.2.1 are live, so both lines are safe to show. As of v0.2.1 the macOS binaries are signed and notarized, so no Gatekeeper interruption is part of the pitch. Don't run a live install over conference wifi; show the command, mention it is how the demo machine got its copy, and move on.

## Do-not-demo list

- `pmx auth login --otp` against a PBS or PDM context: `--otp` is PVE-only; PBS and PDM use `--tfa-challenge` for the second factor. Plain `auth login` works on all three products.

- Tab completion on VMIDs or guest names: node names, context names, and `--product` values complete dynamically, but guests don't.

- Any claim of a test-coverage percentage. Say "extensive live e2e plus lifecycle harness" instead.

- A live `brew install` or release-archive download on stage. The tap and releases are live and fine to reference; running an install over conference wifi is the risk.

- `pve lab ...`: the lab subtree lives only under the `pmx` name; it is not hoisted onto the `pve` persona root.

- `pmx lab create` against a production context. The lab section runs only against the demo cluster. The built-in guard refuses collisions with protected production resources, but don't lean on it on stage.

## Recovery moves if something goes sideways

- Wrong-product error ("command requires a PVE context")
  Narrate it as the safety feature it is (the error itself names the persona or context to use instead), then `pmx ctx select <right-one>`.

- Connection refused against the wrong port
  If a context points at the wrong product, the error hints that we've hit another product's default port (8006 vs 8007 vs 8443). Read the hint out loud; it's a feature.

- Slow task on stage
  Ctrl-C the wait, then `pve task list` and `pve task wait <UPID>` to show the async model. It turns a stall into a feature demo.

- Name resolution ambiguity (same guest name on two nodes)
  Fall back to the VMID, and mention that the error names exactly which nodes collided.

- `pmx lab create` fails partway through
  Re-run the exact same command. Every step queries live state first and skips what already exists, so a partial build completes instead of duplicating. That idempotency is itself a talking point.
