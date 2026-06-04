"""Shared types and lab-isolation constants for the pve e2e harness.

This module is import-only (no third-party dependencies) so it stays usable
from a `uv run --script` entry point and from plain `python3 -m`.
"""

from __future__ import annotations

import enum
from dataclasses import dataclass, field

# --- Lab isolation contract -------------------------------------------------
#
# Resource-creating lifecycle tests (deferred — see each tree's DEFERRED list)
# MUST keep their resources off the lab's host 172.x management network and
# tag everything so concurrent efforts on the shared lab are never disturbed.
# These constants are the single source of truth for that contract; the
# happy-path read-only checks never create anything, but they print this block
# via `e2e --list` so the requirements are discoverable.


class Isolation:
    # Every created resource carries this tag (qemu/lxc `--tags`, pool grouping).
    TAG = "pve-cli"
    # Created VMs/CTs join this resource pool for one-glance identification.
    POOL = "pve-cli"
    # Names are prefixed so a stray resource is obviously ours.
    NAME_PREFIX = "pve-cli-"
    # A dedicated SDN simple zone + vnet keeps test NICs off the host bridge and
    # off the 172.x management subnet entirely.
    SDN_ZONE = "pvecli"          # PVE zone id: <=8 chars, alnum
    SDN_VNET = "pvecli0"
    # Private subnet within the 172.16/12 space, deliberately distinct from the
    # bosh-pve-cpi networks (172.16.5.0/24, 172.31.0.0/24) and off the lab host
    # management network.
    SDN_SUBNET = "172.30.0.0/24"
    SDN_GATEWAY = "172.30.0.1"


class Status(enum.Enum):
    PASS = "PASS"
    FAIL = "FAIL"
    SKIP = "SKIP"


@dataclass
class Result:
    """Outcome of a single happy-path check."""

    tree: str
    name: str
    status: Status
    command: str = ""
    detail: str = ""
    duration_s: float = 0.0

    @property
    def ok(self) -> bool:
        return self.status is not Status.FAIL


@dataclass
class Deferred:
    """A destructive / mutating operation intentionally NOT run by the harness.

    Recorded so coverage gaps are explicit rather than silent. `isolation`
    marks operations that create lab resources and therefore must honour the
    `Isolation` contract when they are eventually implemented.
    """

    tree: str
    name: str
    reason: str
    command: str = ""
    isolation: bool = False
    # True when the mutate phase (`e2e --mutate` / `scripts/lifecycle`) actually
    # exercises this verb live. Such entries are not "gaps": the read-only sweep
    # skips them, but `--mutate` runs them — so the deferred report suppresses
    # them when the mutate phase covering this tree is active.
    live_covered: bool = False


@dataclass
class TreeReport:
    name: str
    results: list[Result] = field(default_factory=list)
    deferred: list[Deferred] = field(default_factory=list)
    error: str = ""

    @property
    def failed(self) -> int:
        return sum(1 for r in self.results if r.status is Status.FAIL)

    @property
    def passed(self) -> int:
        return sum(1 for r in self.results if r.status is Status.PASS)

    @property
    def skipped(self) -> int:
        return sum(1 for r in self.results if r.status is Status.SKIP)
