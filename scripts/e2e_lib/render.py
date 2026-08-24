"""Render audits: the two rendering defect classes, checked on every leaf.

Both were found by a manual sweep and neither was visible from `-o json`, so
they live here and run against the table rendering of every read-only check.

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
"""

from __future__ import annotations

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
    "context list": {"DEFAULT NODE", "DEFAULT OUTPUT"},
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
    "pbs datastore list": {"COMMENT"},
    "pbs user ls": {"EXPIRE", "FIRSTNAME", "LASTNAME", "EMAIL"},
    # The PBS disk endpoint reports a model but no separate vendor.
    "pbs node disks ls": {"VENDOR"},
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
}

# READ_VERBS gates the audit's extra invocation. A check is re-run as a table
# only when its command path contains one of these, so a tree that drives a
# scratch config through `context add` is never replayed.
READ_VERBS = frozenset({
    "list", "ls", "show", "get", "status", "config", "info", "tree", "current",
    "versions", "version", "members", "describe", "usage", "log", "logs",
    "metadata", "df", "content", "capabilities", "report", "dns", "hosts",
    "netstat", "rrddata", "subscription", "whoami", "permissions", "aplinfo",
})


def is_read_only(leaf: str) -> bool:
    """Whether the audit may re-run leaf for its table rendering."""
    return any(tok in READ_VERBS for tok in leaf.split())


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

    The renderer spaces a header out on its separators ("WEB-URL" is laid out
    as "WEB - URL") and may shorten it with an ellipsis, so an allowlist
    written against the rendered text would be unreadable and would break
    whenever a column changes width. Both sides are normalised to the bare
    letters and digits instead.
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


def audit(command: str, out: str) -> str:
    """Both audits over one table rendering. Returns "" when it is clean."""
    return width_violation(out) or empty_columns(command, out)
