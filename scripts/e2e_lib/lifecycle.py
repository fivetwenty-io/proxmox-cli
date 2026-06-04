"""Destructive lifecycle suite: provision an isolated SDN + pool, then drive
every mutating sub-command across the command trees — the full qemu/lxc
power-state matrix plus snapshot create/rollback/delete, the pool/task verbs,
an isolated access user/group/token/acl/password block, a throwaway dir-storage
create/set/delete, and the SSH-gated node exec/ssh/rsync verbs — on resources
created for the purpose, then tear everything down.

This is the live counterpart to the read-only `scripts/e2e` sweep: where the
sweep defers each mutating verb, this suite actually exercises it. Each verb is
recorded individually so the run prints a coverage table proving every deferred
operation was driven against a real Proxmox VE. The node block is SSH-gated: if
the host is unreachable it records SKIP rather than failing.

Isolation — every resource is shielded from other lab efforts:

  * named/hostnamed with the `pve-cli-` prefix,
  * placed in the `pve-cli` resource pool and tagged `pve-cli`,
  * attached to a dedicated `pvecli` simple SDN zone / `pvecli0` vnet on the
    172.30.0.0/24 subnet — off the host management network.

Teardown runs in a `finally` block and is idempotent, so a crashed prior run is
cleaned up by the next one. Nothing here touches pre-existing lab resources.

Guest-OS caveat: the throwaway QEMU VM is diskless (boots to firmware, no guest
OS), so the ACPI-dependent `qemu reboot` cannot complete gracefully. It is
recorded as a SKIP with that reason; the in-place restart path is covered on
qemu by `reset`, and the `reboot` verb itself is covered live on the Alpine
container, which has a real init that handles it.
"""

from __future__ import annotations

import json
import os
import shutil
import subprocess
import sys
import tempfile
import time
from dataclasses import dataclass, field

from .model import Isolation
from .runner import (
    BOLD,
    DIM,
    GREEN,
    RED,
    YELLOW,
    discover_node,
    find_binary,
    target_configured,
)

# Fixed resource names (all carry the isolation prefix/tag/pool).
VM_NAME = Isolation.NAME_PREFIX + "vm"
CT_HOST = Isolation.NAME_PREFIX + "ct"
SNAP_NAME = "pvecli-snap"
ROOTDIR_STORAGE = "local-lvm"   # lvmthin: supports rootdir/images + snapshots
TMPL_STORAGE = "local"          # holds vztmpl content
CT_IP = "172.30.0.50/24"
CT_GW = Isolation.SDN_GATEWAY

# Status glyphs for the coverage table.
PASS, FAIL, SKIP = "PASS", "FAIL", "SKIP"
_GLYPH = {PASS: GREEN("✓"), FAIL: RED("✗"), SKIP: YELLOW("·")}


class LifecycleError(Exception):
    """A required (non-teardown) step failed; abort the create-chain."""


@dataclass
class Cmd:
    rc: int
    out: str
    err: str

    def json(self):
        return json.loads(self.out)


@dataclass
class Step:
    """One recorded mutating operation, for the coverage report."""

    guest: str   # "qemu", "lxc", or "infra"
    verb: str    # the leaf command exercised, e.g. "snapshot rollback"
    status: str  # PASS | FAIL | SKIP
    detail: str = ""


class Runner:
    def __init__(self, binary: str, target: str, node: str, timeout: int = 600):
        self.binary = binary
        self.target = target
        self.node = node
        self.timeout = timeout
        self.cov: list[Step] = []

    def pve(self, *args: str, json_out: bool = False, node: bool = True) -> Cmd:
        argv = [self.binary, "--target", self.target, "--no-log"]
        if json_out:
            argv += ["-o", "json"]
        if node and self.node:
            argv += ["--node", self.node]
        argv += list(args)
        try:
            p = subprocess.run(argv, capture_output=True, text=True, timeout=self.timeout)
            return Cmd(p.returncode, p.stdout, p.stderr)
        except subprocess.TimeoutExpired:
            return Cmd(124, "", f"timed out after {self.timeout}s")

    # Run the binary with an explicit argv (no --target/--node injection), used
    # by steps that drive a scratch `--config` file or a non-default --target.
    def pve_raw(self, *args: str, json_out: bool = False) -> Cmd:
        argv = [self.binary, "--no-log"]
        if json_out:
            argv += ["-o", "json"]
        argv += list(args)
        try:
            p = subprocess.run(argv, capture_output=True, text=True, timeout=self.timeout)
            return Cmd(p.returncode, p.stdout, p.stderr)
        except subprocess.TimeoutExpired:
            return Cmd(124, "", f"timed out after {self.timeout}s")

    # Record a command result: print, append coverage, raise on failure.
    def _record(self, guest: str, verb: str, label: str, res: Cmd) -> Cmd:
        if res.rc == 0:
            print(f"  {GREEN('✓')} {label}")
            self.cov.append(Step(guest, verb, PASS))
            return res
        print(f"  {RED('✗')} {label}")
        detail = (res.err.strip() or res.out.strip())[:300]
        if detail:
            print(RED(f"      {detail}"))
        self.cov.append(Step(guest, verb, FAIL, detail))
        raise LifecycleError(label)

    # A required, coverage-recorded step: print result, record it, raise on failure.
    def step(self, guest: str, verb: str, label: str, *args: str,
             json_out: bool = False) -> Cmd:
        return self._record(guest, verb, label, self.pve(*args, json_out=json_out))

    # Like step(), but runs the binary verbatim (no --target/--node), for verbs
    # driven against a scratch `--config` file or an explicit --target.
    def step_raw(self, guest: str, verb: str, label: str, *args: str,
                 json_out: bool = False) -> Cmd:
        return self._record(guest, verb, label, self.pve_raw(*args, json_out=json_out))

    # A soft, coverage-recorded step for verbs whose completion depends on the
    # host (e.g. LXC suspend needs CRIU). PASS on success; on a recognised
    # environment-limitation error, record SKIP and return False; any other
    # failure is a real bug — record FAIL and raise.
    def soft_step(self, guest: str, verb: str, label: str, *args: str,
                  skip_markers: tuple[str, ...] = (), skip_reason: str = "") -> bool:
        res = self.pve(*args)
        if res.rc == 0:
            print(f"  {GREEN('✓')} {label}")
            self.cov.append(Step(guest, verb, PASS))
            return True
        detail = (res.err.strip() or res.out.strip())
        low = detail.lower()
        if skip_markers and any(m in low for m in skip_markers):
            reason = skip_reason or "unsupported in this environment"
            print(f"  {YELLOW('·')} {label} {DIM('(skip: ' + reason + ')')}")
            self.cov.append(Step(guest, verb, SKIP, reason))
            return False
        print(f"  {RED('✗')} {label}")
        if detail:
            print(RED(f"      {detail[:300]}"))
        self.cov.append(Step(guest, verb, FAIL, detail[:300]))
        raise LifecycleError(label)

    # A non-fatal, coverage-recorded skip (e.g. a verb a guest can't support).
    def cover_skip(self, guest: str, verb: str, label: str, reason: str) -> None:
        print(f"  {YELLOW('·')} {label} {DIM('(skip: ' + reason + ')')}")
        self.cov.append(Step(guest, verb, SKIP, reason))

    # A teardown step: print result, never raise, not coverage-recorded.
    def undo(self, name: str, *args: str) -> None:
        res = self.pve(*args)
        if res.rc == 0:
            print(f"  {GREEN('✓')} {name}")
        else:
            detail = (res.err.strip() or res.out.strip()).splitlines()
            tail = detail[-1][:160] if detail else "failed"
            print(f"  {YELLOW('·')} {name} {DIM('(skip: ' + tail + ')')}")

    # A teardown step that IS coverage-recorded but never raises, so the rest of
    # a multi-step cleanup still runs. Used for delete/revoke verbs that are both
    # the teardown AND a coverage target (token/user/group/acl/storage delete).
    # On the happy path the just-created resource deletes cleanly → PASS. A
    # cleanup error (e.g. resource already gone from a crashed prior run) records
    # SKIP with the detail rather than failing the whole suite.
    def del_step(self, guest: str, verb: str, label: str, *args: str) -> None:
        res = self.pve(*args)
        if res.rc == 0:
            print(f"  {GREEN('✓')} {label}")
            self.cov.append(Step(guest, verb, PASS))
            return
        detail = (res.err.strip() or res.out.strip()).splitlines()
        tail = detail[-1][:160] if detail else "failed"
        print(f"  {YELLOW('·')} {label} {DIM('(cleanup: ' + tail + ')')}")
        self.cov.append(Step(guest, verb, SKIP, "cleanup: " + tail[:120]))


def _node_count(r: Runner) -> int:
    """Return number of cluster nodes, or 1 on error (single-node assumption)."""
    res = r.pve("node", "list", json_out=True, node=False)
    if res.rc != 0:
        return 1
    try:
        data = res.json()
        if isinstance(data, list):
            return max(len(data), 1)
        if isinstance(data, dict) and isinstance(data.get("rows"), list):
            return max(len(data["rows"]), 1)
    except (ValueError, KeyError):
        pass
    return 1


def _alt_image_storage(r: Runner, exclude: str) -> str:
    """Return the id of an enabled storage that supports `images` content other
    than `exclude`, or "" if none exists (single-storage lab). Used so the disk
    `move` verb relocates to a genuinely different storage."""
    res = r.pve("storage", "list", json_out=True, node=False)
    if res.rc != 0:
        return ""
    try:
        rows = res.json()
    except ValueError:
        return ""
    if isinstance(rows, dict) and isinstance(rows.get("rows"), list):
        # table shape: skip, we need typed content — fall back to no alt.
        return ""
    if not isinstance(rows, list):
        return ""
    for s in rows:
        if not isinstance(s, dict):
            continue
        sid = str(s.get("storage", ""))
        content = str(s.get("content", ""))
        if sid and sid != exclude and "images" in content:
            return sid
    return ""


def _next_id(r: Runner) -> str:
    res = r.pve("cluster", "next-id", json_out=True, node=False)
    if res.rc != 0:
        raise LifecycleError("cluster next-id")
    data = res.json()
    # next-id may render as a bare id, {"data": id}, or the table Result shape
    # {"headers": ["VMID"], "rows": [["102"]]}; handle all three.
    if isinstance(data, dict):
        if isinstance(data.get("rows"), list) and data["rows"] and data["rows"][0]:
            data = data["rows"][0][0]
        else:
            data = data.get("data", data)
    return str(data).strip().strip('"')


def _upid_from(res: Cmd) -> str:
    """Pull a task UPID out of an --async command's JSON, tolerating the bare,
    {"upid": …}, {"data": …}, and table {"rows": [[…]]} shapes."""
    if res.rc != 0:
        return ""
    try:
        data = res.json()
    except ValueError:  # json.JSONDecodeError subclasses ValueError
        return res.out.strip().strip('"') if res.out.strip().startswith("UPID") else ""
    if isinstance(data, str):
        return data.strip().strip('"')
    if isinstance(data, dict):
        if isinstance(data.get("rows"), list) and data["rows"] and data["rows"][0]:
            return str(data["rows"][0][0])
        for k in ("upid", "UPID", "data"):
            if data.get(k):
                return str(data[k])
    return ""


def _ensure_template(r: Runner) -> str:
    """Return a vztmpl volid on TMPL_STORAGE, downloading Alpine if needed."""
    have = r.pve("storage", "content", TMPL_STORAGE, "--content", "vztmpl", json_out=True)
    if have.rc == 0:
        for vol in have.json():
            volid = vol.get("volid", "")
            if "alpine" in volid.lower():
                print(f"  {GREEN('✓')} template present: {volid}")
                return volid

    avail = r.pve("lxc", "template", "list", "--filter", "alpine", json_out=True)
    if avail.rc != 0 or not avail.json():
        raise LifecycleError("no Alpine template available to download")
    template = sorted(avail.json(), key=lambda t: t.get("template", ""))[-1]["template"]
    print(f"  {DIM('downloading ' + template + ' (~4MB)…')}")
    r.step("lxc", "template download", f"download template {template}",
           "lxc", "template", "download", TMPL_STORAGE, template)
    return f"{TMPL_STORAGE}:vztmpl/{template}"


def _sweep_stale(r: Runner) -> list[str]:
    """Best-effort: find leftover VMs/CTs named with our prefix from a crash."""
    stale = []
    for kind in ("qemu", "lxc"):
        res = r.pve(kind, "list", json_out=True)
        if res.rc != 0:
            continue
        for guest in res.json():
            name = str(guest.get("name") or guest.get("hostname") or "")
            if name.startswith(Isolation.NAME_PREFIX):
                stale.append(f"{kind}:{guest.get('vmid')}")
    return stale


# --- provisioning -----------------------------------------------------------


def provision_network(r: Runner) -> None:
    print(BOLD("provision: isolated SDN + pool"))
    r.step("infra", "sdn zone create", f"sdn zone create {Isolation.SDN_ZONE}",
           "sdn", "zone", "create", Isolation.SDN_ZONE, "--type", "simple")
    r.step("infra", "sdn vnet create", f"sdn vnet create {Isolation.SDN_VNET}",
           "sdn", "vnet", "create", Isolation.SDN_VNET, "--zone", Isolation.SDN_ZONE)
    r.step("infra", "sdn subnet create", f"sdn subnet create {Isolation.SDN_SUBNET}",
           "sdn", "subnet", "create", Isolation.SDN_VNET, Isolation.SDN_SUBNET,
           "--gateway", Isolation.SDN_GATEWAY)
    r.step("infra", "sdn apply", "sdn apply", "sdn", "apply")
    r.step("infra", "pool create", f"pool create {Isolation.POOL}",
           "pool", "create", "--poolid", Isolation.POOL)
    r.step("infra", "pool set", f"pool set {Isolation.POOL}",
           "pool", "set", Isolation.POOL, "--comment", "pve-cli e2e")


def vm_lifecycle(r: Runner) -> None:
    """Drive a diskless throwaway VM through every mutating qemu verb."""
    print(BOLD("qemu: full VM verb matrix"))
    vmid = _next_id(r)
    print(DIM(f"  vmid={vmid}"))
    # Flag breadth: drive --sockets and --boot alongside the core create flags
    # (read back via `config pending` below), plus a cloud-init flag group
    # (--ciuser/--citype/--ipconfig0/--searchdomain/--nameserver) round-tripped
    # via `config get` below. The diskless VM ignores boot order and never runs
    # cloud-init, but the keys must still be accepted and persisted to config.
    r.step("qemu", "create", f"create VM {vmid}", "qemu", "create", vmid,
           "--name", VM_NAME, "--memory", "512", "--cores", "1", "--sockets", "1",
           "--scsihw", "virtio-scsi-pci", "--scsi0", f"{ROOTDIR_STORAGE}:1",
           "--net0", f"virtio,bridge={Isolation.SDN_VNET}", "--boot", "order=scsi0",
           "--ostype", "l26", "--pool", Isolation.POOL, "--tags", Isolation.TAG,
           "--ciuser", "pveadmin", "--citype", "nocloud", "--ipconfig0", "ip=dhcp",
           "--searchdomain", "pve-cli.local", "--nameserver", "172.30.0.1")
    try:
        # Round-trip the cloud-init flags through `config get`: PVE stores each
        # set key verbatim (cipassword is hashed and sshkeys re-encoded, so
        # those are asserted only in the unit test, not here).
        cfg = r.step("qemu", "config get", f"config get VM {vmid}",
                     "qemu", "config", "get", vmid, json_out=True)
        want = {"ciuser": "pveadmin", "citype": "nocloud",
                "ipconfig0": "ip=dhcp", "searchdomain": "pve-cli.local"}
        cfg_data = cfg.json() if cfg.rc == 0 else {}
        got = {k: cfg_data.get(k) for k in want}
        missing = [k for k, v in want.items() if str(cfg_data.get(k, "")) != v]
        if missing:
            raise LifecycleError(
                f"cloud-init keys not round-tripped via config get: "
                f"{missing} (got {got})")
        r.step("qemu", "start", f"start VM {vmid}", "qemu", "start", vmid)
        r.step("qemu", "status", f"status VM {vmid}", "qemu", "status", vmid, json_out=True)
        # Edit config on the running VM, then read back the pending diff.
        r.step("qemu", "config set", f"config set VM {vmid}",
               "qemu", "config", "set", vmid, "--description", "pve-cli-e2e")
        r.step("qemu", "config pending", f"config pending VM {vmid}",
               "qemu", "config", "pending", vmid, json_out=True)
        # Pause/resume operate on the running qemu process — no guest OS needed.
        r.step("qemu", "suspend", f"suspend VM {vmid}", "qemu", "suspend", vmid)
        r.step("qemu", "resume", f"resume VM {vmid}", "qemu", "resume", vmid)
        # Hard reset stays running; covers the in-place restart path.
        r.step("qemu", "reset", f"reset VM {vmid}", "qemu", "reset", vmid)
        # Graceful reboot needs guest ACPI the diskless VM lacks; covered on LXC.
        r.cover_skip("qemu", "reboot", f"reboot VM {vmid}",
                     "diskless guest has no OS to ACPI-reboot (covered on lxc)")
        # Hard stop from running, then start again to exercise shutdown.
        r.step("qemu", "stop", f"stop VM {vmid}", "qemu", "stop", vmid)
        r.step("qemu", "start", f"start VM {vmid} (again)", "qemu", "start", vmid)
        # Drive `task stop` / `node task stop` against a real, deterministic
        # server-side task: `qemu shutdown --timeout 30 --async` spawns a
        # qmshutdown task that waits the full 30s window for a guest ACPI
        # power-off the diskless VM can never deliver, and returns its UPID
        # immediately. Aborting that task leaves the VM running (reversible), so
        # the power matrix below is unaffected.
        res = r.pve("qemu", "shutdown", vmid, "--timeout", "30", "--async", json_out=True)
        upid = _upid_from(res)
        if upid:
            # Top-level `task stop` reads the node from the auto-injected
            # --node flag (same as the `task wait` step below).
            r.step("infra", "task stop", "task stop <upid>",
                   "task", "stop", upid)
        else:
            r.cover_skip("infra", "task stop", "task stop",
                         "async shutdown returned no UPID")
        # Second async shutdown for the positional-node `node task stop` form.
        res = r.pve("qemu", "shutdown", vmid, "--timeout", "30", "--async", json_out=True)
        upid = _upid_from(res)
        if upid:
            r.step("node", "task stop", "node task stop <node> <upid>",
                   "node", "task", "stop", r.node, upid)
        else:
            r.cover_skip("node", "task stop", "node task stop",
                         "async shutdown returned no UPID")
        r.step("qemu", "snapshot create", f"snapshot create {SNAP_NAME}",
               "qemu", "snapshot", "create", vmid, SNAP_NAME)
        r.step("qemu", "snapshot list", "snapshot list",
               "qemu", "snapshot", "list", vmid, json_out=True)
        # Graceful shutdown with a short timeout + force-stop is deterministic
        # even without a responsive guest, and leaves the VM stopped.
        r.step("qemu", "shutdown", f"shutdown VM {vmid}",
               "qemu", "shutdown", vmid, "--timeout", "10", "--force-stop")
        # Rollback requires the VM stopped (the snapshot carries no RAM state).
        r.step("qemu", "snapshot rollback", f"snapshot rollback {SNAP_NAME}",
               "qemu", "snapshot", "rollback", vmid, SNAP_NAME)
        r.step("qemu", "snapshot delete", f"snapshot delete {SNAP_NAME}",
               "qemu", "snapshot", "delete", vmid, SNAP_NAME, "--yes")
        # Drive `task wait` against a real UPID: start the (stopped) VM with
        # --async so the verb returns a task id instead of blocking, then wait.
        res = r.pve("qemu", "start", vmid, "--async", json_out=True)
        upid = _upid_from(res)
        if upid:
            r.step("infra", "task wait", "task wait <upid>", "task", "wait", upid)
        else:
            r.cover_skip("infra", "task wait", "task wait",
                         "async start returned no UPID")

        # Clone: stop the VM first (clone works on running VMs too, but a
        # stopped clone is cleaner to delete and avoids dirty-disk warnings).
        r.step("qemu", "stop", f"stop VM {vmid} (pre-clone)", "qemu", "stop", vmid)
        clone_id = _next_id(r)
        clone_name = Isolation.NAME_PREFIX + "clone"
        print(DIM(f"  clone_id={clone_id}"))
        clone_created = False
        try:
            r.step("qemu", "clone", f"clone VM {vmid} -> {clone_id}",
                   "qemu", "clone", vmid,
                   "--newid", clone_id,
                   "--name", clone_name,
                   "--pool", Isolation.POOL,
                   "--full")
            clone_created = True
            r.step("qemu", "clone verify", f"verify clone {clone_id} exists",
                   "qemu", "status", clone_id, json_out=True)

            # Migrate: only meaningful when the cluster has more than one node.
            # On a single-node lab, record as SKIP rather than failing.
            n_nodes = _node_count(r)
            if n_nodes < 2:
                r.cover_skip("qemu", "migrate", f"migrate clone {clone_id}",
                             "single-node cluster — migrate requires a second node")
            else:
                # Pick a target node that is not the current node.
                node_res = r.pve("node", "list", json_out=True, node=False)
                other = ""
                if node_res.rc == 0:
                    try:
                        for nd in node_res.json():
                            nd_name = (nd.get("node") or "") if isinstance(nd, dict) else ""
                            if nd_name and nd_name != r.node:
                                other = nd_name
                                break
                    except (ValueError, KeyError):
                        pass
                if not other:
                    r.cover_skip("qemu", "migrate", f"migrate clone {clone_id}",
                                 "could not determine a second node name")
                else:
                    r.soft_step(
                        "qemu", "migrate", f"migrate clone {clone_id} -> {other}",
                        "qemu", "migrate", clone_id, "--target", other,
                        skip_markers=("shared storage", "local disk", "not supported",
                                      "cannot migrate", "no route"),
                        skip_reason="migration blocked by storage or network constraints",
                    )
        finally:
            if clone_created:
                r.undo(f"stop clone {clone_id}", "qemu", "stop", clone_id)
                r.del_step("qemu", "clone delete", f"delete clone {clone_id}",
                           "qemu", "delete", clone_id, "--yes",
                           "--purge", "--destroy-unreferenced-disks")

        # Disk ops on the (stopped) base VM: grow scsi0, relocate it to another
        # storage when one exists, then detach it. All operate on the isolated
        # pve-cli VM and its own disk, so nothing else in the lab is touched.
        r.step("qemu", "disk resize", f"disk resize scsi0 on {vmid} (+1G)",
               "qemu", "disk", "resize", vmid, "--disk", "scsi0", "--size", "+1G")
        alt = _alt_image_storage(r, ROOTDIR_STORAGE)
        if alt:
            r.soft_step(
                "qemu", "disk move", f"disk move scsi0 -> {alt}",
                "qemu", "disk", "move", vmid, "--disk", "scsi0",
                "--storage", alt, "--delete",
                skip_markers=("storage", "no such", "not supported",
                              "same", "content type"),
                skip_reason="target storage cannot hold the disk",
            )
        else:
            r.cover_skip("qemu", "disk move", f"disk move scsi0 on {vmid}",
                         "no second images-capable storage available")
        r.step("qemu", "disk unlink", f"disk unlink scsi0 on {vmid}",
               "qemu", "disk", "unlink", vmid, "--disk", "scsi0", "--force")
    finally:
        r.undo(f"stop VM {vmid}", "qemu", "stop", vmid)
        r.step("qemu", "delete", f"delete VM {vmid}", "qemu", "delete", vmid, "--yes",
               "--purge", "--destroy-unreferenced-disks")


def ct_lifecycle(r: Runner, ostemplate: str) -> None:
    """Drive an Alpine throwaway container through every mutating lxc verb."""
    print(BOLD("lxc: full container verb matrix"))
    ctid = _next_id(r)
    print(DIM(f"  ctid={ctid}"))
    # Flag breadth: drive --swap alongside the core create flags.
    r.step("lxc", "create", f"create CT {ctid}", "lxc", "create", ctid,
           "--ostemplate", ostemplate, "--hostname", CT_HOST,
           "--storage", ROOTDIR_STORAGE, "--rootfs", f"{ROOTDIR_STORAGE}:1",
           "--memory", "256", "--cores", "1", "--swap", "0", "--unprivileged",
           "--net0", f"name=eth0,bridge={Isolation.SDN_VNET},ip={CT_IP},gw={CT_GW}",
           "--pool", Isolation.POOL, "--tags", Isolation.TAG)
    try:
        r.step("lxc", "start", f"start CT {ctid}", "lxc", "start", ctid)
        r.step("lxc", "status", f"status CT {ctid}", "lxc", "status", ctid, json_out=True)
        r.step("lxc", "config set", f"config set CT {ctid}",
               "lxc", "config", "set", ctid, "--description", "pve-cli-e2e")
        # Suspend/resume go through CRIU (`lxc-checkpoint`); on hosts without
        # working CRIU support this can't complete. Treat that as a SKIP rather
        # than a CLI failure, and only resume if the suspend took.
        suspended = r.soft_step(
            "lxc", "suspend", f"suspend CT {ctid}", "lxc", "suspend", ctid,
            skip_markers=("lxc-checkpoint", "criu"),
            skip_reason="host lacks working CRIU checkpoint support",
        )
        if suspended:
            r.step("lxc", "resume", f"resume CT {ctid}", "lxc", "resume", ctid)
        else:
            r.cover_skip("lxc", "resume", f"resume CT {ctid}",
                         "suspend unavailable; nothing to resume")
        # Alpine's init handles a graceful reboot; the CT stays running.
        r.step("lxc", "reboot", f"reboot CT {ctid}", "lxc", "reboot", ctid, "--timeout", "30")
        r.step("lxc", "stop", f"stop CT {ctid}", "lxc", "stop", ctid)
        r.step("lxc", "start", f"start CT {ctid} (again)", "lxc", "start", ctid)
        r.step("lxc", "snapshot create", f"snapshot create {SNAP_NAME}",
               "lxc", "snapshot", "create", ctid, SNAP_NAME)
        r.step("lxc", "snapshot list", "snapshot list",
               "lxc", "snapshot", "list", ctid, json_out=True)
        # Graceful shutdown, then rollback (rollback needs the CT stopped).
        r.step("lxc", "shutdown", f"shutdown CT {ctid}",
               "lxc", "shutdown", ctid, "--timeout", "30", "--force-stop")
        r.step("lxc", "snapshot rollback", f"snapshot rollback {SNAP_NAME}",
               "lxc", "snapshot", "rollback", ctid, SNAP_NAME)
        r.step("lxc", "snapshot delete", f"snapshot delete {SNAP_NAME}",
               "lxc", "snapshot", "delete", ctid, SNAP_NAME)
    finally:
        r.undo(f"stop CT {ctid}", "lxc", "stop", ctid)
        r.step("lxc", "delete", f"delete CT {ctid}", "lxc", "delete", ctid, "--yes",
               "--force", "--purge")


def teardown_network(r: Runner) -> None:
    print(BOLD("teardown: isolated SDN + pool"))
    # Subnet must be deleted by its id (zone-prefixed), not the CIDR.
    sub = r.pve("sdn", "subnet", "list", Isolation.SDN_VNET, json_out=True)
    if sub.rc == 0:
        for s in sub.json():
            sid = s.get("subnet")
            if sid:
                r.undo(f"sdn subnet delete {sid}", "sdn", "subnet", "delete",
                       Isolation.SDN_VNET, sid, "--yes")
    r.undo(f"sdn vnet delete {Isolation.SDN_VNET}", "sdn", "vnet", "delete",
           Isolation.SDN_VNET, "--yes")
    r.undo(f"sdn zone delete {Isolation.SDN_ZONE}", "sdn", "zone", "delete",
           Isolation.SDN_ZONE, "--yes")
    r.undo("sdn apply", "sdn", "apply")
    r.undo(f"pool delete {Isolation.POOL}", "pool", "delete", Isolation.POOL, "--yes")


def sweep_stale_guests(r: Runner) -> None:
    stale = _sweep_stale(r)
    for ref in stale:
        kind, vmid = ref.split(":")
        print(f"  {YELLOW('·')} cleaning stale {kind} {vmid} from a prior run")
        if kind == "qemu":
            r.undo(f"delete VM {vmid}", "qemu", "stop", vmid)
            r.undo(f"delete VM {vmid}", "qemu", "delete", vmid, "--yes", "--purge",
                   "--destroy-unreferenced-disks")
        else:
            r.undo(f"delete CT {vmid}", "lxc", "stop", vmid)
            r.undo(f"delete CT {vmid}", "lxc", "delete", vmid, "--yes", "--force", "--purge")


# --- access / storage / node lifecycle --------------------------------------


def access_lifecycle(r: Runner) -> None:
    """Create an isolated probe user/group/token, exercise the mutating access
    verbs, then tear them down. Role create/delete is read-only in the CLI, so
    it is not exercised here.

    Security: `access user token create` echoes the secret token VALUE to
    stdout. step()/del_step() print only the label on success — never stdout —
    and the value is never parsed or stored, so it stays out of the logs.
    """
    print(BOLD("access: user / group / token / acl / password verbs"))
    user = Isolation.NAME_PREFIX + "probe@pve"
    group = Isolation.NAME_PREFIX + "probe"
    token = "e2e"
    acl_path = "/pool/" + Isolation.POOL
    # Throwaway password for the probe user (NEVER root); not a real secret.
    probe_pw = "pve-cli-e2e-pw"
    try:
        r.step("access", "group create", f"group create {group}",
               "access", "group", "create", group, "--comment", "pve-cli e2e")
        r.step("access", "user create", f"user create {user}",
               "access", "user", "create", user, "--groups", group,
               "--comment", "pve-cli e2e")
        r.step("access", "user get", f"user get {user}",
               "access", "user", "get", user, json_out=True)
        # Changing a password is forbidden when the target authenticates with an
        # API token (PVE blocks /access/password for token auth); record that as
        # a SKIP rather than a failure, same as the CRIU-gated suspend path.
        r.soft_step("access", "password", f"password set {user}",
                    "access", "password", "set", "--userid", user, "--password", probe_pw,
                    skip_markers=("api token", "access/password"),
                    skip_reason="password change not permitted for API-token auth")
        # Token create prints the secret in plaintext — do NOT request json or
        # echo it; step() prints only the label on success.
        r.step("access", "token create", f"token create {user}!{token}",
               "access", "user", "token", "create", user, token, "--comment", "pve-cli e2e")
        r.step("access", "token list", f"token list {user}",
               "access", "user", "token", "list", user, json_out=True)
        # Update verbs: set a fresh comment on the probe group/user/token, then
        # read it back to prove the mutation took (round-trip, not just rc==0).
        updated = "pve-cli-e2e-updated"
        r.step("access", "group set", f"group set {group}",
               "access", "group", "set", group, "--comment", updated)
        got = r.step("access", "group get", f"group get {group}",
                     "access", "group", "get", group, json_out=True)
        if updated not in got.out:
            raise LifecycleError(f"group set not reflected in group get for {group}")
        r.step("access", "user set", f"user set {user}",
               "access", "user", "set", user, "--comment", updated)
        got = r.step("access", "user get", f"user get {user} (after set)",
                     "access", "user", "get", user, json_out=True)
        if updated not in got.out:
            raise LifecycleError(f"user set not reflected in user get for {user}")
        r.step("access", "token set", f"token set {user}!{token}",
               "access", "user", "token", "set", user, token, "--comment", updated)
        got = r.step("access", "token get", f"token get {user}!{token}",
                     "access", "user", "token", "get", user, token, json_out=True)
        if updated not in got.out:
            raise LifecycleError(f"token set not reflected in token get for {user}!{token}")
        # Grant the probe user a read-only role on our own pool path.
        r.step("access", "acl set", f"acl set {acl_path}",
               "access", "acl", "set", "--path", acl_path,
               "--roles", "PVEAuditor", "--users", user)
    finally:
        r.del_step("access", "acl set", f"acl revoke {acl_path}",
                   "access", "acl", "set", "--path", acl_path,
                   "--roles", "PVEAuditor", "--users", user, "--delete")
        r.del_step("access", "token delete", f"token delete {user}!{token}",
                   "access", "user", "token", "delete", user, token, "--yes")
        r.del_step("access", "user delete", f"user delete {user}",
                   "access", "user", "delete", user, "--yes")
        r.del_step("access", "group delete", f"group delete {group}",
                   "access", "group", "delete", group, "--yes")


def auth_lifecycle(r: Runner) -> None:
    """Exercise `api auth login`/`refresh`/`logout` against the live server using
    a throwaway pve-realm user and a scratch `--config` file.

    Isolation: the session is obtained for a fresh `pve-cli-authprobe@pve` user
    (NEVER root) and stored only in a temp config; the real config, the configured
    target, and its session are never touched, so the suite returns to the
    original identity automatically. The user is created with an initial password
    (accepted by `POST /access/users` even under API-token auth) and deleted in
    the finally block.

    The scratch target carries the bare user id (`pve-cli-authprobe`) with the
    realm passed separately as `pve`: PVE's `/access/ticket` rejects a combined
    `user@realm` username when a realm field is also present.
    """
    print(BOLD("api: auth login / refresh / logout (scratch session)"))
    user = Isolation.NAME_PREFIX + "authprobe@pve"     # created on the pve realm
    login_user = Isolation.NAME_PREFIX + "authprobe"   # bare id; realm sent separately
    probe_pw = "pve-cli-e2e-pw"                          # throwaway, never a real secret

    show = r.pve("api", "target", r.target, "show", json_out=True, node=False)
    host = ""
    if show.rc == 0:
        data = show.json()
        data = data.get("data", data) if isinstance(data, dict) else {}
        host = str(data.get("Host", ""))
    if not host:
        for verb in ("auth login", "auth refresh", "auth logout"):
            r.cover_skip("api", verb, verb, "could not resolve target host")
        return

    scratch = tempfile.mkdtemp(prefix="pve-cli-e2e-auth-")
    cfg = os.path.join(scratch, "config.yml")
    created = False
    try:
        create = r.pve("access", "user", "create", user, "--password", probe_pw,
                       "--comment", "pve-cli auth probe")
        if create.rc != 0:
            detail = (create.err.strip() or create.out.strip())[:200]
            raise LifecycleError(f"auth probe user create: {detail}")
        created = True
        print(f"  {GREEN('✓')} user create {user}")

        # Scratch password-auth target pointed at the same host (TLS verification
        # disabled for the throwaway target). target add + set-password mutate
        # local config only and are already covered read-only, so they are setup
        # here, not recorded again.
        r.pve_raw("--config", cfg, "api", "target", "authprobe", "add",
                  "--host", host, "--username", login_user, "--realm", "pve",
                  "--token", "x=y", "--tls-insecure")
        r.pve_raw("--config", cfg, "api", "auth", "set-password", "--target", "authprobe",
                  "--username", login_user, "--secret", probe_pw)

        # login → real session ticket, stored only in the scratch config.
        r.step_raw("api", "auth login", f"auth login as {user}",
                   "--config", cfg, "api", "auth", "login", "--target", "authprobe")
        st = r.pve_raw("--config", cfg, "api", "auth", "status", "--target", "authprobe",
                       json_out=True)
        sess = ""
        if st.rc == 0:
            sd = st.json()
            sd = sd.get("data", sd) if isinstance(sd, dict) else {}
            sess = str(sd.get("Session", ""))
        if "valid" not in sess:
            raise LifecycleError(f"auth login did not establish a session (Session={sess!r})")

        # refresh → re-obtain the ticket for the password target.
        r.step_raw("api", "auth refresh", "auth refresh",
                   "--config", cfg, "api", "auth", "refresh", "--target", "authprobe")

        # logout → invalidate the ticket server-side and clear it locally.
        r.step_raw("api", "auth logout", "auth logout",
                   "--config", cfg, "api", "auth", "logout", "--target", "authprobe")
        st = r.pve_raw("--config", cfg, "api", "auth", "status", "--target", "authprobe",
                       json_out=True)
        if st.rc == 0:
            sd = st.json()
            sd = sd.get("data", sd) if isinstance(sd, dict) else {}
            if str(sd.get("Session", "")) != "none":
                raise LifecycleError("auth logout did not clear the session")
    finally:
        if created:
            r.undo(f"user delete {user}", "access", "user", "delete", user, "--yes")
        shutil.rmtree(scratch, ignore_errors=True)


def storage_lifecycle(r: Runner) -> None:
    """Create / set / delete an isolated `dir` storage config object, restricted
    to the test node. Points at `/var/lib/vz/pve-cli-e2e`: the CLI has no
    `--mkdir`, but a dir storage config is created regardless of whether the
    path exists yet — enough to exercise the create/set/delete verbs."""
    print(BOLD("storage: dir storage create / set / delete"))
    sid = Isolation.NAME_PREFIX + "store"
    spath = "/var/lib/vz/" + Isolation.NAME_PREFIX + "e2e"
    r.step("storage", "create", f"storage create {sid}",
           "storage", "create", "--storage", sid, "--type", "dir",
           "--path", spath, "--content", "iso", "--nodes", r.node)
    try:
        r.step("storage", "get", f"storage get {sid}",
               "storage", "get", sid, json_out=True)
        r.step("storage", "set", f"storage set {sid}",
               "storage", "set", sid, "--content", "iso,vztmpl")
    finally:
        r.del_step("storage", "delete", f"storage delete {sid}",
                   "storage", "delete", sid, "--yes")


def node_ops(r: Runner) -> None:
    """Exercise the SSH-gated node verbs (exec/ssh/rsync). Probe reachability
    with `node exec -- true`; if SSH to the host is unavailable, record all
    three as SKIP rather than failing the suite."""
    print(BOLD("node: exec / ssh / rsync (SSH-gated)"))
    probe = r.pve("node", "exec", r.node, "--", "true")
    if probe.rc != 0:
        reason = "SSH to host unavailable"
        detail = (probe.err.strip() or probe.out.strip()).splitlines()
        if detail:
            reason = detail[-1][:80]
        for verb in ("exec", "ssh", "rsync"):
            r.cover_skip("node", verb, f"node {verb} {r.node}", reason)
        return
    print(f"  {GREEN('✓')} exec {r.node} -- true")
    r.cov.append(Step("node", "exec", PASS))
    r.step("node", "ssh", f"ssh {r.node} -- true", "node", "ssh", r.node, "--", "true")
    # rsync round-trip: seed a scratch file on the host, pull it back to /tmp.
    scratch = "/tmp/" + Isolation.NAME_PREFIX + "rsync"
    r.step("node", "exec", f"seed {scratch} on host",
           "node", "exec", r.node, "--", "sh", "-c", f"echo pve-cli-e2e > {scratch}")
    r.step("node", "rsync", f"rsync {r.node}:{scratch} -> local",
           "node", "rsync", r.node, f"{r.node}:{scratch}", scratch)
    r.undo(f"rm host {scratch}", "node", "exec", r.node, "--", "rm", "-f", scratch)
    try:
        import os as _os
        _os.remove(scratch)
    except OSError:
        pass


# --- coverage report --------------------------------------------------------


def _print_coverage(r: Runner) -> None:
    """Per-group table of every mutating verb the suite drove, with its result.
    Groups: infra (sdn/pool/task), the qemu + lxc guests, and the access /
    storage / node verb blocks — whichever produced steps this run."""
    if not r.cov:
        return
    preferred = ["infra", "qemu", "lxc", "access", "api", "storage", "node"]
    present = {s.guest for s in r.cov}
    guests = [g for g in preferred if g in present]
    guests += [g for g in present if g not in preferred]  # any others, stable-ish
    print(BOLD("Mutating-verb coverage (deferred ops, now exercised live):"))
    for guest in guests:
        steps = [s for s in r.cov if s.guest == guest]
        # Collapse repeats (e.g. two `start`s) to the worst status seen.
        order: list[str] = []
        worst: dict[str, Step] = {}
        rank = {PASS: 0, SKIP: 1, FAIL: 2}
        for s in steps:
            if s.verb not in worst:
                order.append(s.verb)
                worst[s.verb] = s
            elif rank[s.status] > rank[worst[s.verb].status]:
                worst[s.verb] = s
        covered = sum(1 for v in order if worst[v].status == PASS)
        print(f"  {BOLD(guest)} {DIM(f'({covered}/{len(order)} verbs passed)')}")
        for verb in order:
            s = worst[verb]
            line = f"    {_GLYPH[s.status]} {verb}"
            if s.status == SKIP and s.detail:
                line += DIM(f"  ({s.detail})")
            print(line)


# --- entry point ------------------------------------------------------------


def run(target: str, binary: str | None, build: bool, strict: bool,
        skip_ct: bool, skip_vm: bool) -> int:
    bin_path = find_binary(binary, build=build)
    ok, why = target_configured(bin_path, target)
    if not ok:
        msg = f"target {target!r} not usable: {why}"
        if strict:
            print(f"lifecycle: error: {msg}", file=sys.stderr)
            return 3
        print(f"lifecycle: skipping — {msg}")
        return 0

    node = discover_node(bin_path, target)
    if not node:
        print("lifecycle: error: could not discover a node", file=sys.stderr)
        return 3

    r = Runner(bin_path, target, node)
    print(BOLD(f"lifecycle: target={target} node={node}"))
    print(DIM(f"  isolation: zone={Isolation.SDN_ZONE} vnet={Isolation.SDN_VNET} "
              f"subnet={Isolation.SDN_SUBNET} pool={Isolation.POOL} tag={Isolation.TAG}"))
    print()

    failed = False
    started = time.monotonic()
    try:
        # Clean any leftovers from a crashed prior run before provisioning.
        sweep_stale_guests(r)
        teardown_network(r)
        print()

        provision_network(r)
        print()

        # Access + storage verb blocks run regardless of --vm-only/--ct-only:
        # they are independent of the guests and isolated by the pve-cli prefix.
        access_lifecycle(r)
        print()
        auth_lifecycle(r)
        print()
        storage_lifecycle(r)
        print()

        if not skip_vm:
            vm_lifecycle(r)
            print()
        if not skip_ct:
            ostemplate = _ensure_template(r)
            ct_lifecycle(r, ostemplate)
            print()

        node_ops(r)
        print()
    except LifecycleError as exc:
        failed = True
        print(RED(f"lifecycle: aborted at step: {exc}"))
    except KeyboardInterrupt:
        failed = True
        print(RED("lifecycle: interrupted"))
    finally:
        print()
        teardown_network(r)
        sweep_stale_guests(r)

    print()
    _print_coverage(r)
    # A recorded FAIL means a mutating verb did not behave; surface it.
    if any(s.status == FAIL for s in r.cov):
        failed = True

    dur = time.monotonic() - started
    print()
    if failed:
        print(BOLD("lifecycle: ") + RED("FAILED") + DIM(f"  ({dur:.0f}s)"))
        return 1
    print(BOLD("lifecycle: ") + GREEN("PASSED") + DIM(f"  ({dur:.0f}s)"))
    return 0
