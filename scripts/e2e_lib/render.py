"""Render audits: the two rendering defect classes, checked on every leaf.

Both were found by a manual sweep and neither was visible from `-o json`, so
they live here and run against the table rendering of every read-only check.

Two callers share them. The read-only sweep re-runs each passing check as a
table (`Ctx.audit_render`). The destructive suites audit each step's own
output (`Runner._audit`), which is the only place the argument-taking
read-only leaves are reached with a real argument: `pve cluster ha rule get
<sid>` needs a rule to exist, and only a lifecycle run makes one.

Width
  `pmx pve node ceph status` wrote 3.95 MB and a 515,739-column line, because
  the generic renderer marshalled a nested payload into one cell and
  tablewriter padded every other row to it. pmx now bounds the layout; this
  asserts no leaf can reintroduce an unbounded one.

Always-empty columns
  A struct tag that does not match the field the API sends decodes to the zero
  value, so the column renders blank for every row and the data is silently
  lost. Four of those shipped (PBS task TYPE/ID, disk TYPE/HEALTH, service
  ACTIVE-STATE, apt RUNNING-KERNEL). A column blank in every row of a
  many-row table is the signature.

Placeholder cells
  A value the server never sent, rendered through fmt's %v, reaches the table
  as the literal "<nil>" and reads as something PVE actually said. That form
  also hides the column from the always-empty check, because the cell is not
  blank: `pve lxc config pending` printed "<nil>" down its whole
  PENDING-VALUE column and the empty-column audit saw a populated column.
"""

from __future__ import annotations

import json

# BUDGET is the terminal width the sweep pins through $COLUMNS. Pinning it
# makes the width assertion reproducible without a pty: pmx prefers $COLUMNS
# over the tty size precisely so a sweep can do this.
BUDGET = 120

# MIN_ROWS is the table size below which an all-blank column proves nothing:
# a two-row lab table can legitimately leave a column unset.
MIN_ROWS = 3

# EMPTY_COLUMN_ALLOWLIST names columns that are legitimately blank in the lab
# rather than misdecoded. The key is the pmx command with its global flags
# removed; the value is the set of header names exempted for it.
#
# Add to it only after confirming the field really is unset on the server, by
# reading the same leaf with -o json.
EMPTY_COLUMN_ALLOWLIST: dict[str, set[str]] = {
    "context ls": {"DEFAULT NODE", "DEFAULT OUTPUT"},
    "pve access user list": {"FIRSTNAME", "LASTNAME", "EMAIL", "COMMENT", "GROUPS"},
    "pve access group list": {"COMMENT", "MEMBERS"},
    "pve access role list": {"COMMENT"},
    "pve access domain list": {"COMMENT"},
    "pve cluster status": {"LEVEL"},
    "pve cluster resources": {"POOL", "TAGS"},
    # No host firewall rule in the lab restricts a destination.
    "pve node firewall rules list": {"DEST"},
    "pve pool list": {"COMMENT"},
    # /cluster/resources carries no pid; only the per-node list does.
    "pve qemu list": {"PID"},
    # No lab guest trips a risk, and no lab guest sets explicit CPU flags.
    "pve qemu security list": {"RISKS"},
    "pve qemu security cpu-flags show": {"STATE"},
    "pve storage list": {"COMMENT", "NODES"},
    # A zfspool option declares neither a default nor an enumeration.
    "pve storage describe": {"DEFAULT", "VALUES"},
    # No lab SDN zone is pinned to a node.
    "pve sdn zone list": {"NODES"},
    # A lab whose VMs live on another cluster reports absent, with no VMID
    # or node to name.
    "lab list": {"VMID", "NODE"},
    "lab status": {"VMID", "PVE NODE"},
    "pbs datastore ls": {"COMMENT"},
    "pbs user ls": {"EXPIRE", "FIRSTNAME", "LASTNAME", "EMAIL"},
    # The PBS disk endpoint reports a model but no separate vendor.
    "pbs node disks ls": {"VENDOR"},
    # Port and region are optional on an S3 endpoint; the lab endpoints leave both unset.
    "pbs s3 ls": {"PORT", "REGION"},
    # Every remote SDN zone in the lab is `simple`, so the vxlan and
    # controller columns have nothing to carry.
    "pdm sdn vnet ls": {"STATE"},
    # No remote in the lab declares a web URL, and PDM leaves worker_id unset
    # on its own node tasks and on the remote task cache.
    "pdm remote ls": {"WEB-URL"},
    "pdm remote task ls": {"WORKER-ID"},
    "pdm node task ls": {"ID"},
    # The lab has no subscription key, so every node reports notfound.
    "pdm subscription node-status": {
        "ASSIGNED-KEY", "CURRENT-KEY", "NEXT-DUE-DATE", "CHECK-TIME",
    },
    "pdm sdn zone ls": {"STATE", "CONTROLLER", "NODES", "VRF-VXLAN"},
    # PVE sets a pending entry's delete flag only on a key staged for removal,
    # and no lab guest has one staged. Confirmed against -o json: the payload
    # carries no delete key on any of the 25 rows. The native
    # `pve qemu config pending` renders no such column, so only the PDM proxy
    # needs the exemption.
    "pdm pve qemu pending": {"DELETE"},
    # Same field, same reason, on the native leaves: PVE reports a pending
    # value and a delete flag only for a key that has one staged, and a lab
    # guest between two config writes has none. Confirmed against -o json.
    "pve qemu config pending": {"PENDING-VALUE"},
    "pve lxc config pending": {"PENDING-VALUE"},
    "pve qemu cloudinit pending": {"PENDING", "DELETE"},
    # The Ceph daemon tables note only what is wrong with a daemon (a missing
    # systemd unit or data directory), so a healthy cluster leaves the column
    # empty on every row.
    "pve node ceph mon list": {"NOTES"},
    "pve node ceph mgr list": {"NOTES"},
    "pve node ceph mds list": {"NOTES"},
}

# ROOT_COMMANDS are pmx's top-level command groups, which is where a command
# path starts. Ctx.leaf reads it to tell a global flag's value ("--config
# /tmp/x") from the command, since the two are indistinguishable by shape.
ROOT_COMMANDS = frozenset({
    "api", "auth", "completion", "context", "help", "init", "lab", "logs",
    "pbs", "pdm", "pve", "rsync", "ssh", "version",
})

# READ_VERBS gates the audit's extra invocation. A check is re-run as a table
# only when its command path contains one of these, so a tree that drives a
# scratch config through `context add` is never replayed.
READ_VERBS = frozenset({
    "list", "ls", "show", "get", "status", "config", "info", "tree", "current",
    "versions", "version", "members", "describe", "usage", "log", "logs",
    "metadata", "df", "content", "capabilities", "report", "dns", "hosts",
    "netstat", "rrddata", "subscription", "whoami", "permissions", "aplinfo",
    # Read verbs that name what they return rather than the act of reading.
    # Every leaf carrying one of these is read-only; checked against the leaf
    # set before adding, because a token here also decides whether a check may
    # be re-run.
    "pending", "bridges", "ip-vrf", "mac-vrf", "interfaces", "neighbors",
    "routes", "releases", "buckets",
})


def is_read_only(leaf: str) -> bool:
    """Whether the audit may re-run leaf for its table rendering."""
    return any(tok in READ_VERBS for tok in leaf.split())


def command_path(args: tuple[str, ...] | list[str]) -> str:
    """The pmx command path in args, global flags and their values dropped.

    This is the key EMPTY_COLUMN_ALLOWLIST is written against, so that an
    exemption reads as the command an operator would type.

    The path starts at the first token naming a top-level pmx command, because
    a caller may lead with a global flag: the scratch-config checks pass
    `--config <path>` first, and reading the path from the first non-flag token
    would make `<path>` the command and leave those leaves un-audited.
    """
    path: list[str] = []
    for a in args:
        if not path:
            if a in ROOT_COMMANDS:
                path.append(a)
            continue
        if a.startswith("-"):
            break
        path.append(a)
    return " ".join(path)


def table_lines(out: str) -> list[str]:
    """The lines of the first box table in out, rules included; [] if none.

    Locating the table rather than scanning the whole output matters: a
    `node report` streams `systemctl status`, whose process tree draws with
    the same box-drawing runes a table does.
    """
    lines = out.splitlines()
    top = -1
    for i, line in enumerate(lines):
        t = line.strip()
        if t.startswith("┌") and t.endswith("┐"):
            top = i
            break
    if top < 0 or top + 2 >= len(lines):
        return []
    if "│" not in lines[top + 1]:
        return []
    sep = lines[top + 2].strip()
    if not (sep.startswith("├") and sep.endswith("┤")):
        return []

    out_lines = lines[top:top + 3]
    for line in lines[top + 3:]:
        out_lines.append(line)
        if line.strip().startswith("└"):
            break
    return out_lines


def _cells(line: str) -> list[str]:
    return [c.strip() for c in line.strip().strip("│").split("│")]


def parse_table(out: str) -> tuple[list[str], list[list[str]]]:
    """Split the first box table in out into its header and its data rows.

    Returns ([], []) when out holds no table, which is how a plain rendering,
    a streamed report, or `--help` opts itself out.
    """
    lines = table_lines(out)
    if not lines:
        return [], []
    header = _cells(lines[1])
    rows = [_cells(l) for l in lines[3:] if "│" in l]
    return header, rows


# A column is never shrunk below FLOOR runes, and each costs FRAME more in
# borders and padding, with EDGE for the closing border. These mirror
# minColumnRunes, tableFrameRunes, and tableFrameEdgeRunes in
# internal/output/width.go: a table with enough columns cannot fit the budget
# without becoming unreadable, and is allowed to exceed it.
FLOOR = 8
FRAME = 3
EDGE = 1


def allowance(columns: int, budget: int = BUDGET) -> int:
    """The widest a table of this many columns is allowed to render."""
    return max(budget, columns * (FLOOR + FRAME) + EDGE)


def width_violation(out: str, budget: int = BUDGET) -> str:
    """The widest line of a box table when it overruns its allowance; else "".

    Output that is not a box table is not this check's business: `node report`
    streams a server-generated text report, and `--help` is cobra's.
    """
    lines = table_lines(out)
    if not lines:
        return ""
    header, _ = parse_table(out)
    allowed = allowance(len(header), budget)
    widest = max((len(line.rstrip()) for line in lines), default=0)
    if widest <= allowed:
        return ""
    if allowed > budget:
        return (f"rendered {widest} columns wide; {len(header)} columns cannot "
                f"fit {budget}, but {allowed} is the floor")
    return f"rendered {widest} columns wide, budget is {budget}"


def allowlisted(command: str) -> set[str]:
    """The exempt columns for command, matched by command path prefix.

    Prefix matching is what lets one entry cover a leaf that takes an
    argument: "pve qemu security cpu-flags show" exempts the column for every
    VMID rather than for one lab's.
    """
    out: set[str] = set()
    for key, columns in EMPTY_COLUMN_ALLOWLIST.items():
        if command == key or command.startswith(key + " "):
            out |= columns
    return out


def normalize_header(name: str) -> str:
    """A header reduced to what survives both rendering and shortening.

    The renderer may shorten a header with an ellipsis, so an allowlist
    written against the rendered text would break whenever a column changes
    width. Both sides are normalised to the bare letters and digits instead,
    which also keeps the allowlist indifferent to a header's separators
    (e.g. "WEB-URL").
    """
    return "".join(c for c in name.upper() if c.isalnum())


def is_allowed(rendered: str, allowed: set[str]) -> bool:
    """Whether a rendered header names an allowlisted column.

    A shortened header is a prefix of the name it was cut from, so prefix
    matching is what lets one entry cover a column at any width.
    """
    got = normalize_header(rendered)
    return any(got == name or name.startswith(got) for name in allowed)


def empty_columns(command: str, out: str, min_rows: int = MIN_ROWS) -> str:
    """Columns blank in every row of a many-row table, described; else "".

    A KEY/VALUE rendering of a single object is skipped: its VALUE column is
    per-key, so "blank in every row" means nothing there.
    """
    header, rows = parse_table(out)
    if not header or len(rows) < min_rows:
        return ""
    if [h.upper() for h in header] == ["KEY", "VALUE"]:
        return ""

    allowed = {normalize_header(n) for n in allowlisted(command)}
    blank = []
    for i, name in enumerate(header):
        if not name or is_allowed(name, allowed):
            continue
        if all(i >= len(r) or not r[i] for r in rows):
            blank.append(name)
    if not blank:
        return ""
    return (
        f"column(s) blank in all {len(rows)} rows: {', '.join(blank)} "
        "(struct tag mismatch, or add to EMPTY_COLUMN_ALLOWLIST)"
    )


# PLACEHOLDERS are the renderings of a Go value that no server ever sends.
# Each is what fmt's %v prints for a type that reached a table cell without
# being turned into text first. A pointer's "0x..." is deliberately not here:
# PVE reports PCI vendor and device ids in exactly that form.
PLACEHOLDERS = ("<nil>", "%!", "map[", "[]interface {}", "&{")


def placeholder_cells(out: str) -> str:
    """Cells rendering a Go value verbatim rather than a server value; else "".

    Unlike the empty-column check this fires on a single cell, because one is
    already a defect: there is no lab state under which PVE sends "<nil>".
    """
    header, rows = parse_table(out)
    if not header:
        return ""
    found: list[str] = []
    for row in rows:
        for i, cell in enumerate(row):
            if not cell.startswith(PLACEHOLDERS):
                continue
            name = header[i] if i < len(header) else f"column {i}"
            desc = f"{name}={cell}"
            if desc not in found:
                found.append(desc)
    if not found:
        return ""
    return (f"cell(s) rendering a Go value, not a server value: "
            f"{', '.join(found)} (render the field through cli.StringifyValue)")


def audit(command: str, out: str) -> str:
    """Both audits over one table rendering. Returns "" when it is clean."""
    return (width_violation(out) or placeholder_cells(out)
            or empty_columns(command, out))


# ---------------------------------------------------------------------------
# JSON versus YAML
#
# The two structured formats are one document in two syntaxes: renderYAML and
# renderJSON in internal/output take the same Result fields in the same
# priority, and the YAML side re-encodes Result.Raw through JSON so that
# holds for raw SDK payloads too. That equivalence went unasserted, and
# `pmx pve access permissions -o yaml` shipped printing the ASCII codes of the
# JSON document as a list of integers (go-yaml renders a []byte that way, and
# json.RawMessage is one). Comparing the two renderings on every read-only
# check is what catches the next such divergence, wherever it lands.
#
# The two renderings come from two invocations seconds apart, so their values
# are not comparable: a list comes back from PVE in a different order each
# time, and a counter such as disk usage or uptime moves between the calls.
# What the renderer owns is the shape, so that is what is compared: the kind
# of value (object, array, scalar) at every path, with an array's items
# folded into one shape by unioning their keys. A scalar that comes back as
# an array of integers, or an object that comes back as an array, is the
# defect; scalar values and their fidelity are pinned by the Go tests in
# internal/output, which see one Result rendered both ways.
#
# YAML is loaded with PyYAML's BaseLoader, which keeps every scalar a string,
# so YAML 1.1's ideas about "yes", "0755", or a timestamp never bend a scalar
# into something else. PyYAML is a declared dependency of the entry scripts;
# without it the byte-list signature is still asserted, so the check
# degrades rather than disappears.
# ---------------------------------------------------------------------------


def byte_sequence(out: str) -> bool:
    """Whether out is a YAML document that is one flat list of byte codes.

    The signature is a top-level block sequence whose items are all bare
    integers and whose first item is "{" or "[": a JSON document that reached
    go-yaml as []byte. A legitimate list of integers never starts with 123 or
    91 by accident often enough to matter, and the structural comparison
    covers that case exactly when PyYAML is present.
    """
    lines = [ln for ln in out.splitlines() if ln.strip()]
    if len(lines) < 2:
        return False
    items = []
    for ln in lines:
        if not ln.startswith("- ") or not ln[2:].strip().isdigit():
            return False
        items.append(int(ln[2:].strip()))
    return items[0] in (ord("{"), ord("["))


_MISSING = object()


def _load_yaml(text: str):
    """The YAML document with every scalar a string, or _MISSING without PyYAML."""
    try:
        import yaml  # type: ignore[import-not-found]
    except ImportError:
        return _MISSING
    return yaml.load(text, Loader=yaml.BaseLoader)


# A shape is one of:
#   ("object", {key: shape})
#   ("array", shape | None)     None for an empty array, which matches any
#   ("scalar",)
# and an array's items are folded into one shape by `_merge`.


def shape(v) -> tuple:
    """The shape of a parsed document: kinds at every path, values dropped."""
    if isinstance(v, dict):
        return ("object", {k: shape(x) for k, x in v.items()})
    if isinstance(v, list):
        merged = None
        for item in v:
            merged = _merge(merged, shape(item))
        return ("array", merged)
    return ("scalar",)


def _merge(a, b):
    """One shape covering both a and b, for an array's items."""
    if a is None:
        return b
    if b is None:
        return a
    if a[0] != b[0]:
        return ("mixed",)
    if a[0] == "object":
        keys = dict(a[1])
        for k, sub in b[1].items():
            keys[k] = _merge(keys.get(k), sub)
        return ("object", keys)
    if a[0] == "array":
        return ("array", _merge(a[1], b[1]))
    return a


def _first_diff(j: tuple, y: tuple, path: str) -> str:
    """The first path at which shapes j (json) and y (yaml) differ; else ""."""
    if j[0] != y[0]:
        return f"{path}: json {j[0]}, yaml {y[0]}"
    if j[0] == "object":
        missing = [k for k in j[1] if k not in y[1]]
        extra = [k for k in y[1] if k not in j[1]]
        if missing or extra:
            return (f"{path}: keys differ (json only: {missing or '-'}, "
                    f"yaml only: {extra or '-'})")
        for k, sub in j[1].items():
            d = _first_diff(sub, y[1][k], f"{path}.{k}")
            if d:
                return d
        return ""
    if j[0] == "array":
        if j[1] is None or y[1] is None:
            return ""
        return _first_diff(j[1], y[1], f"{path}[]")
    return ""


def yaml_mismatch(json_out: str, yaml_out: str) -> str:
    """How the yaml rendering's shape diverges from the json one's; else "".

    Both renderings are of the same command. A yaml document that is a flat
    list of byte codes is named as such, since that is the shape the bug
    takes; anything else is reported as the first path whose kind or key set
    differs, with the remedy the renderer owner needs.
    """
    if byte_sequence(yaml_out):
        return ("yaml rendered the JSON document as a list of byte codes "
                "(a []byte reached go-yaml; route Result.Raw through "
                "yamlValueFromJSON in internal/output/yaml.go)")
    try:
        j = json.loads(json_out)
    except json.JSONDecodeError as exc:
        return f"json output does not parse: {exc}"
    y = _load_yaml(yaml_out)
    if y is _MISSING:
        return ""
    d = _first_diff(shape(j), shape(y), "$")
    if not d:
        return ""
    return f"yaml differs from json at {d} (renderYAML and renderJSON must emit one document)"
