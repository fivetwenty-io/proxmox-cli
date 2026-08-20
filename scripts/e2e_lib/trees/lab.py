"""lab: config-driven nested lab lifecycle (pmx persona only).

The read-only verbs (`list`, `status`) run live against the configured
cluster. The config verbs (`config init/add/show`) operate on a throwaway
scratch `--config` file, never the real config.yml. Every mutating verb has
its error contract exercised with an unresolvable lab name — each fails
during config resolution, before any API call or ssh — and its happy path
deferred: those verbs provision, reshape, or destroy SDN, storage, pools,
clusters, and VMs, and need the dedicated lab-pmx destructive test lab as a
standing target.

`context rm` is the one verb that does not resolve a lab (it exists to clean
up a context whose lab is already gone), so its error contract is driven
through the lab-name charset guard instead.
"""

from __future__ import annotations

import os
import shutil
import tempfile

from ..context import CmdResult, Ctx
from ..model import Status

NAME = "lab"
DESCRIPTION = "Config-driven nested lab lifecycle"

# A lab name that must never resolve; used to exercise each mutating verb's
# error contract without reaching the API.
ABSENT = "e2eabsent"


def run(ctx: Ctx) -> None:
    _scratch_config_checks(ctx)
    _live_readonly_checks(ctx)
    _mutating_error_contracts(ctx)
    _deferred_mutations(ctx)


def _scratch_config_checks(ctx: Ctx) -> None:
    """Drive config init/add/show against a throwaway config file."""
    probe = "e2eprobe"

    def show_has_probe(res: CmdResult) -> str | None:
        data = res.json()
        if not isinstance(data, dict):
            return "expected a JSON object"
        lab = data.get("lab")
        if not (isinstance(lab, dict) and lab.get("name") == probe):
            return f"resolved lab is not {probe!r}"
        if probe not in str(data.get("provenance", "")):
            return f"provenance does not name {probe!r}'s file"
        return None

    scratch_dir = tempfile.mkdtemp(prefix="pmx-cli-e2e-lab-")
    cfg = os.path.join(scratch_dir, "config.yml")
    try:
        # `config init` never rewrites config.yml (it only prints the
        # labs_dir line to add), and ResolveLabs only globs labs.d when
        # labs_dir/include is actually set — so seed the scratch config
        # with labs_dir up front or `config show` would resolve zero labs.
        with open(cfg, "w", encoding="utf-8") as fh:
            fh.write("labs_dir: labs.d\n")

        res = ctx.check(
            "config init (temp path)", "--config", cfg, "lab", "config", "init",
            with_context=False, fmt="",
        )
        example = os.path.join(scratch_dir, "labs.d", "example.yaml")
        if res.rc == 0 and not os.path.isfile(example):
            ctx.results[-1].status = Status.FAIL
            ctx.results[-1].detail = "config init reported success but wrote no example.yaml"

        ctx.check(
            "config add", "--config", cfg, "lab", "config", "add", probe,
            "--vxlan-tag", "5099", "--cidr", "10.199.0.0/16",
            with_context=False, fmt="",
        )
        ctx.check(
            "config show", "--config", cfg, "lab", "config", "show", probe,
            with_context=False, validate=show_has_probe,
        )

        # Error contract: re-adding a name that already resolves must refuse
        # without --force.
        ctx.expect_fail(
            "config add (duplicate guard)", "--config", cfg,
            "lab", "config", "add", probe,
            "--vxlan-tag", "5099", "--cidr", "10.199.0.0/16",
            must_contain="already", with_context=False,
        )
    finally:
        shutil.rmtree(scratch_dir, ignore_errors=True)


def _live_readonly_checks(ctx: Ctx) -> None:
    """list joins configured labs against live cluster state (read-only);
    status runs only when the operator's config defines at least one lab."""

    def is_table(res: CmdResult) -> str | None:
        data = res.json()
        if isinstance(data, dict) and isinstance(data.get("headers"), list):
            return None
        return "expected a JSON table object with headers"

    lst = ctx.check("list", "lab", "list", validate=is_table)

    name = None
    if lst.rc == 0:
        try:
            rows = lst.json().get("rows") or []
            if rows and rows[0]:
                name = rows[0][0]
        except (ValueError, AttributeError, IndexError):
            name = None

    if name is None:
        ctx.skip("status", "no lab defined in the operator's config")
    else:
        ctx.check("status", "lab", "status", str(name))

    ctx.expect_fail("status (unknown lab)", "lab", "status", ABSENT,
                    must_contain="not found")


def _mutating_error_contracts(ctx: Ctx) -> None:
    """Each mutating verb refuses an unresolvable lab during config
    resolution — before building a plan or touching the API — so these run
    safely against any cluster."""
    ctx.expect_fail("create (unknown lab)", "lab", "create", ABSENT,
                    must_contain="not found")
    ctx.expect_fail("destroy (unknown lab)", "lab", "destroy", ABSENT, "--yes",
                    must_contain="not found")
    ctx.expect_fail("start (unknown lab)", "lab", "start", ABSENT,
                    must_contain="not found")
    ctx.expect_fail("stop (unknown lab)", "lab", "stop", ABSENT,
                    must_contain="not found")
    ctx.expect_fail("net apply (unknown lab)", "lab", "net", "apply", ABSENT,
                    must_contain="not found")
    ctx.expect_fail("access grant (unknown lab)",
                    "lab", "access", "grant", ABSENT, "member@pve",
                    must_contain="not found")
    ctx.expect_fail("quota set (unknown lab)", "lab", "quota", "set", ABSENT,
                    "--refquota-gb", "600", "--yes",
                    must_contain="not found")
    ctx.expect_fail("cluster init (unknown lab)", "lab", "cluster", "init", ABSENT,
                    must_contain="not found")
    ctx.expect_fail("cluster join (unknown lab)", "lab", "cluster", "join", ABSENT,
                    must_contain="not found")
    ctx.expect_fail("cluster status (unknown lab)", "lab", "cluster", "status", ABSENT,
                    must_contain="not found")
    ctx.expect_fail("ceph install (unknown lab)", "lab", "ceph", "install", ABSENT,
                    must_contain="not found")
    ctx.expect_fail("context sync (unknown lab)", "lab", "context", "sync", ABSENT,
                    must_contain="not found")
    ctx.expect_fail("hostnet apply (unknown lab)", "lab", "hostnet", "apply", ABSENT,
                    must_contain="not found")
    ctx.expect_fail("nfs attach (unknown lab)", "lab", "nfs", "attach", ABSENT,
                    must_contain="not found")
    ctx.expect_fail("nfs detach (unknown lab)", "lab", "nfs", "detach", ABSENT, "--yes",
                    must_contain="not found")
    ctx.expect_fail("nfs status (unknown lab)", "lab", "nfs", "status", ABSENT,
                    must_contain="not found")
    ctx.expect_fail("qdevice add (unknown lab)", "lab", "qdevice", "add", ABSENT,
                    must_contain="not found")
    ctx.expect_fail("qdevice remove (unknown lab)", "lab", "qdevice", "remove", ABSENT,
                    must_contain="not found")
    ctx.expect_fail("scale (unknown lab)", "lab", "scale", ABSENT, "--yes",
                    must_contain="not found")
    ctx.expect_fail("sdn apply (unknown lab)", "lab", "sdn", "apply", ABSENT,
                    must_contain="not found")
    ctx.expect_fail("sdn vlan apply (unknown lab)", "lab", "sdn", "vlan", "apply", ABSENT,
                    must_contain="not found")

    # `context rm` deliberately accepts a name no lab defines, so the only
    # failure it can be driven into without deleting a real context is the
    # charset guard that keeps a lab name off any remote command line.
    ctx.expect_fail("context rm (bad lab name)", "lab", "context", "rm", "bad name!",
                    must_contain="charset")


def _deferred_mutations(ctx: Ctx) -> None:
    ctx.defer(
        "create",
        "provisions SDN zone/vnet/subnet, storage, pool, and a VM on the "
        "cluster; needs the dedicated lab-pmx destructive test lab as the "
        "standing target",
        "pmx lab create pmx --node <node>",
        isolation=True,
    )
    ctx.defer(
        "destroy",
        "deletes a lab's VM, pool, storage, and SDN resources; needs the "
        "dedicated lab-pmx destructive test lab as the standing target",
        "pmx lab destroy pmx --yes",
        isolation=True,
    )
    ctx.defer(
        "start",
        "powers on a lab VM; needs the dedicated lab-pmx destructive test "
        "lab as the standing target",
        "pmx lab start pmx",
        isolation=True,
    )
    ctx.defer(
        "stop",
        "hard powers off a lab VM; needs the dedicated lab-pmx destructive "
        "test lab as the standing target",
        "pmx lab stop pmx",
        isolation=True,
    )
    ctx.defer(
        "net apply",
        "reconciles and commits cluster-wide SDN configuration; needs the "
        "dedicated lab-pmx destructive test lab as the standing target",
        "pmx lab net apply pmx",
        isolation=True,
    )
    ctx.defer(
        "access grant",
        "creates a pve user and grants pool ACLs cluster-wide; needs the "
        "dedicated lab-pmx destructive test lab as the standing target",
        "pmx lab access grant pmx member@pve",
        isolation=True,
    )
    ctx.defer(
        "quota set",
        "runs `zfs set refquota` over ssh on the real host's dataset; no PVE "
        "API endpoint exists for it",
        "pmx lab quota set pmx --refquota-gb 600 --yes",
        isolation=True,
    )
    ctx.defer(
        "cluster init",
        "runs `pvecm create` over ssh on a lab's node 0, forming a corosync "
        "cluster; needs the dedicated lab-pmx destructive test lab as the "
        "standing target",
        "pmx lab cluster init pmx",
        isolation=True,
    )
    ctx.defer(
        "cluster join",
        "runs `pvecm add` over ssh on each non-zero lab node, joining it to "
        "node 0's cluster; needs the dedicated lab-pmx destructive test lab as "
        "the standing target",
        "pmx lab cluster join pmx",
        isolation=True,
    )
    ctx.defer(
        "cluster status",
        "reads corosync quorum and link state over ssh on a lab's nodes, so it "
        "needs a provisioned and running lab; needs the dedicated lab-pmx "
        "destructive test lab as the standing target",
        "pmx lab cluster status pmx",
        isolation=True,
    )
    ctx.defer(
        "ceph install",
        "installs the Ceph packages over ssh on each of a lab's nodes; needs "
        "the dedicated lab-pmx destructive test lab as the standing target",
        "pmx lab ceph install pmx",
        isolation=True,
    )
    ctx.defer(
        "context sync",
        "creates the pmx@pve token user and ACL on a lab's nested cluster and "
        "rewrites the local `lab-<name>` context and its keychain secret; needs "
        "the dedicated lab-pmx destructive test lab as the standing target",
        "pmx lab context sync pmx",
        isolation=True,
    )
    ctx.defer(
        "context rm",
        "deletes a `lab-<name>` context from the operator's own config.yml and "
        "its keychain secret; needs the dedicated lab-pmx destructive test lab "
        "as the standing target",
        "pmx lab context rm pmx",
        isolation=True,
    )
    ctx.defer(
        "hostnet apply",
        "rewrites the outer node's bridge and bond configuration, which can "
        "sever the suite's own connection to it; needs the dedicated lab-pmx "
        "destructive test lab as the standing target",
        "pmx lab hostnet apply pmx",
        isolation=True,
    )
    ctx.defer(
        "nfs attach",
        "creates ZFS datasets, quotas, and sharenfs ACLs on the outer node, "
        "opens 2049/111 in its firewall, and adds pvesm storage entries on the "
        "lab; needs the dedicated lab-pmx destructive test lab as the standing "
        "target",
        "pmx lab nfs attach pmx",
        isolation=True,
    )
    ctx.defer(
        "nfs detach",
        "removes a lab's pvesm storage entries and can narrow an aliased "
        "export's sharenfs ACL; needs the dedicated lab-pmx destructive test "
        "lab as the standing target",
        "pmx lab nfs detach pmx --yes",
        isolation=True,
    )
    ctx.defer(
        "nfs status",
        "runs `pvesm status` over ssh on a lab's node 0, so it needs a "
        "provisioned and running lab; needs the dedicated lab-pmx destructive "
        "test lab as the standing target",
        "pmx lab nfs status pmx",
        isolation=True,
    )
    ctx.defer(
        "qdevice add",
        "provisions a QDevice VM and runs `pvecm qdevice setup` against the "
        "lab's cluster; needs the dedicated lab-pmx destructive test lab as the "
        "standing target",
        "pmx lab qdevice add pmx",
        isolation=True,
    )
    ctx.defer(
        "qdevice remove",
        "tears the QDevice out of the lab's cluster and destroys its VM; needs "
        "the dedicated lab-pmx destructive test lab as the standing target",
        "pmx lab qdevice remove pmx",
        isolation=True,
    )
    ctx.defer(
        "scale",
        "creates or destroys lab node VMs, joins or removes them from the "
        "cluster, and moves the QDevice; needs the dedicated lab-pmx "
        "destructive test lab as the standing target",
        "pmx lab scale pmx --yes",
        isolation=True,
    )
    ctx.defer(
        "sdn apply",
        "reconciles the inner VXLAN zone, vnet, and subnet inside a lab's own "
        "nested cluster; needs the dedicated lab-pmx destructive test lab as "
        "the standing target",
        "pmx lab sdn apply pmx",
        isolation=True,
    )
    ctx.defer(
        "sdn vlan apply",
        "reconciles the inner vlan-type zone and its vnets inside a lab's own "
        "nested cluster; needs the dedicated lab-pmx destructive test lab as "
        "the standing target",
        "pmx lab sdn vlan apply pmx",
        isolation=True,
    )
