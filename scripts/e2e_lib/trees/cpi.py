"""cpi: read-only audits of what the BOSH Proxmox VE CPI manages.

`cpi disk-audit` only issues GET requests, so it runs against any lab.
It exits 9 when it finds free-floating disks, which is a verdict about the
cluster rather than a failure of the command, so both 0 and 9 pass.
"""

from __future__ import annotations

from ..context import CmdResult, Ctx

NAME = "cpi"
DESCRIPTION = "Audit the persistent disks the BOSH CPI manages"

# The document keys, in the order the audit emits them.
AUDIT_KEYS = [
    "host", "port", "disk_band", "parker_band", "summary", "disks", "parkers",
    "skipped_storages", "multiply_referenced",
    "multiply_referenced_unreadable_vmids", "multiply_referenced_visibility",
    "multiply_referenced_complete",
]
SUMMARY_KEYS = [
    "total", "attached", "parked", "free_floating", "unknown",
    "multiply_referenced",
]


def run(ctx: Ctx) -> None:
    def is_audit_document(res: CmdResult) -> str | None:
        data = res.json()
        if not isinstance(data, dict):
            return "expected a JSON object"
        if list(data) != AUDIT_KEYS:
            return f"keys {list(data)} differ from {AUDIT_KEYS}"
        summary = data["summary"]
        if not isinstance(summary, dict) or list(summary) != SUMMARY_KEYS:
            return f"summary keys differ from {SUMMARY_KEYS}"
        if not isinstance(data["disks"], list):
            return "disks is not an array"
        floating = summary["free_floating"]
        if (res.rc == 9) != (floating > 0):
            return f"exit {res.rc} does not match free_floating={floating}"
        return None

    ctx.check("disk-audit", "cpi", "disk-audit",
              ok_rcs=(0, 9), validate=is_audit_document)
