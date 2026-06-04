"""Registry of command-tree e2e modules.

Each module exposes `NAME`, `DESCRIPTION`, and `run(ctx)`. Order here is the
display/run order; trees are independent and run on separate threads.
"""

from __future__ import annotations

from types import ModuleType

from . import (
    access,
    api,
    cluster,
    init,
    lxc,
    negative,
    node,
    pool,
    qemu,
    sdn,
    storage,
    task,
    version,
)

_MODULES: list[ModuleType] = [
    version,
    cluster,
    node,
    qemu,
    lxc,
    storage,
    pool,
    sdn,
    task,
    access,
    api,
    init,
    negative,
]

TREES: dict[str, ModuleType] = {m.NAME: m for m in _MODULES}


def names() -> list[str]:
    return list(TREES.keys())
