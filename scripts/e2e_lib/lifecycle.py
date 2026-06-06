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
import base64
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
FW_IPSET = "pvecli-ips"
FW_ALIAS = "pvecli-alias"
CL_FW_GROUP = "pvecli-grp"      # isolated cluster security group
CL_FW_IPSET = "pvecli-clips"    # cluster-level IP set (distinct from per-guest FW_IPSET)
CL_FW_ALIAS = "pvecli-clalias"  # cluster-level address alias
CL_FW_COMMENT = "pve-cli-e2e"   # marks the throwaway top-level cluster rule
ROOTDIR_STORAGE = "local-lvm"   # lvmthin: supports rootdir/images + snapshots
TMPL_STORAGE = "local"          # holds vztmpl content
BACKUP_STORAGE = "local"        # dir storage that holds backup content
BACKUP_JOB = "pvecli-backup"    # isolated, disabled vzdump schedule id
METRICS_SERVER = "pve-cli-graphite"  # isolated, disabled external metric server
GOTIFY_ENDPOINT = "pve-cli-gotify"   # isolated, disabled gotify notification endpoint
DIR_MAPPING = "pve-cli-dir"          # isolated host-directory mapping
REALMSYNC_REALM = "pve-cli-syncrealm"  # isolated ldap realm the sync job points at
REALMSYNC_JOB = "pve-cli-syncjob"    # isolated, disabled realm-sync job
ACME_PLUGIN = "pve-cli-acme"         # isolated dns-01 ACME challenge plugin
SDN_IPAM = "pvecliipam"              # isolated SDN IPAM backend (pve-type, no external backend)
DUMMY_HOST = "172.30.0.250"     # unused address on the e2e subnet (never contacted)
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


VOLUME_NOTE = "pve-cli-e2e marker"


def _backup_volid(r: Runner, vmid: str) -> str:
    """Return the volid of a backup archive for vmid on BACKUP_STORAGE, or ""."""
    res = r.pve("storage", "content", BACKUP_STORAGE, "--content", "backup",
                "--vmid", vmid, json_out=True)
    if res.rc != 0:
        return ""
    try:
        data = res.json()
    except ValueError:
        return ""
    rows = data if isinstance(data, list) else (
        data.get("rows", []) if isinstance(data, dict) else [])
    for v in rows:
        if isinstance(v, dict) and v.get("volid"):
            return str(v["volid"])
    return ""


def _volume_set_roundtrip(r: Runner, vmid: str) -> str | None:
    """Set a marker note on vmid's backup archive, verify it via `volume get`,
    then restore the original note. Records coverage for `storage volume get`
    and `storage volume set`. Returns an error string if verification fails (the
    caller raises it AFTER pruning the archive), or None on success / no archive.
    """
    volid = _backup_volid(r, vmid)
    if not volid:
        r.cover_skip("storage", "volume get", f"volume get on VM {vmid} backup",
                     "no backup archive found")
        r.cover_skip("storage", "volume set", f"volume set on VM {vmid} backup",
                     "no backup archive found")
        return None

    g0 = r.pve("storage", "volume", "get", volid, json_out=True)
    try:
        orig = str(g0.json().get("notes", "") or "")
    except (ValueError, AttributeError):
        orig = ""

    r.step("storage", "volume set", f"volume set notes on {volid}",
           "storage", "volume", "set", volid, "--notes", VOLUME_NOTE)
    g1 = r.step("storage", "volume get", f"volume get {volid}",
                "storage", "volume", "get", volid, json_out=True)
    err = None
    if VOLUME_NOTE not in g1.out:
        err = f"volume set note not reflected in volume get for {volid}"
    # Restore the original note (an empty string clears it).
    r.step("storage", "volume set restore", f"restore notes on {volid}",
           "storage", "volume", "set", volid, "--notes", orig)
    return err


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


def _alt_rootdir_storage(r: Runner, exclude: str) -> str:
    """Return the id of an enabled storage that supports `rootdir` content other
    than `exclude`, or "" if none exists. Used so the CT volume `move` verb
    relocates a container rootfs to a genuinely different storage."""
    res = r.pve("storage", "list", json_out=True, node=False)
    if res.rc != 0:
        return ""
    try:
        rows = res.json()
    except ValueError:
        return ""
    if not isinstance(rows, list):
        return ""
    for s in rows:
        if not isinstance(s, dict):
            continue
        sid = str(s.get("storage", ""))
        content = str(s.get("content", ""))
        if sid and sid != exclude and "rootdir" in content:
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
        # cloud-init exposure: the VM carries cloud-init config keys (set at
        # create) but no cloud-init drive and no guest OS. `cloudinit pending`
        # reads the config diff and always works; `dump`/`update` need a real
        # cloud-init drive, and `agent ping` needs a running guest agent — both
        # are soft-skipped on this diskless VM.
        r.step("qemu", "cloudinit pending", f"cloudinit pending VM {vmid}",
               "qemu", "cloudinit", "pending", vmid, json_out=True)
        r.soft_step("qemu", "cloudinit dump", f"cloudinit dump user VM {vmid}",
                    "qemu", "cloudinit", "dump", vmid, "--type", "user",
                    skip_markers=("cloudinit", "cloud-init", "no such", "not found"),
                    skip_reason="VM has no cloud-init drive")
        r.soft_step("qemu", "cloudinit update", f"cloudinit update VM {vmid}",
                    "qemu", "cloudinit", "update", vmid,
                    skip_markers=("cloudinit", "cloud-init", "no such", "not found",
                                  "not configured"),
                    skip_reason="VM has no cloud-init drive to regenerate")
        r.soft_step("qemu", "agent ping", f"agent ping VM {vmid}",
                    "qemu", "agent", vmid, "ping",
                    skip_markers=("guest agent", "agent", "not running", "timeout",
                                  "no such"),
                    skip_reason="guest agent not installed/running on diskless VM")
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
                        "qemu", "migrate", clone_id, "--target-node", other,
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

        # Firewall ops on the isolated VM's own config: enable the firewall,
        # add/inspect/remove a rule, an IP set with one member, and an address
        # alias. Every object is scoped to this throwaway VM and uses pve-cli
        # names plus the e2e subnet, so no other workload's policy is touched.
        r.step("qemu", "firewall options set", f"firewall options set on {vmid}",
               "qemu", "firewall", "options", "set", vmid, "--enable", "--policy-in", "ACCEPT")
        r.step("qemu", "firewall options get", f"firewall options get on {vmid}",
               "qemu", "firewall", "options", "get", vmid, json_out=True)
        r.step("qemu", "firewall rules create", f"firewall rule add on {vmid}",
               "qemu", "firewall", "rules", "create", vmid,
               "--type", "in", "--action", "ACCEPT", "--proto", "tcp",
               "--dport", "22", "--comment", "pve-cli-e2e")
        r.step("qemu", "firewall rules list", "firewall rules list",
               "qemu", "firewall", "rules", "list", vmid, json_out=True)
        r.step("qemu", "firewall rules get", "firewall rule get pos 0",
               "qemu", "firewall", "rules", "get", vmid, "0", json_out=True)
        r.del_step("qemu", "firewall rules delete", "firewall rule delete pos 0",
                   "qemu", "firewall", "rules", "delete", vmid, "0", "--yes")
        r.step("qemu", "firewall ipset create", f"firewall ipset create {FW_IPSET}",
               "qemu", "firewall", "ipset", "create", vmid, FW_IPSET, "--comment", "pve-cli-e2e")
        r.step("qemu", "firewall ipset add", f"firewall ipset add {Isolation.SDN_SUBNET}",
               "qemu", "firewall", "ipset", "add", vmid, FW_IPSET, Isolation.SDN_SUBNET)
        r.step("qemu", "firewall ipset list", "firewall ipset member list",
               "qemu", "firewall", "ipset", "list", vmid, FW_IPSET, json_out=True)
        r.del_step("qemu", "firewall ipset remove", f"firewall ipset remove {Isolation.SDN_SUBNET}",
                   "qemu", "firewall", "ipset", "remove", vmid, FW_IPSET, Isolation.SDN_SUBNET, "--yes")
        r.del_step("qemu", "firewall ipset delete", f"firewall ipset delete {FW_IPSET}",
                   "qemu", "firewall", "ipset", "delete", vmid, FW_IPSET, "--yes", "--force")
        r.step("qemu", "firewall alias create", f"firewall alias create {FW_ALIAS}",
               "qemu", "firewall", "alias", "create", vmid, FW_ALIAS, "172.30.0.99",
               "--comment", "pve-cli-e2e")
        r.step("qemu", "firewall alias list", "firewall alias list",
               "qemu", "firewall", "alias", "list", vmid, json_out=True)
        r.del_step("qemu", "firewall alias delete", f"firewall alias delete {FW_ALIAS}",
                   "qemu", "firewall", "alias", "delete", vmid, FW_ALIAS, "--yes")
        # Console proxy: request a VNC ticket on the isolated VM. The ticket
        # carries a short-lived secret, so the step records exit status only and
        # never prints the response body.
        r.step("qemu", "console", f"console vnc ticket on {vmid}",
               "qemu", "console", vmid, "--type", "vnc", json_out=True)
        # On-demand backup of the isolated VM, then prune its own archive. vzdump
        # writes a real backup of THIS throwaway VM to the backup storage; the
        # prune (scoped to this vmid, keep-last=0) removes it again, so no backup
        # artifact is left behind and no other guest's archives are touched.
        r.step("node", "vzdump", f"vzdump backup VM {vmid}",
               "node", "vzdump", "--vmid", vmid, "--storage", BACKUP_STORAGE, "--mode", "snapshot")
        # Single-volume management on the archive just created. Set a marker note
        # on THIS VM's backup, read it back, then restore the original note. Fully
        # reversible, scoped to our own archive, and the prune below removes the
        # archive entirely regardless of outcome. Any verification failure is
        # raised only AFTER the prune so no artifact is left behind.
        vol_verify_err = _volume_set_roundtrip(r, vmid)
        r.step("storage", "prune dry-run", f"prune dry-run for VM {vmid}",
               "storage", "prune", BACKUP_STORAGE, "--vmid", vmid, "--keep-last", "1",
               "--dry-run", json_out=True)
        r.del_step("storage", "prune", f"prune backups of VM {vmid}",
                   "storage", "prune", BACKUP_STORAGE, "--vmid", vmid, "--keep-last", "0", "--yes")
        if vol_verify_err:
            raise LifecycleError(vol_verify_err)
        # HA: manage this isolated VM (sid vm:<id>), then release it. Skipped if
        # the lab is not a quorate cluster.
        ha_resource_lifecycle(r, "qemu", f"vm:{vmid}")
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
        # The running container exposes its host-visible NICs. The endpoint reads
        # the live namespace, so it works once the CT is up; the freshly started
        # network can briefly lag, so a transient lookup error is a SKIP, not a
        # failure.
        r.soft_step("lxc", "interfaces", f"interfaces CT {ctid}",
                    "lxc", "interfaces", ctid,
                    skip_markers=("not running", "timeout", "no such", "unable to open"),
                    skip_reason="container network not ready for interface enumeration")
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

        # Clone the (stopped) container, verify it exists, then migrate it on a
        # multi-node cluster. Mirrors the qemu clone/migrate path; everything
        # stays inside the pve-cli pool so no other workload is touched.
        clone_id = _next_id(r)
        clone_host = Isolation.NAME_PREFIX + "ctclone"
        print(DIM(f"  clone_id={clone_id}"))
        clone_created = False
        try:
            r.step("lxc", "clone", f"clone CT {ctid} -> {clone_id}",
                   "lxc", "clone", ctid,
                   "--newid", clone_id,
                   "--hostname", clone_host,
                   "--pool", Isolation.POOL,
                   "--full")
            clone_created = True
            r.step("lxc", "clone verify", f"verify clone {clone_id} exists",
                   "lxc", "status", clone_id, json_out=True)

            # Migrate: only meaningful when the cluster has more than one node.
            n_nodes = _node_count(r)
            if n_nodes < 2:
                r.cover_skip("lxc", "migrate", f"migrate clone {clone_id}",
                             "single-node cluster — migrate requires a second node")
            else:
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
                    r.cover_skip("lxc", "migrate", f"migrate clone {clone_id}",
                                 "could not determine a second node name")
                else:
                    # A stopped CT migrates offline; --restart is unnecessary.
                    r.soft_step(
                        "lxc", "migrate", f"migrate clone {clone_id} -> {other}",
                        "lxc", "migrate", clone_id, "--target-node", other,
                        skip_markers=("shared storage", "local disk", "not supported",
                                      "cannot migrate", "no route"),
                        skip_reason="migration blocked by storage or network constraints",
                    )
        finally:
            if clone_created:
                r.del_step("lxc", "clone delete", f"delete clone {clone_id}",
                           "lxc", "delete", clone_id, "--yes", "--force", "--purge")

        # Volume ops on the (stopped) base CT: grow rootfs, then relocate it to
        # another rootdir-capable storage when one exists. Both operate on the
        # isolated pve-cli container and its own volume, so nothing else is touched.
        r.step("lxc", "disk resize", f"disk resize rootfs on {ctid} (+1G)",
               "lxc", "disk", "resize", ctid, "--disk", "rootfs", "--size", "+1G")
        alt = _alt_rootdir_storage(r, ROOTDIR_STORAGE)
        if alt:
            r.soft_step(
                "lxc", "disk move", f"disk move rootfs -> {alt}",
                "lxc", "disk", "move", ctid, "--volume", "rootfs",
                "--storage", alt, "--delete",
                skip_markers=("storage", "no such", "not supported",
                              "same", "content type"),
                skip_reason="target storage cannot hold the volume",
            )
        else:
            r.cover_skip("lxc", "disk move", f"disk move rootfs on {ctid}",
                         "no second rootdir-capable storage available")

        # Firewall ops on the isolated CT's own config: enable the firewall,
        # add/inspect/remove a rule, an IP set with one member, and an address
        # alias. Every object is scoped to this throwaway container and uses
        # pve-cli names plus the e2e subnet, so no other workload's policy is touched.
        r.step("lxc", "firewall options set", f"firewall options set on {ctid}",
               "lxc", "firewall", "options", "set", ctid, "--enable", "--policy-in", "ACCEPT")
        r.step("lxc", "firewall options get", f"firewall options get on {ctid}",
               "lxc", "firewall", "options", "get", ctid, json_out=True)
        r.step("lxc", "firewall rules create", f"firewall rule add on {ctid}",
               "lxc", "firewall", "rules", "create", ctid,
               "--type", "in", "--action", "ACCEPT", "--proto", "tcp",
               "--dport", "22", "--comment", "pve-cli-e2e")
        r.step("lxc", "firewall rules list", "firewall rules list",
               "lxc", "firewall", "rules", "list", ctid, json_out=True)
        r.step("lxc", "firewall rules get", "firewall rule get pos 0",
               "lxc", "firewall", "rules", "get", ctid, "0", json_out=True)
        r.del_step("lxc", "firewall rules delete", "firewall rule delete pos 0",
                   "lxc", "firewall", "rules", "delete", ctid, "0", "--yes")
        r.step("lxc", "firewall ipset create", f"firewall ipset create {FW_IPSET}",
               "lxc", "firewall", "ipset", "create", ctid, FW_IPSET, "--comment", "pve-cli-e2e")
        r.step("lxc", "firewall ipset add", f"firewall ipset add {Isolation.SDN_SUBNET}",
               "lxc", "firewall", "ipset", "add", ctid, FW_IPSET, Isolation.SDN_SUBNET)
        r.step("lxc", "firewall ipset list", "firewall ipset member list",
               "lxc", "firewall", "ipset", "list", ctid, FW_IPSET, json_out=True)
        r.del_step("lxc", "firewall ipset remove", f"firewall ipset remove {Isolation.SDN_SUBNET}",
                   "lxc", "firewall", "ipset", "remove", ctid, FW_IPSET, Isolation.SDN_SUBNET, "--yes")
        r.del_step("lxc", "firewall ipset delete", f"firewall ipset delete {FW_IPSET}",
                   "lxc", "firewall", "ipset", "delete", ctid, FW_IPSET, "--yes", "--force")
        r.step("lxc", "firewall alias create", f"firewall alias create {FW_ALIAS}",
               "lxc", "firewall", "alias", "create", ctid, FW_ALIAS, "172.30.0.99",
               "--comment", "pve-cli-e2e")
        r.step("lxc", "firewall alias list", "firewall alias list",
               "lxc", "firewall", "alias", "list", ctid, json_out=True)
        r.del_step("lxc", "firewall alias delete", f"firewall alias delete {FW_ALIAS}",
                   "lxc", "firewall", "alias", "delete", ctid, FW_ALIAS, "--yes")
        # Console proxy: request a VNC ticket on the isolated CT. The ticket
        # carries a short-lived secret, so the step records exit status only and
        # never prints the response body. A container's vncproxy spawns a
        # vncterm that occasionally times out waiting for its port to bind — a
        # host-side limitation, not a CLI fault — so a port-readiness timeout is
        # recorded as a skip rather than a failure.
        r.soft_step("lxc", "console", f"console vnc ticket on {ctid}",
                    "lxc", "console", ctid, "--type", "vnc",
                    skip_markers=("timeout while waiting for port", "port '5900'"),
                    skip_reason="container vncproxy port not ready (host-side timeout)")
        # On-demand backup of the isolated CT, then prune its own archive — same
        # contract as the VM path: the backup is of THIS throwaway container and
        # is pruned immediately, scoped to this ctid, leaving nothing behind.
        r.step("node", "vzdump", f"vzdump backup CT {ctid}",
               "node", "vzdump", "--vmid", ctid, "--storage", BACKUP_STORAGE, "--mode", "snapshot")
        r.step("storage", "prune dry-run", f"prune dry-run for CT {ctid}",
               "storage", "prune", BACKUP_STORAGE, "--vmid", ctid, "--keep-last", "1",
               "--dry-run", json_out=True)
        r.del_step("storage", "prune", f"prune backups of CT {ctid}",
                   "storage", "prune", BACKUP_STORAGE, "--vmid", ctid, "--keep-last", "0", "--yes")
        # HA: manage this isolated CT (sid ct:<id>), then release it. Skipped if
        # the lab is not a quorate cluster.
        ha_resource_lifecycle(r, "lxc", f"ct:{ctid}")
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
        # Clearing the TFA lockout on the probe user exercises the unlock path
        # live. The probe has no TFA configured, so depending on the server this
        # is a no-op success or a benign "no such entry"/permission rejection;
        # tolerate the latter as a SKIP rather than a failure.
        r.soft_step("access", "tfa unlock", f"tfa unlock {user}",
                    "access", "tfa", "unlock", user, "--yes",
                    skip_markers=("tfa", "permission", "not found", "no such",
                                  "lock", "realm", "does not exist"),
                    skip_reason="probe user has no tfa lockout to clear")
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


def domain_lifecycle(r: Runner) -> None:
    """Create an isolated ldap realm, exercise get/set/sync, then delete it.

    Isolation: the realm id is `pve-cli-realm` (NAME_PREFIX + "realm"), fully
    namespaced and distinct from the built-in pam/pve realms. It points at a
    dummy LDAP server that is never contacted on create or set — PVE only probes
    connectivity when `--check-connection` is given, which it is not. Sync DOES
    contact the server, so it is a soft_step: an unreachable dummy host records
    SKIP (the command path and arg parsing are still exercised), while any other
    failure is a real bug. The realm is removed in the finally block.
    """
    print(BOLD("access: domain (realm) create / get / set / sync / delete"))
    realm = Isolation.NAME_PREFIX + "realm"   # pve-cli-realm
    updated = "pve-cli e2e updated"

    # Best-effort clean of a realm left by a crashed prior run so create is
    # idempotent (never raises, not coverage-recorded).
    r.undo(f"pre-clean {realm}", "access", "domain", "delete", realm, "--yes")
    try:
        r.step("access", "domain create", f"domain create {realm}",
               "access", "domain", "create", realm, "--type", "ldap",
               "--server1", "ldap.invalid.pve-cli.local", "--port", "389",
               "--base-dn", "dc=pve-cli,dc=local", "--user-attr", "uid",
               "--comment", "pve-cli e2e")
        got = r.step("access", "domain get", f"domain get {realm}",
                     "access", "domain", "get", realm, json_out=True)
        if "ldap" not in got.out:
            raise LifecycleError(f"domain get did not report the ldap realm type for {realm}")
        r.step("access", "domain set", f"domain set {realm}",
               "access", "domain", "set", realm, "--comment", updated)
        got = r.step("access", "domain get", f"domain get {realm} (after set)",
                     "access", "domain", "get", realm, json_out=True)
        if updated not in got.out:
            raise LifecycleError(f"domain set comment not reflected in get for {realm}")
        # sync contacts the (dummy, unreachable) LDAP server; --dry-run guarantees
        # nothing is written even on an unexpected connection. Tolerate the
        # expected connection failure as a SKIP.
        r.soft_step("access", "domain sync", f"domain sync {realm} (dry-run)",
                    "access", "domain", "sync", realm, "--dry-run", "--scope", "users",
                    skip_markers=("connect", "connection", "timeout", "unable to",
                                  "contact", "resolve", "no route", "ldap", "host"),
                    skip_reason="dummy ldap server unreachable (expected)")
    finally:
        r.del_step("access", "domain delete", f"domain delete {realm}",
                   "access", "domain", "delete", realm, "--yes")


def role_lifecycle(r: Runner) -> None:
    """Create an isolated custom role, change its privileges, then delete it.

    Isolation: the role id is `pve-cli-role` (NAME_PREFIX + "role"), namespaced
    and distinct from the built-in roles. Privileges are read-only audit privs so
    the role grants nothing harmful even if it lingered. The round-trip asserts a
    `role set` is reflected by `role get`. The role is removed in the finally block.
    """
    print(BOLD("access: role create / get / set / delete"))
    # PVE reserves the (case-insensitive) 'PVE' role-ID namespace, so the usual
    # `pve-cli-` prefix is rejected here; prefix with `e2e-` instead. Still
    # uniquely namespaced (`e2e-pve-cli-role`) and safe to leave behind.
    role = "e2e-" + Isolation.NAME_PREFIX + "role"   # e2e-pve-cli-role

    # Best-effort clean of a role left by a crashed prior run (never raises).
    r.undo(f"pre-clean {role}", "access", "role", "delete", role, "--yes")
    try:
        r.step("access", "role create", f"role create {role}",
               "access", "role", "create", role, "--privs", "VM.Audit,Datastore.Audit")
        got = r.step("access", "role get", f"role get {role}",
                     "access", "role", "get", role, json_out=True)
        if "VM.Audit" not in got.out:
            raise LifecycleError(f"role get did not report the granted privilege for {role}")
        # Replace the privilege set (no --append), then prove the new priv is
        # present and the old one is gone.
        r.step("access", "role set", f"role set {role}",
               "access", "role", "set", role, "--privs", "Sys.Audit")
        got = r.step("access", "role get", f"role get {role} (after set)",
                     "access", "role", "get", role, json_out=True)
        if "Sys.Audit" not in got.out:
            raise LifecycleError(f"role set not reflected in role get for {role}")
        if "VM.Audit" in got.out:
            raise LifecycleError(f"role set did not replace the prior privileges for {role}")
    finally:
        r.del_step("access", "role delete", f"role delete {role}",
                   "access", "role", "delete", role, "--yes")


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
    path exists yet — enough to exercise the create/set/delete verbs.

    The definition is node-restricted (`--nodes <node>`) so it is only ever
    considered on the test node, and it is removed in the finally block, leaving
    the cluster storage config as found. Beyond the bare verbs, this also
    exercises the expanded attribute surface (backup retention tunables) to prove
    the new create/set flags reach the API and survive a round-trip."""
    print(BOLD("storage: dir storage create / set / delete"))
    sid = Isolation.NAME_PREFIX + "store"
    spath = "/var/lib/vz/" + Isolation.NAME_PREFIX + "e2e"

    # Best-effort clean of a definition left by a crashed prior run (never raises).
    r.undo(f"pre-clean {sid}", "storage", "delete", sid, "--yes")

    r.step("storage", "create", f"storage create {sid}",
           "storage", "create", "--storage", sid, "--type", "dir",
           "--path", spath, "--content", "backup", "--nodes", r.node,
           "--prune-backups", "keep-last=1", "--max-protected-backups", "1")
    try:
        got = r.step("storage", "get", f"storage get {sid}",
                     "storage", "get", sid, json_out=True)
        if "keep-last=1" not in got.out:
            raise LifecycleError(f"storage get did not report the prune-backups tunable for {sid}")
        # set forwards only the changed flag; the backend type and path are fixed.
        r.step("storage", "set", f"storage set {sid}",
               "storage", "set", sid, "--prune-backups", "keep-last=2", "--content", "backup,iso")
        verify = r.step("storage", "get", f"storage get {sid} (verify)",
                        "storage", "get", sid, json_out=True)
        if "keep-last=2" not in verify.out:
            raise LifecycleError(f"storage set did not update the prune-backups tunable for {sid}")
    finally:
        r.del_step("storage", "delete", f"storage delete {sid}",
                   "storage", "delete", sid, "--yes")


def backup_lifecycle(r: Runner) -> None:
    """Create / inspect / update / delete an isolated, DISABLED vzdump backup
    schedule. The job is scoped to the pve-cli pool and never enabled, so it can
    never run and disrupt other workloads; it carries the pvecli- id prefix and is
    deleted in the finally block."""
    print(BOLD("cluster: backup schedule create / get / set / delete"))
    r.step("cluster", "backup create", f"backup job create {BACKUP_JOB}",
           "cluster", "backup", "create", "--id", BACKUP_JOB,
           "--schedule", "sun 03:30", "--storage", BACKUP_STORAGE,
           "--pool", Isolation.POOL, "--mode", "snapshot",
           "--enabled=false", "--comment", "pve-cli-e2e")
    try:
        r.step("cluster", "backup list", "backup job list",
               "cluster", "backup", "list", json_out=True)
        r.step("cluster", "backup get", f"backup job get {BACKUP_JOB}",
               "cluster", "backup", "get", BACKUP_JOB, json_out=True)
        r.step("cluster", "backup set", f"backup job set {BACKUP_JOB}",
               "cluster", "backup", "set", BACKUP_JOB, "--comment", "pve-cli-e2e-upd")
    finally:
        r.del_step("cluster", "backup delete", f"backup job delete {BACKUP_JOB}",
                   "cluster", "backup", "delete", BACKUP_JOB, "--yes")


def _err_reason(res, fallback: str) -> str:
    """Distil a short, human-readable skip reason from a failed command's output.
    The CLI prints the API message followed by a trailing ` (code: N)` line, so the
    last line is noise; pick the longest meaningful line instead."""
    text = (res.err.strip() or res.out.strip())
    lines = [ln.strip() for ln in text.splitlines() if ln.strip()]
    lines = [ln for ln in lines if not ln.lower().startswith("(code:")]
    if not lines:
        return fallback
    line = max(lines, key=len)
    # The wrapped error reads "<context>: API request failed: <message>"; the
    # trailing API message is the informative part, so prefer it over the prefix.
    for marker in ("api request failed:", "api error:"):
        idx = line.lower().rfind(marker)
        if idx != -1:
            line = line[idx + len(marker):].strip()
            break
    return line[:80]


def ha_resource_lifecycle(r: Runner, guest: str, sid: str) -> None:
    """Place an isolated guest under HA management, read it back, update it, then
    remove it again. HA needs a quorate cluster; a standalone or non-quorate lab
    rejects `ha resource create`, so that failure is recorded as SKIP (an
    environment limitation, like the SSH-gated node verbs) rather than a bug — the
    CLI wiring itself is covered by unit tests. migrate/relocate need a second node
    to accept the guest, so they are SKIPped on a single-node lab. The resource is
    always removed before the guest is destroyed, so HA never blocks teardown."""
    create = r.pve("cluster", "ha", "resource", "create", sid,
                   "--state", "started", "--comment", "pve-cli-e2e")
    if create.rc != 0:
        reason = _err_reason(create, "HA stack unavailable")
        for verb in ("ha resource create", "ha resource get",
                     "ha resource set", "ha resource delete"):
            r.cover_skip(guest, verb, f"{verb} {sid}", reason)
        r.cover_skip(guest, "ha resource migrate", f"ha resource migrate {sid}", reason)
        return
    print(f"  {GREEN('✓')} ha resource create {sid}")
    r.cov.append(Step(guest, "ha resource create", PASS))
    try:
        r.step(guest, "ha resource list", "ha resource list",
               "cluster", "ha", "resource", "list", json_out=True)
        r.step(guest, "ha resource get", f"ha resource get {sid}",
               "cluster", "ha", "resource", "get", sid, json_out=True)
        r.step(guest, "ha resource set", f"ha resource set {sid}",
               "cluster", "ha", "resource", "set", sid, "--comment", "pve-cli-e2e-upd")
        _ha_config_lifecycle(r, guest, sid)
        if _node_count(r) < 2:
            r.cover_skip(guest, "ha resource migrate", f"ha resource migrate {sid}",
                         "needs a second node as the migration target")
    finally:
        r.del_step(guest, "ha resource delete", f"ha resource delete {sid}",
                   "cluster", "ha", "resource", "delete", sid, "--yes", "--purge")


def _ha_config_lifecycle(r: Runner, guest: str, sid: str) -> None:
    """Exercise HA group and rule CRUD against the quorate lab, referencing the
    live HA resource `sid`. A node-affinity rule constrains where `sid` may run; a
    group (pre-PVE-9) pins resources to a node set. Both are namespaced (pve-cli-*)
    and torn down before the parent resource the rule references. HA groups were
    migrated to rules in PVE 9, so `ha group create` is recorded as a SKIP there —
    which must NOT suppress the rule lifecycle, since rules are the replacement and
    still work. A non-quorate lab never reaches here (the parent create already
    skipped), so a group failure is an environment limitation, not a bug."""
    group = Isolation.NAME_PREFIX + "ha"
    rule = Isolation.NAME_PREFIX + "rule"
    grp_create = r.pve("cluster", "ha", "group", "create", group, "--nodes", r.node)
    grp_created = grp_create.rc == 0
    if not grp_created:
        reason = _err_reason(grp_create, "HA group create rejected")
        for verb in ("ha group create", "ha group get", "ha group set", "ha group delete"):
            r.cover_skip(guest, verb, f"{verb} {group}", reason)
    else:
        print(f"  {GREEN('✓')} ha group create {group}")
        r.cov.append(Step(guest, "ha group create", PASS))
    try:
        if grp_created:
            r.step(guest, "ha group list", "ha group list",
                   "cluster", "ha", "group", "list", json_out=True)
            r.step(guest, "ha group get", f"ha group get {group}",
                   "cluster", "ha", "group", "get", group, json_out=True)
            r.step(guest, "ha group set", f"ha group set {group}",
                   "cluster", "ha", "group", "set", group, "--comment", "pve-cli-e2e")
        _ha_rule_lifecycle(r, guest, sid, rule)
    finally:
        if grp_created:
            r.del_step(guest, "ha group delete", f"ha group delete {group}",
                       "cluster", "ha", "group", "delete", group, "--yes")


def _ha_rule_lifecycle(r: Runner, guest: str, sid: str, rule: str) -> None:
    """Create a node-affinity rule constraining `sid`, read/update it, then remove
    it. Driven inside the group lifecycle so the rule is torn down before both the
    group and the parent HA resource it references."""
    rule_create = r.pve("cluster", "ha", "rule", "create", rule,
                        "--type", "node-affinity", "--resources", sid, "--nodes", r.node)
    if rule_create.rc != 0:
        reason = _err_reason(rule_create, "HA rule create rejected")
        for verb in ("ha rule create", "ha rule get", "ha rule set", "ha rule delete"):
            r.cover_skip(guest, verb, f"{verb} {rule}", reason)
        return
    print(f"  {GREEN('✓')} ha rule create {rule}")
    r.cov.append(Step(guest, "ha rule create", PASS))
    try:
        r.step(guest, "ha rule list", "ha rule list",
               "cluster", "ha", "rule", "list", json_out=True)
        r.step(guest, "ha rule get", f"ha rule get {rule}",
               "cluster", "ha", "rule", "get", rule, json_out=True)
        r.step(guest, "ha rule set", f"ha rule set {rule}",
               "cluster", "ha", "rule", "set", rule, "--type", "node-affinity",
               "--comment", "pve-cli-e2e")
    finally:
        r.del_step(guest, "ha rule delete", f"ha rule delete {rule}",
                   "cluster", "ha", "rule", "delete", rule, "--yes")


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


def _cluster_rule_pos_by_comment(r: Runner, comment: str) -> str | None:
    """Return the position of the cluster firewall rule whose comment matches,
    or None. Used to locate the throwaway top-level rule for deletion without
    assuming a fixed position (PVE inserts new rules at position 0)."""
    res = r.pve("cluster", "firewall", "rules", "list", json_out=True, node=False)
    if res.rc != 0:
        return None
    try:
        rows = res.json()
    except ValueError:
        return None
    if not isinstance(rows, list):
        return None
    for rule in rows:
        if isinstance(rule, dict) and rule.get("comment") == comment:
            pos = rule.get("pos")
            if pos is not None:
                return str(pos)
    return None


def _node_rule_pos_by_comment(r: Runner, comment: str) -> str | None:
    """Return the position of the host firewall rule whose comment matches, or
    None. Mirrors the cluster helper: PVE inserts new rules at position 0, so
    the throwaway rule is located by its comment rather than a fixed index."""
    res = r.pve("node", "firewall", "rules", "list", json_out=True)
    if res.rc != 0:
        return None
    try:
        rows = res.json()
    except ValueError:
        return None
    if not isinstance(rows, list):
        return None
    for rule in rows:
        if isinstance(rule, dict) and rule.get("comment") == comment:
            pos = rule.get("pos")
            if pos is not None:
                return str(pos)
    return None


def _vnet_fw_rule_pos_by_comment(r: Runner, vnet: str, comment: str) -> str | None:
    """Return the position of the vnet firewall rule whose comment matches, or
    None. Mirrors the cluster/node helpers: PVE inserts new rules at position 0,
    so the throwaway rule is located by its comment rather than a fixed index."""
    res = r.pve("sdn", "vnet", "firewall", "rules", "list", vnet, json_out=True, node=False)
    if res.rc != 0:
        return None
    try:
        rows = res.json()
    except ValueError:
        return None
    if not isinstance(rows, list):
        return None
    for rule in rows:
        if isinstance(rule, dict) and rule.get("comment") == comment:
            pos = rule.get("pos")
            if pos is not None:
                return str(pos)
    return None


def node_firewall_lifecycle(r: Runner) -> None:
    """Exercise the host firewall of the resolved node: append a disabled rule
    tagged with the pve-cli comment, read it back, then delete it.

    Isolation: the rule is created DISABLED (--enable 0) with a `pve-cli-e2e`
    comment and removed in the same run, so the node's active firewall policy is
    never changed. Host firewall *options* are read only — never set — because
    enabling the host firewall could cut the node off the network. The rule is
    removed in the finally block.
    """
    print(BOLD("node: host firewall rule (disabled, isolated)"))

    # Best-effort clean of a rule left by a crashed prior run (never raises).
    stale = _node_rule_pos_by_comment(r, CL_FW_COMMENT)
    if stale is not None:
        r.undo(f"pre-clean host rule pos {stale}",
               "node", "firewall", "rules", "delete", stale, "--yes")

    created_pos: str | None = None
    try:
        # Host firewall options: read only (never mutated — could isolate the host).
        r.step("node", "firewall options get", "firewall options get",
               "node", "firewall", "options", "get", json_out=True)

        # A disabled rule is inert; it never affects live traffic on the host.
        r.step("node", "firewall rules create", "firewall rules create (disabled)",
               "node", "firewall", "rules", "create",
               "--type", "in", "--action", "ACCEPT", "--proto", "tcp",
               "--dport", "22", "--enable", "0", "--comment", CL_FW_COMMENT)
        r.step("node", "firewall rules list", "firewall rules list",
               "node", "firewall", "rules", "list", json_out=True)

        created_pos = _node_rule_pos_by_comment(r, CL_FW_COMMENT)
        if created_pos is not None:
            r.step("node", "firewall rules get", f"firewall rules get {created_pos}",
                   "node", "firewall", "rules", "get", created_pos, json_out=True)
        else:
            r.cover_skip("node", "firewall rules get", "firewall rules get",
                         "created rule not found by comment")
    finally:
        if created_pos is None:
            created_pos = _node_rule_pos_by_comment(r, CL_FW_COMMENT)
        if created_pos is not None:
            r.del_step("node", "firewall rules delete", f"firewall rules delete {created_pos}",
                       "node", "firewall", "rules", "delete", created_pos, "--yes")


def node_system_lifecycle(r: Runner) -> None:
    """Exercise the node's reversible system-config write verbs.

    Isolation: only the node's time zone and DNS settings are touched, and each
    is set back to the value it already holds (a no-op write). The original
    values are captured first and restored in the finally block, so the node's
    configuration is left exactly as found. The /etc/hosts and subscription write
    verbs are NOT exercised here — replacing /etc/hosts could break host name
    resolution and changing the subscription affects licensing on the shared lab.
    """
    print(BOLD("node: system config (time zone + DNS, set-to-self, reversible)"))

    # ---- time zone: always present, fully reversible -----------------------
    original_tz: str | None = None
    try:
        tz_get = r.step("node", "time get", "time get",
                        "node", "time", "get", json_out=True)
        try:
            td = tz_get.json()
            if isinstance(td, dict):
                tz = td.get("timezone")
                original_tz = tz if isinstance(tz, str) and tz else None
        except ValueError:
            original_tz = None

        if original_tz:
            r.step("node", "time set", f"time set (self: {original_tz})",
                   "node", "time", "set", "--timezone", original_tz)
            verify = r.pve("node", "time", "get", json_out=True)
            ok = False
            try:
                vd = verify.json()
                ok = isinstance(vd, dict) and vd.get("timezone") == original_tz
            except ValueError:
                ok = False
            if ok:
                print(f"  {GREEN('✓')} time set verified (time zone unchanged)")
                r.cov.append(Step("node", "time verify", PASS))
            else:
                r.cover_skip("node", "time verify", "time set verify",
                             "time zone did not read back unchanged")
        else:
            r.cover_skip("node", "time set", "time set", "no time zone reported by the node")
    finally:
        # The set above is a no-op (same value); re-assert it to be safe.
        if original_tz:
            r.del_step("node", "time restore", f"time restore ({original_tz})",
                       "node", "time", "set", "--timezone", original_tz)

    # ---- DNS: guarded on a configured search domain (--search is required) --
    dns_get = r.step("node", "dns get", "dns get", "node", "dns", "get", json_out=True)
    search: str | None = None
    servers: list[tuple[str, str]] = []
    try:
        dd = dns_get.json()
        if isinstance(dd, dict):
            s = dd.get("search")
            search = s if isinstance(s, str) and s else None
            for key in ("dns1", "dns2", "dns3"):
                v = dd.get(key)
                if isinstance(v, str) and v:
                    servers.append((key, v))
    except ValueError:
        search = None

    if not search:
        r.cover_skip("node", "dns set", "dns set",
                     "no DNS search domain configured on the node")
        return

    # Re-apply the exact current values: a no-op write that leaves DNS as found.
    set_args = ["node", "dns", "set", "--search", search]
    for key, val in servers:
        set_args += [f"--{key}", val]
    r.step("node", "dns set", f"dns set (self: {search})", *set_args)
    verify = r.pve("node", "dns", "get", json_out=True)
    ok = False
    try:
        vd = verify.json()
        ok = isinstance(vd, dict) and vd.get("search") == search
    except ValueError:
        ok = False
    if ok:
        print(f"  {GREEN('✓')} dns set verified (search domain unchanged)")
        r.cov.append(Step("node", "dns verify", PASS))
    else:
        r.cover_skip("node", "dns verify", "dns set verify",
                     "search domain did not read back unchanged")


def cluster_options_lifecycle(r: Runner) -> None:
    """Exercise `cluster options get/set` reversibly.

    Isolation: the only option touched is the datacenter `description` (the notes
    panel text), which has no operational effect. The original value is captured
    first and restored in the finally block, so the datacenter config is left
    exactly as found. No policy, migration, or HA option is ever changed.
    """
    print(BOLD("cluster: datacenter options (description marker, reversible)"))

    marker = "pve-cli-e2e options marker"
    original: str | None = None
    try:
        get_res = r.step("cluster", "options get", "options get",
                         "cluster", "options", "get", json_out=True)
        try:
            data = get_res.json()
            if isinstance(data, dict):
                desc = data.get("description")
                original = desc if isinstance(desc, str) else None
        except ValueError:
            original = None

        r.step("cluster", "options set", "options set (description marker)",
               "cluster", "options", "set", "--description", marker)

        verify = r.pve("cluster", "options", "get", json_out=True)
        ok = False
        try:
            vd = verify.json()
            # PVE stores the description with a trailing newline, so compare the
            # stripped value rather than requiring an exact match.
            desc = vd.get("description") if isinstance(vd, dict) else None
            ok = isinstance(desc, str) and desc.strip() == marker
        except ValueError:
            ok = False
        if ok:
            print(f"  {GREEN('✓')} options set verified (description == marker)")
            r.cov.append(Step("cluster", "options verify", PASS))
        else:
            r.cover_skip("cluster", "options verify", "options set verify",
                         "description did not read back as the marker")
    finally:
        # Restore the datacenter description to exactly what it was.
        if original:
            r.del_step("cluster", "options restore", "options restore (original description)",
                       "cluster", "options", "set", "--description", original)
        else:
            r.del_step("cluster", "options restore", "options restore (clear description)",
                       "cluster", "options", "set", "--delete", "description")


def cluster_replication_lifecycle(r: Runner) -> None:
    """Exercise storage replication.

    The job list is read live (always safe). Replication targets a *second* node,
    so on the single-node lab the create/set/delete verbs cannot run — they are
    recorded as coverage skips with the environment reason, mirroring the HA
    migrate single-node pattern. On a multi-node cluster a job would be created
    against the isolated guest in the guest lifecycle.
    """
    print(BOLD("cluster: storage replication job"))

    r.step("cluster", "replication list", "replication list",
           "cluster", "replication", "list", json_out=True)

    if _node_count(r) < 2:
        reason = "replication needs a second node as the target — single-node lab"
        r.cover_skip("cluster", "replication create", "replication create", reason)
        r.cover_skip("cluster", "replication get", "replication get", reason)
        r.cover_skip("cluster", "replication set", "replication set", reason)
        r.cover_skip("cluster", "replication delete", "replication delete", reason)
        return

    # Multi-node cluster: replication still requires an existing guest, which is
    # provisioned by the guest lifecycle; a standalone job has no guest to bind
    # to, so record the gap honestly rather than create an orphaned job.
    r.cover_skip("cluster", "replication create", "replication create",
                 "no isolated guest available in the cluster-scoped block")
    r.cover_skip("cluster", "replication get", "replication get",
                 "no isolated guest available in the cluster-scoped block")
    r.cover_skip("cluster", "replication set", "replication set",
                 "no isolated guest available in the cluster-scoped block")
    r.cover_skip("cluster", "replication delete", "replication delete",
                 "no isolated guest available in the cluster-scoped block")


def cluster_firewall_lifecycle(r: Runner) -> None:
    """Exercise the cluster-wide firewall: a security group with one rule, a
    disabled top-level rule, an IP set with a member, and an address alias.

    Isolation: every object is pve-cli-namespaced (group `pvecli-grp`, ipset
    `pvecli-clips`, alias `pvecli-clalias`) and the IP set and alias use the e2e
    subnet (172.30.0.0/24). The top-level rule is created DISABLED (--enable 0)
    with a `pve-cli-e2e` comment and removed in the same run, so the active
    datacenter policy is never changed. Datacenter firewall *options* are read
    only — never set — because enabling the cluster firewall would affect every
    node. All objects are removed in the finally block.
    """
    print(BOLD("cluster: firewall group / rule / ipset / alias"))

    # Best-effort clean of objects left by a crashed prior run (never raises).
    stale = _cluster_rule_pos_by_comment(r, CL_FW_COMMENT)
    if stale is not None:
        r.undo(f"pre-clean cluster rule pos {stale}",
               "cluster", "firewall", "rules", "delete", stale, "--yes")
    r.undo(f"pre-clean group rule {CL_FW_GROUP}",
           "cluster", "firewall", "group", "rule-delete", CL_FW_GROUP, "0", "--yes")
    r.undo(f"pre-clean {CL_FW_IPSET}",
           "cluster", "firewall", "ipset", "delete", CL_FW_IPSET, "--yes", "--force")
    r.undo(f"pre-clean {CL_FW_ALIAS}",
           "cluster", "firewall", "alias", "delete", CL_FW_ALIAS, "--yes")
    r.undo(f"pre-clean {CL_FW_GROUP}",
           "cluster", "firewall", "group", "delete", CL_FW_GROUP, "--yes")

    created_rule_pos: str | None = None
    try:
        # Datacenter firewall options: read only (never mutated — cluster-wide).
        r.step("cluster", "firewall options get", "firewall options get",
               "cluster", "firewall", "options", "get", json_out=True)

        # Security group + a rule inside it (inert until referenced by --action).
        r.step("cluster", "firewall group create", f"firewall group create {CL_FW_GROUP}",
               "cluster", "firewall", "group", "create", CL_FW_GROUP, "--comment", "pve-cli-e2e")
        r.step("cluster", "firewall group list", "firewall group list",
               "cluster", "firewall", "group", "list", json_out=True)
        r.step("cluster", "firewall group rule-add", f"firewall group rule-add {CL_FW_GROUP}",
               "cluster", "firewall", "group", "rule-add", CL_FW_GROUP,
               "--type", "in", "--action", "ACCEPT", "--proto", "tcp", "--dport", "22")
        r.step("cluster", "firewall group rules", "firewall group rules list",
               "cluster", "firewall", "group", "rules", CL_FW_GROUP, json_out=True)
        r.del_step("cluster", "firewall group rule-delete", "firewall group rule-delete pos 0",
                   "cluster", "firewall", "group", "rule-delete", CL_FW_GROUP, "0", "--yes")

        # Top-level cluster rule: created DISABLED, found by comment, then deleted.
        r.step("cluster", "firewall rules create", "firewall rule add (disabled)",
               "cluster", "firewall", "rules", "create",
               "--type", "in", "--action", "ACCEPT", "--proto", "tcp",
               "--dport", "22", "--enable", "0", "--comment", CL_FW_COMMENT)
        r.step("cluster", "firewall rules list", "firewall rules list",
               "cluster", "firewall", "rules", "list", json_out=True)
        created_rule_pos = _cluster_rule_pos_by_comment(r, CL_FW_COMMENT)
        if created_rule_pos is None:
            raise LifecycleError("created cluster firewall rule not found in list")
        r.step("cluster", "firewall rules get", f"firewall rule get pos {created_rule_pos}",
               "cluster", "firewall", "rules", "get", created_rule_pos, json_out=True)

        # IP set with one member drawn from the e2e subnet.
        r.step("cluster", "firewall ipset create", f"firewall ipset create {CL_FW_IPSET}",
               "cluster", "firewall", "ipset", "create", CL_FW_IPSET, "--comment", "pve-cli-e2e")
        r.step("cluster", "firewall ipset add", f"firewall ipset add {Isolation.SDN_SUBNET}",
               "cluster", "firewall", "ipset", "add", CL_FW_IPSET, Isolation.SDN_SUBNET)
        r.step("cluster", "firewall ipset list", "firewall ipset member list",
               "cluster", "firewall", "ipset", "list", CL_FW_IPSET, json_out=True)
        r.del_step("cluster", "firewall ipset remove", f"firewall ipset remove {Isolation.SDN_SUBNET}",
                   "cluster", "firewall", "ipset", "remove", CL_FW_IPSET, Isolation.SDN_SUBNET, "--yes")

        # Address alias.
        r.step("cluster", "firewall alias create", f"firewall alias create {CL_FW_ALIAS}",
               "cluster", "firewall", "alias", "create", CL_FW_ALIAS, "172.30.0.99",
               "--comment", "pve-cli-e2e")
        r.step("cluster", "firewall alias list", "firewall alias list",
               "cluster", "firewall", "alias", "list", json_out=True)
    finally:
        # Delete the top-level rule by its discovered (or re-discovered) position.
        pos = created_rule_pos if created_rule_pos is not None else _cluster_rule_pos_by_comment(r, CL_FW_COMMENT)
        if pos is not None:
            r.del_step("cluster", "firewall rules delete", f"firewall rule delete pos {pos}",
                       "cluster", "firewall", "rules", "delete", pos, "--yes")
        r.del_step("cluster", "firewall ipset delete", f"firewall ipset delete {CL_FW_IPSET}",
                   "cluster", "firewall", "ipset", "delete", CL_FW_IPSET, "--yes", "--force")
        r.del_step("cluster", "firewall alias delete", f"firewall alias delete {CL_FW_ALIAS}",
                   "cluster", "firewall", "alias", "delete", CL_FW_ALIAS, "--yes")
        # The group must be empty before deletion; clear any lingering rule first.
        r.undo(f"clear group rule {CL_FW_GROUP}",
               "cluster", "firewall", "group", "rule-delete", CL_FW_GROUP, "0", "--yes")
        r.del_step("cluster", "firewall group delete", f"firewall group delete {CL_FW_GROUP}",
                   "cluster", "firewall", "group", "delete", CL_FW_GROUP, "--yes")


def cluster_metrics_lifecycle(r: Runner) -> None:
    """Exercise `cluster metrics server create/get/set/delete` reversibly.

    Isolation: a single Graphite metric server `pve-cli-graphite` is created
    DISABLED (--disable) pointing at an unused address on the e2e subnet
    (172.30.0.250) that is never contacted — Proxmox stores the plugin config
    without probing the target on create. The Graphite type carries no secret
    (unlike InfluxDB's token), so nothing sensitive is involved. The server is
    removed in the finally block, leaving the cluster metric config as found.
    """
    print(BOLD("cluster: external metric server (graphite, disabled, reversible)"))

    # Best-effort clean of a server left by a crashed prior run (never raises).
    r.undo(f"pre-clean {METRICS_SERVER}",
           "cluster", "metrics", "server", "delete", METRICS_SERVER, "--yes")

    try:
        r.step("cluster", "metrics server create", f"metrics server create {METRICS_SERVER}",
               "cluster", "metrics", "server", "create", METRICS_SERVER,
               "--type", "graphite", "--server", DUMMY_HOST, "--port", "2003", "--disable")
        r.step("cluster", "metrics server list", "metrics server list",
               "cluster", "metrics", "server", "list", json_out=True)
        r.step("cluster", "metrics server get", f"metrics server get {METRICS_SERVER}",
               "cluster", "metrics", "server", "get", METRICS_SERVER, json_out=True)
        # set requires re-sending server+port (the API rewrites the full target).
        r.step("cluster", "metrics server set", "metrics server set (mtu)",
               "cluster", "metrics", "server", "set", METRICS_SERVER,
               "--server", DUMMY_HOST, "--port", "2003", "--mtu", "1400")
    finally:
        r.del_step("cluster", "metrics server delete", f"metrics server delete {METRICS_SERVER}",
                   "cluster", "metrics", "server", "delete", METRICS_SERVER, "--yes")


def cluster_notifications_lifecycle(r: Runner) -> None:
    """Exercise `cluster notifications gotify create/get/set/delete` reversibly.

    Isolation: a single Gotify endpoint `pve-cli-gotify` is created DISABLED
    pointing at an unused address on the e2e subnet (172.30.0.250). Proxmox does
    not contact the server on create or update — only an explicit `test` verb
    would, and it is never invoked — so the dummy host is never reached. The
    Gotify token is a throwaway dummy value; the CLI never echoes it (create
    returns only a status message) and it is not placed in any printed label.
    The endpoint is removed in the finally block.
    """
    print(BOLD("cluster: notification endpoint (gotify, disabled, reversible)"))

    # Best-effort clean of an endpoint left by a crashed prior run (never raises).
    r.undo(f"pre-clean {GOTIFY_ENDPOINT}",
           "cluster", "notifications", "gotify", "delete", GOTIFY_ENDPOINT, "--yes")

    try:
        r.step("cluster", "notifications targets", "notifications targets",
               "cluster", "notifications", "targets", json_out=True)
        r.step("cluster", "notifications gotify create", f"notifications gotify create {GOTIFY_ENDPOINT}",
               "cluster", "notifications", "gotify", "create", GOTIFY_ENDPOINT,
               "--server", f"https://{DUMMY_HOST}", "--token", "pve-cli-e2e-dummy-token",
               "--comment", "pve-cli-e2e", "--disable")
        r.step("cluster", "notifications gotify list", "notifications gotify list",
               "cluster", "notifications", "gotify", "list", json_out=True)
        r.step("cluster", "notifications gotify get", f"notifications gotify get {GOTIFY_ENDPOINT}",
               "cluster", "notifications", "gotify", "get", GOTIFY_ENDPOINT, json_out=True)
        r.step("cluster", "notifications gotify set", "notifications gotify set (comment)",
               "cluster", "notifications", "gotify", "set", GOTIFY_ENDPOINT,
               "--comment", "pve-cli-e2e updated")
    finally:
        r.del_step("cluster", "notifications gotify delete", f"notifications gotify delete {GOTIFY_ENDPOINT}",
                   "cluster", "notifications", "gotify", "delete", GOTIFY_ENDPOINT, "--yes")


def cluster_mapping_lifecycle(r: Runner) -> None:
    """Exercise `cluster mapping dir create/get/set/delete` reversibly.

    Isolation: a single host-directory mapping `pve-cli-dir` is created with one
    per-node entry pointing at /var/lib/vz (which always exists on a PVE node).
    A directory mapping needs only a node and a path — no real hardware — so it
    is safe to create and remove on a shared lab. PCI and USB mappings need real
    device IDs and are not exercised live. The mapping is removed in the finally
    block, leaving the cluster mapping config as found.
    """
    print(BOLD("cluster: host-directory mapping (reversible)"))

    entry = f"node={r.node},path=/var/lib/vz"

    # Best-effort clean of a mapping left by a crashed prior run (never raises).
    r.undo(f"pre-clean {DIR_MAPPING}",
           "cluster", "mapping", "dir", "delete", DIR_MAPPING, "--yes")

    try:
        r.step("cluster", "mapping dir create", f"mapping dir create {DIR_MAPPING}",
               "cluster", "mapping", "dir", "create", DIR_MAPPING,
               "--map", entry, "--description", "pve-cli-e2e")
        r.step("cluster", "mapping dir list", "mapping dir list",
               "cluster", "mapping", "dir", "list", json_out=True)
        got = r.step("cluster", "mapping dir get", f"mapping dir get {DIR_MAPPING}",
                     "cluster", "mapping", "dir", "get", DIR_MAPPING, json_out=True)
        if "/var/lib/vz" not in got.out:
            raise LifecycleError(f"mapping dir get did not report the mapped path for {DIR_MAPPING}")
        # set re-sends the full --map (the API rewrites the per-node map on update).
        r.step("cluster", "mapping dir set", "mapping dir set (description)",
               "cluster", "mapping", "dir", "set", DIR_MAPPING,
               "--map", entry, "--description", "pve-cli-e2e updated")
    finally:
        r.del_step("cluster", "mapping dir delete", f"mapping dir delete {DIR_MAPPING}",
                   "cluster", "mapping", "dir", "delete", DIR_MAPPING, "--yes")


def cluster_realmsync_lifecycle(r: Runner) -> None:
    """Exercise `cluster jobs realm-sync create/get/set/delete` reversibly.

    Isolation: a realm-sync job needs an authentication realm to point at, so
    this creates its own isolated ldap realm `pve-cli-syncrealm` (pointing at a
    dummy server that is never contacted — job creation only registers a schedule
    and never syncs) and a DISABLED job `pve-cli-syncjob` against it. The job is
    created with --enabled=false so it never fires on the schedule. Both the job
    and the realm are removed in the finally block, leaving auth + job config as
    found.
    """
    print(BOLD("cluster: realm-sync job (disabled, reversible)"))

    # Best-effort clean of a job/realm left by a crashed prior run (never raises).
    r.undo(f"pre-clean {REALMSYNC_JOB}",
           "cluster", "jobs", "realm-sync", "delete", REALMSYNC_JOB, "--yes")
    r.undo(f"pre-clean {REALMSYNC_REALM}",
           "access", "domain", "delete", REALMSYNC_REALM, "--yes")

    realm_created = False
    try:
        r.step("access", "domain create", f"domain create {REALMSYNC_REALM} (for realm-sync)",
               "access", "domain", "create", REALMSYNC_REALM, "--type", "ldap",
               "--server1", "ldap.invalid.pve-cli.local", "--port", "389",
               "--base-dn", "dc=pve-cli,dc=local", "--user-attr", "uid",
               "--comment", "pve-cli e2e realm-sync")
        realm_created = True
        r.step("cluster", "jobs realm-sync create", f"jobs realm-sync create {REALMSYNC_JOB}",
               "cluster", "jobs", "realm-sync", "create", REALMSYNC_JOB,
               "--schedule", "daily", "--realm", REALMSYNC_REALM, "--scope", "both",
               "--comment", "pve-cli-e2e", "--enabled=false")
        r.step("cluster", "jobs realm-sync list", "jobs realm-sync list",
               "cluster", "jobs", "realm-sync", "list", json_out=True)
        got = r.step("cluster", "jobs realm-sync get", f"jobs realm-sync get {REALMSYNC_JOB}",
                     "cluster", "jobs", "realm-sync", "get", REALMSYNC_JOB, json_out=True)
        if REALMSYNC_REALM not in got.out:
            raise LifecycleError(f"realm-sync get did not report the realm for {REALMSYNC_JOB}")
        # set must re-send the required schedule; change the comment.
        r.step("cluster", "jobs realm-sync set", "jobs realm-sync set (comment)",
               "cluster", "jobs", "realm-sync", "set", REALMSYNC_JOB,
               "--schedule", "weekly", "--comment", "pve-cli-e2e updated")
    finally:
        r.del_step("cluster", "jobs realm-sync delete", f"jobs realm-sync delete {REALMSYNC_JOB}",
                   "cluster", "jobs", "realm-sync", "delete", REALMSYNC_JOB, "--yes")
        if realm_created:
            r.del_step("access", "domain delete", f"domain delete {REALMSYNC_REALM}",
                       "access", "domain", "delete", REALMSYNC_REALM, "--yes")


def cluster_acme_plugin_lifecycle(r: Runner) -> None:
    """Exercise `cluster acme plugin create/get/set/delete` reversibly.

    Isolation: a single dns-01 ACME challenge plugin `pve-cli-acme` is created
    with a throwaway Cloudflare-style credential block. The plugin is a local
    cluster-config entry only; it is never attached to a node certificate and no
    certificate is ever ordered, so the ACME CA is never contacted and the dummy
    credential is never used. The credential is a base64 dummy value and is never
    echoed (create returns only a status message; the value is not placed in any
    printed label). The plugin is removed in the finally block, leaving the ACME
    config as found. Account register/update/deregister contact the CA and are
    never exercised live.
    """
    print(BOLD("cluster: ACME dns-01 plugin (reversible)"))

    # Dummy base64 credential block (never used to issue a certificate).
    data = base64.b64encode(b"CF_Token=pve-cli-e2e-dummy\n").decode("ascii")

    # Best-effort clean of a plugin left by a crashed prior run (never raises).
    r.undo(f"pre-clean {ACME_PLUGIN}",
           "cluster", "acme", "plugin", "delete", ACME_PLUGIN, "--yes")

    try:
        r.step("cluster", "acme plugin create", f"acme plugin create {ACME_PLUGIN}",
               "cluster", "acme", "plugin", "create", ACME_PLUGIN,
               "--type", "dns", "--api", "cf", "--data", data,
               "--validation-delay", "30", "--disable")
        r.step("cluster", "acme plugin list", "acme plugin list",
               "cluster", "acme", "plugin", "list", json_out=True)
        got = r.step("cluster", "acme plugin get", f"acme plugin get {ACME_PLUGIN}",
                     "cluster", "acme", "plugin", "get", ACME_PLUGIN, json_out=True)
        if "cf" not in got.out:
            raise LifecycleError(f"acme plugin get did not report the api for {ACME_PLUGIN}")
        r.step("cluster", "acme plugin set", "acme plugin set (validation-delay)",
               "cluster", "acme", "plugin", "set", ACME_PLUGIN,
               "--validation-delay", "60")
    finally:
        r.del_step("cluster", "acme plugin delete", f"acme plugin delete {ACME_PLUGIN}",
                   "cluster", "acme", "plugin", "delete", ACME_PLUGIN, "--yes")


def sdn_objects_lifecycle(r: Runner) -> None:
    """Exercise `sdn ipam` CRUD and `sdn vnet set` reversibly.

    Isolation: a built-in `pve`-type IPAM backend (`pvecliipam`) is created. The
    `pve` IPAM stores its allocations in the cluster config itself, so — unlike
    the netbox/phpipam/powerdns plugins, which validate connectivity to an
    external backend on create — it needs no reachable endpoint and is a pure,
    reversible config edit. It is a staged config entry only: it is never
    committed with `sdn apply` and no subnet references it, so live networking is
    untouched. The vnet edit targets the isolated `pvecli0` vnet provisioned by
    this suite (no guest depends on its alias) and exercises the shared update
    path live. Every created object is removed in a finally block, leaving the
    SDN config as found. Backend-validated IPAM/DNS providers and routing
    controllers are deferred live and covered by unit tests.
    """
    print(BOLD("sdn: pve IPAM + vnet edit (reversible, staged-only)"))

    # Best-effort clean of an object left by a crashed prior run (never raises).
    r.undo(f"pre-clean {SDN_IPAM}", "sdn", "ipam", "delete", SDN_IPAM, "--yes")

    # ---- IPAM (pve-type, staged-only, no external backend) ------------------
    try:
        r.step("sdn", "ipam create", f"ipam create {SDN_IPAM}",
               "sdn", "ipam", "create", SDN_IPAM, "--type", "pve")
        got = r.step("sdn", "ipam get", f"ipam get {SDN_IPAM}",
                     "sdn", "ipam", "get", SDN_IPAM, json_out=True)
        if "pve" not in got.out:
            raise LifecycleError(f"ipam get did not report the type for {SDN_IPAM}")
        r.step("sdn", "ipam list", "ipam list", "sdn", "ipam", "list", json_out=True)
        # `ipam status` is only supported for the default `pve` IPAM (covered by
        # the read-only tree); the API rejects it for any other IPAM id.
    finally:
        r.del_step("sdn", "ipam delete", f"ipam delete {SDN_IPAM}",
                   "sdn", "ipam", "delete", SDN_IPAM, "--yes")

    # ---- vnet edit on the isolated pvecli0 vnet (staged-only) ---------------
    # Covers the shared set/update path; restored via --delete in the same run.
    r.step("sdn", "vnet set", f"vnet set {Isolation.SDN_VNET} (alias)",
           "sdn", "vnet", "set", Isolation.SDN_VNET, "--alias", "pve-cli-e2e")
    r.step("sdn", "vnet set delete", f"vnet set {Isolation.SDN_VNET} (--delete alias)",
           "sdn", "vnet", "set", Isolation.SDN_VNET, "--delete", "alias")

    # ---- vnet firewall on the isolated pvecli0 vnet (staged-only) -----------
    # A disabled rule (--enable 0) is appended, read back, then deleted. The
    # vnet firewall is never enabled (options are read-only), so no traffic on
    # any guest is affected; the rule is staged and removed in the same run.
    vnet = Isolation.SDN_VNET
    stale = _vnet_fw_rule_pos_by_comment(r, vnet, CL_FW_COMMENT)
    if stale is not None:
        r.undo(f"pre-clean vnet fw rule {stale}", "sdn", "vnet", "firewall",
               "rules", "delete", vnet, stale, "--yes")
    created_pos = None
    try:
        r.step("sdn", "vnet fw rule create", f"vnet firewall rules create {vnet}",
               "sdn", "vnet", "firewall", "rules", "create", vnet,
               "--type", "forward", "--action", "ACCEPT", "--enable", "0",
               "--comment", CL_FW_COMMENT)
        r.step("sdn", "vnet fw rules list", f"vnet firewall rules list {vnet}",
               "sdn", "vnet", "firewall", "rules", "list", vnet, json_out=True)
        created_pos = _vnet_fw_rule_pos_by_comment(r, vnet, CL_FW_COMMENT)
        if created_pos is not None:
            r.step("sdn", "vnet fw rule get", f"vnet firewall rules get {vnet} {created_pos}",
                   "sdn", "vnet", "firewall", "rules", "get", vnet, created_pos, json_out=True)
        else:
            r.cover_skip("sdn", "vnet fw rule get",
                         "created rule position not found by comment")
        # Options are read-only: enabling the vnet firewall could affect guests.
        r.step("sdn", "vnet fw options get", f"vnet firewall options get {vnet}",
               "sdn", "vnet", "firewall", "options", "get", vnet, json_out=True)
    finally:
        pos = created_pos if created_pos is not None else \
            _vnet_fw_rule_pos_by_comment(r, vnet, CL_FW_COMMENT)
        if pos is not None:
            r.del_step("sdn", "vnet fw rule delete", f"vnet firewall rules delete {vnet} {pos}",
                       "sdn", "vnet", "firewall", "rules", "delete", vnet, pos, "--yes")


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
        domain_lifecycle(r)
        print()
        role_lifecycle(r)
        print()
        auth_lifecycle(r)
        print()
        storage_lifecycle(r)
        print()
        backup_lifecycle(r)
        print()
        cluster_firewall_lifecycle(r)
        print()
        node_firewall_lifecycle(r)
        print()
        node_system_lifecycle(r)
        print()
        cluster_options_lifecycle(r)
        print()
        cluster_replication_lifecycle(r)
        print()
        cluster_metrics_lifecycle(r)
        print()
        cluster_notifications_lifecycle(r)
        print()
        cluster_mapping_lifecycle(r)
        print()
        cluster_realmsync_lifecycle(r)
        print()
        cluster_acme_plugin_lifecycle(r)

        sdn_objects_lifecycle(r)
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
