"""qemu: VM inventory + per-VM read-only inspection.

Lifecycle (start/stop/reboot/reset/suspend/resume/delete/snapshot create) is
deferred. When implemented it must create VMs under the isolation contract:
pool `pve-cli`, tag `pve-cli`, NIC on the `pvecli` SDN — never the host bridge.
"""

from __future__ import annotations

from ..context import CmdResult, Ctx
from ..model import Isolation

NAME = "qemu"
DESCRIPTION = "Manage QEMU virtual machines"


def run(ctx: Ctx) -> None:
    # config/firewall-options describe: offline schema catalogs — no API call,
    # so they run even before node discovery.
    ctx.check("config describe", "qemu", "config", "describe")
    ctx.check("firewall options describe", "qemu", "firewall", "options", "describe")
    # security cpu-flags describe: offline mitigation-flag catalog — no API
    # call, so it runs alongside the other offline schema catalogs.
    ctx.check("security cpu-flags describe", "qemu", "security", "cpu-flags", "describe")

    n = ctx.node
    if not n:
        ctx.skip("list", "no node discovered")
        return

    def is_list(res: CmdResult) -> str | None:
        return None if isinstance(res.json(), list) else "expected a JSON array"

    lst = ctx.check("list", "qemu", "list", node=n, validate=is_list)

    vmid = None
    if lst.rc == 0:
        try:
            vmid = ctx.first(lst.json(), "vmid")
        except ValueError:
            vmid = None

    def has_status(res: CmdResult) -> str | None:
        data = res.json()
        if not isinstance(data, dict):
            return "expected a JSON object"
        missing = [k for k in ("status", "vmid") if k not in data]
        return f"status response missing keys: {missing}" if missing else None

    def has_ticket(res: CmdResult) -> str | None:
        # Validate the proxy ticket's shape only. The response carries a
        # short-lived secret; assert on key presence and never echo values.
        data = res.json()
        if not isinstance(data, dict):
            return "expected a JSON object"
        missing = [k for k in ("ticket", "port") if k not in data]
        return f"console response missing keys: {missing}" if missing else None

    if vmid is None:
        ctx.skip("status", "no VM on node")
        ctx.skip("config get", "no VM on node")
        ctx.skip("metrics", "no VM on node")
        ctx.skip("rrd", "no VM on node")
        ctx.skip("feature", "no VM on node")
        ctx.skip("snapshot list", "no VM on node")
        ctx.skip("snapshot show", "no VM on node")
        ctx.skip("migrate check", "no VM on node")
        ctx.skip("firewall rules list", "no VM on node")
        ctx.skip("firewall options get", "no VM on node")
        ctx.skip("firewall log", "no VM on node")
        ctx.skip("firewall refs", "no VM on node")
        ctx.skip("console vnc ticket", "no VM on node")
        ctx.skip("cloudinit pending", "no VM on node")
    else:
        vid = str(vmid)
        ctx.check("status", "qemu", "status", vid, node=n, validate=has_status)
        ctx.check("config get", "qemu", "config", "get", vid, node=n)

        # metrics: rrd timeseries for a guest; zero-row result is a valid list.
        ctx.check("metrics", "qemu", "metrics", vid, "--timeframe", "hour",
                  node=n, validate=is_list)

        # rrd: rrd PNG image reference; always returns a filename object.
        def has_filename(res: CmdResult) -> str | None:
            data = res.json()
            if not isinstance(data, dict):
                return "expected a JSON object"
            if "filename" not in data:
                return "rrd response missing 'filename' key"
            return None

        ctx.check("rrd", "qemu", "rrd", vid, "--ds", "cpu", "--timeframe", "hour",
                  node=n, validate=has_filename)

        # feature: whether the guest supports a named feature (clone is always safe).
        def has_feature(res: CmdResult) -> str | None:
            data = res.json()
            if not isinstance(data, dict):
                return "expected a JSON object"
            if "hasFeature" not in data:
                return "feature response missing 'hasFeature' key"
            return None

        ctx.check("feature", "qemu", "feature", vid, "--feature", "clone",
                  node=n, validate=has_feature)

        ctx.check("snapshot list", "qemu", "snapshot", "list", vid, node=n)

        # snapshot show: discover a real snapshot name, skip when none exists.
        snap_res = ctx.run("qemu", "snapshot", "list", vid, node=n)
        snap_name = None
        if snap_res.rc == 0:
            try:
                for entry in snap_res.json():
                    if isinstance(entry, dict):
                        nm = entry.get("name") or entry.get("snapname")
                        if nm and nm != "current":
                            snap_name = str(nm)
                            break
            except (ValueError, KeyError):
                snap_name = None
        if snap_name:
            ctx.check("snapshot show", "qemu", "snapshot", "show", vid, snap_name, node=n)
        else:
            ctx.skip("snapshot show", "no snapshot found on the discovered VM")

        # migrate check: pre-flight analysis (read-only). A single-node cluster
        # returns the feasibility object without an `allowed_nodes` list, so
        # assert only the object shape here.
        def is_migrate_check(res: CmdResult) -> str | None:
            return None if isinstance(res.json(), dict) else "expected a JSON object"

        ctx.check("migrate check", "qemu", "migrate", "check", vid,
                  node=n, validate=is_migrate_check)

        # Firewall reads are non-mutating: safe against any existing VM.
        ctx.check("firewall rules list", "qemu", "firewall", "rules", "list", vid,
                  node=n, validate=is_list)
        ctx.check("firewall options get", "qemu", "firewall", "options", "get", vid, node=n)
        ctx.check("firewall log", "qemu", "firewall", "log", vid, node=n)
        ctx.check("firewall refs", "qemu", "firewall", "refs", vid, node=n, validate=is_list)
        # Requesting a VNC proxy ticket is non-disruptive — it spawns an
        # ephemeral proxy the same way the web GUI does and changes no VM state.
        ctx.check("console vnc ticket", "qemu", "console", vid, "--type", "vnc",
                  node=n, validate=has_ticket)
        # cloud-init pending reads the VM's current vs pending cloud-init config.
        # It is non-mutating and returns an array whether or not the VM carries a
        # cloud-init drive, so it is safe against any existing VM.
        ctx.check("cloudinit pending", "qemu", "cloudinit", "pending", vid,
                  node=n, validate=is_list)

        # security posture reads: all API-only, gated on a discovered VM so the
        # posture blocks and the cluster audit table render against real data.
        # `show` is the layered per-VM posture, `list` the cluster-wide audit,
        # `agent show`/`secureboot show`/`tpm show`/`confidential show`/
        # `cpu-flags show`/`nic show` the per-facet reads. The mutating
        # counterparts (agent set, secureboot enable, tpm add/remove,
        # confidential set/clear, cpu-flags set, nic firewall, protection
        # enable/disable) are deferred below.
        def has_security_posture(res: CmdResult) -> str | None:
            data = res.json()
            if not isinstance(data, dict):
                return "expected a JSON object"
            missing = [k for k in ("vmid", "protection") if k not in data]
            return f"security posture missing keys: {missing}" if missing else None

        def has_agent_posture(res: CmdResult) -> str | None:
            data = res.json()
            if not isinstance(data, dict):
                return "expected a JSON object"
            return None if "enabled" in data else "agent posture missing 'enabled' key"

        def has_boot_posture(res: CmdResult) -> str | None:
            data = res.json()
            if not isinstance(data, dict):
                return "expected a JSON object"
            return None if "posture" in data else "boot posture missing 'posture' key"

        def has_tpm_posture(res: CmdResult) -> str | None:
            data = res.json()
            if not isinstance(data, dict):
                return "expected a JSON object"
            return None if "present" in data else "TPM posture missing 'present' key"

        def has_confidential_posture(res: CmdResult) -> str | None:
            data = res.json()
            if not isinstance(data, dict):
                return "expected a JSON object"
            return None if "platform" in data else "confidential posture missing 'platform' key"

        def has_cpuflags_posture(res: CmdResult) -> str | None:
            data = res.json()
            if not isinstance(data, dict):
                return "expected a JSON object"
            return None if "enabled" in data else "cpu-flags posture missing 'enabled' key"

        ctx.check("security show", "qemu", "security", "show", vid,
                  node=n, validate=has_security_posture)
        # list is a cluster resources scan plus one config read per VM; an
        # empty cluster still returns a valid (possibly empty) array.
        ctx.check("security list", "qemu", "security", "list", node=n, validate=is_list)
        ctx.check("security agent show", "qemu", "security", "agent", "show", vid,
                  node=n, validate=has_agent_posture)
        ctx.check("security secureboot show", "qemu", "security", "secureboot", "show", vid,
                  node=n, validate=has_boot_posture)
        ctx.check("security tpm show", "qemu", "security", "tpm", "show", vid,
                  node=n, validate=has_tpm_posture)
        ctx.check("security confidential show", "qemu", "security", "confidential", "show", vid,
                  node=n, validate=has_confidential_posture)
        ctx.check("security cpu-flags show", "qemu", "security", "cpu-flags", "show", vid,
                  node=n, validate=has_cpuflags_posture)
        ctx.check("security nic show", "qemu", "security", "nic", "show", vid,
                  node=n, validate=is_list)

    # QEMU capability queries are node-scoped and always safe: they report the
    # CPU models, CPU flags, and machine types the node's QEMU binary can offer
    # guests, independent of whether any VM exists.
    ctx.check("cpu list", "qemu", "cpu", "list", node=n, validate=is_list)
    ctx.check("cpu-flags", "qemu", "cpu-flags", node=n, validate=is_list)
    ctx.check("machine list", "qemu", "machine", "list", node=n, validate=is_list)

    # migrate capabilities: node-scoped QEMU migration feature report,
    # independent of whether any VM exists (mirrors `pve node capabilities
    # qemu migration`).
    def has_migrate_capabilities(res: CmdResult) -> str | None:
        data = res.json()
        if not isinstance(data, dict):
            return "expected a JSON object"
        return None if "has-dbus-vmstate" in data else "capabilities response missing 'has-dbus-vmstate' key"

    ctx.check("migrate capabilities", "qemu", "migrate", "capabilities",
              node=n, validate=has_migrate_capabilities)

    # Verify clone, migrate, disk, and firewall help text parses (commands are wired).
    ctx.check("clone --help", "qemu", "clone", "--help", fmt="")
    ctx.check("migrate --help", "qemu", "migrate", "--help", fmt="")
    ctx.check("disk resize --help", "qemu", "disk", "resize", "--help", fmt="")
    ctx.check("disk move --help", "qemu", "disk", "move", "--help", fmt="")
    ctx.check("disk unlink --help", "qemu", "disk", "unlink", "--help", fmt="")
    ctx.check("firewall rules create --help", "qemu", "firewall", "rules", "create", "--help", fmt="")
    ctx.check("firewall ipset add --help", "qemu", "firewall", "ipset", "add", "--help", fmt="")
    ctx.check("firewall alias create --help", "qemu", "firewall", "alias", "create", "--help", fmt="")
    ctx.check("firewall options set --help", "qemu", "firewall", "options", "set", "--help", fmt="")
    ctx.check("console --help", "qemu", "console", "--help", fmt="")
    ctx.check("agent --help", "qemu", "agent", "--help", fmt="")
    ctx.check("cloudinit dump --help", "qemu", "cloudinit", "dump", "--help", fmt="")
    ctx.check("cloudinit update --help", "qemu", "cloudinit", "update", "--help", fmt="")
    ctx.check("template --help", "qemu", "template", "--help", fmt="")

    # The mutating verbs below are not run by the read-only sweep, but are all
    # exercised live on a purpose-built isolated VM by the mutate phase
    # (`scripts/e2e --mutate` / `scripts/lifecycle`). `reboot` is the sole
    # exception: a diskless VM has no guest OS to ACPI-reboot, so it is covered
    # on the LXC side instead (qemu `reset` covers the in-place restart path).
    ctx.defer(
        "create",
        "creates a VM — covered live by `e2e --mutate`",
        f"pve qemu create ... --pool {Isolation.POOL} --tags {Isolation.TAG}",
        isolation=True, live_covered=True,
    )
    ctx.defer("start/stop/shutdown/reset/suspend/resume",
              "changes VM power state — covered live by `e2e --mutate`",
              "pve qemu start <vmid> --node <node>", isolation=True, live_covered=True)
    ctx.defer("reboot", "graceful reboot needs a guest OS — covered on the lxc container",
              "pve qemu reboot <vmid> --node <node>", isolation=True, live_covered=True)
    ctx.defer("delete", "destroys a VM — covered live by `e2e --mutate`",
              "pve qemu delete <vmid> --node <node>", isolation=True, live_covered=True)
    ctx.defer("snapshot create/rollback/delete",
              "mutates VM snapshots — covered live by `e2e --mutate`",
              "pve qemu snapshot create <vmid> <name>", isolation=True, live_covered=True)
    ctx.defer(
        "clone",
        "clones a VM — covered live by `e2e --mutate`",
        f"pve qemu clone <vmid> --newid <id> --pool {Isolation.POOL} --name {Isolation.NAME_PREFIX}clone",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "migrate",
        "migrates a VM to another node — covered live by `e2e --mutate` on multi-node clusters",
        "pve qemu migrate <vmid> --target <node>",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "disk resize/move/unlink",
        "grows, relocates, and detaches VM disks — covered live by `e2e --mutate`",
        "pve qemu disk resize <vmid> --disk scsi0 --size +1G",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "firewall rules/ipset/alias create-delete + options set",
        "mutates a VM's firewall config — covered live by `e2e --mutate` on the isolated VM",
        "pve qemu firewall rules create <vmid> --type in --action ACCEPT --proto tcp --dport 22",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "console connect (websocket/spice viewer)",
        "opening the proxied console session needs an interactive viewer — the "
        "CLI only returns the ticket, which the read-only sweep validates",
        "pve qemu console <vmid> --type spice",
    )
    ctx.defer(
        "monitor",
        "sends a raw QEMU monitor command to a running VM — covered live by "
        "`e2e --mutate` (soft-step: info status, which cannot change VM state)",
        "pve qemu monitor <vmid> --command 'info status' --yes",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "sendkey",
        "injects a key event into a running VM's QEMU process (no guest OS "
        "needed) — covered live by `e2e --mutate` with a benign key (ret)",
        "pve qemu sendkey <vmid> --key ret",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "remote-migrate",
        "migrates a VM to a different Proxmox VE cluster — requires two live "
        "clusters with shared or compatible storage; no rollback without manual "
        "intervention; not exercised live",
        "pve qemu remote-migrate <vmid> --yes --target-endpoint https://remote:8006 "
        "--target-storage local-lvm --target-bridge vmbr0",
        isolation=False, live_covered=False,
    )
    ctx.defer(
        "agent <command>",
        "runs guest-agent verbs (ping/get-*/fstrim/...) — requires a running "
        "guest agent; ping is exercised live (soft) on the isolated VM by `e2e --mutate`",
        "pve qemu agent <vmid> ping",
        isolation=True, live_covered=True,
    )
    # Parameterised guest-agent sub-commands. Each needs a guest running the
    # qemu-guest-agent daemon. The agent talks over a virtio-serial channel rather
    # than the guest network, so the only requirement is an image that *contains*
    # the daemon — the offline isolated network is irrelevant. The mutate phase
    # bakes qemu-guest-agent into a copy of a cached cloud image with virt-customize
    # over passwordless root SSH, imports it as the boot disk of an isolated
    # throwaway VM (`--agent 1`, no NIC), waits for the agent to answer `ping`,
    # exercises each verb, then destroys the VM and removes the baked image. They
    # skip gracefully if the host is unreachable or lacks the imaging tooling.
    ctx.defer(
        "agent exec",
        "runs an arbitrary command inside the guest — covered live by `e2e --mutate`, "
        "which boots an isolated VM from an image with qemu-guest-agent baked in and "
        "runs `agent exec id`",
        "pve qemu agent exec <vmid> --command 'id'",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "agent exec-status",
        "polls a guest exec PID — covered live by `e2e --mutate`, which polls the PID "
        "returned by the preceding `agent exec` on the baked-agent VM",
        "pve qemu agent exec-status <vmid> --pid <pid>",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "agent file-read",
        "reads a file from inside the guest — covered live by `e2e --mutate`, which "
        "reads back the file written by `agent file-write` on the baked-agent VM",
        "pve qemu agent file-read <vmid> --file /etc/hostname",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "agent file-write",
        "writes a file inside the guest filesystem — covered live by `e2e --mutate`, "
        "which writes a marker file on the baked-agent VM and reads it back",
        "pve qemu agent file-write <vmid> --file /tmp/probe --content x",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "agent set-user-password",
        "sets a guest user's password — secret-bearing (read from stdin, never "
        "echoed or logged), guarded by --yes; covered live by `e2e --mutate`, which "
        "sets root's password on the disposable baked-agent VM via a stdin-piped "
        "throwaway value",
        "pve qemu agent set-user-password <vmid> --username <user> --yes",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "cloudinit dump/update",
        "dumps/regenerates the cloud-init drive — exercised live (soft) on the "
        "isolated VM by `e2e --mutate` (skips when the VM has no cloud-init drive)",
        "pve qemu cloudinit update <vmid>",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "template",
        "converts a VM into a template — irreversible; covered live by `e2e "
        "--mutate` against a dedicated single-purpose isolated VM that is "
        "templated and then destroyed",
        "pve qemu template <vmid> --yes",
        isolation=True, live_covered=True,
    )
    ctx.defer(
        "ssh",
        "opens an interactive SSH tunnel into a guest — not automatable head-less, "
        "same class as `node shell`/`node console`; covered by unit tests",
        "pve qemu ssh <vmid>",
    )
    # `security` mutations are pure PVE-API read-modify-write operations (no
    # ssh, unlike lxc's capability caps), but the mutate phase does not yet
    # drive a purpose-built VM through them: each stays deferred here and is
    # covered by unit tests (internal/cli/qemu/security*_test.go).
    ctx.defer(
        "security agent set",
        "sets the guest-agent config option (agent=); not wired into the "
        "mutate phase; covered by unit tests",
        "pve qemu security agent set <vmid> --enabled --type virtio",
    )
    ctx.defer(
        "security secureboot enable",
        "switches firmware to OVMF and allocates an EFI vars disk; not wired "
        "into the mutate phase; covered by unit tests",
        "pve qemu security secureboot enable <vmid> --storage local-lvm",
    )
    ctx.defer(
        "security tpm add",
        "allocates a TPM state disk; not wired into the mutate phase; "
        "covered by unit tests",
        "pve qemu security tpm add <vmid> --storage local-lvm",
    )
    ctx.defer(
        "security tpm remove",
        "destroys the TPM state device and every key sealed in it; not "
        "wired into the mutate phase; covered by unit tests",
        "pve qemu security tpm remove <vmid> --force",
    )
    ctx.defer(
        "security confidential set",
        "configures AMD SEV / Intel TDX memory encryption, which needs "
        "matching host CPU/firmware support; not wired into the mutate "
        "phase; covered by unit tests",
        "pve qemu security confidential set <vmid> --sev std",
    )
    ctx.defer(
        "security confidential clear",
        "removes the confidential-computing configuration; not wired into "
        "the mutate phase; covered by unit tests",
        "pve qemu security confidential clear <vmid>",
    )
    ctx.defer(
        "security cpu-flags set",
        "edits the VM's security-relevant CPU flags; not wired into the "
        "mutate phase; covered by unit tests",
        "pve qemu security cpu-flags set <vmid> --enable spec-ctrl",
    )
    ctx.defer(
        "security nic firewall",
        "toggles per-NIC firewall coverage; not wired into the mutate "
        "phase; covered by unit tests",
        "pve qemu security nic firewall <vmid> --on --all",
    )
    ctx.defer(
        "security protection enable",
        "sets the VM protection flag; not wired into the mutate phase; "
        "covered by unit tests",
        "pve qemu security protection enable <vmid>",
    )
    ctx.defer(
        "security protection disable",
        "clears the VM protection flag; not wired into the mutate phase; "
        "covered by unit tests",
        "pve qemu security protection disable <vmid>",
    )
    # `firewall alias get` / `firewall ipset get-member` read a single
    # pre-existing entry by name. A fresh lab has none by default, and the
    # mutate phase's isolated alias/ipset create-list-update-delete lifecycle
    # (see `scripts/e2e_lib/lifecycle.py`) does not yet call these two reads,
    # so they cannot be driven head-less; covered by unit tests.
    ctx.defer(
        "firewall alias get",
        "reads a single firewall alias by name — needs a pre-existing alias; "
        "not wired into the mutate phase; covered by unit tests",
        "pve qemu firewall alias get <vmid> <name>",
    )
    ctx.defer(
        "firewall ipset get-member",
        "reads a single CIDR entry of an IP set — needs a pre-existing "
        "member; not wired into the mutate phase; covered by unit tests",
        "pve qemu firewall ipset get-member <vmid> <name> <cidr>",
    )
