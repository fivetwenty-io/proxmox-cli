"""pve CLI end-to-end happy-path harness.

Exercises every command tree's read-only commands against a configured target
(default: `lab`). Destructive / mutating operations are recorded as deferred
rather than executed; see `model.Isolation` for the contract such operations
must honour against the shared lab.
"""

from __future__ import annotations

from .model import Deferred, Isolation, Result, Status, TreeReport

__all__ = ["Deferred", "Isolation", "Result", "Status", "TreeReport"]
