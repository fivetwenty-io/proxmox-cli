"""lab: config-driven nested lab lifecycle (pmx persona only).

The read-only verbs (`list`, `status`) run live against the configured
cluster. The config verbs (`config init/add/show`) operate on a throwaway
scratch `--config` file, never the real config.yml. Every mutating verb
(create, destroy, start, stop, net apply, access grant, quota set) has its
error contract exercised with an unresolvable lab name — each fails during
config resolution, before any API call — and its happy path deferred: those
verbs provision/destroy SDN, storage, pools, and VMs, and need the dedicated
lab-pmx destructive test lab as a standing target.
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
