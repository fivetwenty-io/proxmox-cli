#!/usr/bin/env python3
"""Generate the storage type→options mapping from the pve-storage plugin sources.

The PVE API schema for /storage is flat: POST /storage lists every option of
every storage type in one property set, with no record of which types accept
which options. The authoritative mapping lives in the Perl storage plugins —
each `PVE::Storage::<X>Plugin` returns its accepted options (and whether each
is create-only "fixed" or required) from `sub options`. This script parses a
pve-storage checkout and emits that mapping as Go data for
`internal/cli/storage`, so `pve storage describe --type <t>` and the
type-aware `get --defaults` can filter the flat schema honestly.

Every extracted option name is cross-checked against the vendored apidoc's
POST /storage properties; an unknown name aborts generation so plugin/schema
drift is caught at regen time, not at runtime.

Usage (from the repo root):

    git clone https://git.proxmox.com/git/pve-storage.git /tmp/pve-storage
    git -C /tmp/pve-storage checkout <tag-or-commit matching the lab version>
    python3 scripts/storage_type_options.py --pve-storage /tmp/pve-storage

The pinned source version is recorded in the generated header (from
debian/changelog) so the provenance of the committed file is auditable.
"""
from __future__ import annotations

import argparse
import json
import os
import re
import subprocess
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
DEFAULT_OUT = os.path.join(ROOT, "internal", "cli", "storage", "type_options_gen.go")

# Meta-parameters of the POST /storage endpoint itself, never per-type options.
META = {"storage", "type"}


def parse_plugin(path: str) -> tuple[str, dict[str, dict[str, bool]]] | None:
    """Return (type, {option: {fixed, required}}) for one plugin file."""
    src = open(path, encoding="utf-8").read()

    m = re.search(r"^sub type\s*\{.*?return\s+'([a-z0-9]+)';", src, re.M | re.S)
    if not m:
        return None
    stype = m.group(1)

    m = re.search(r"^sub options\s*\{(.*?)^\}", src, re.M | re.S)
    if not m:
        sys.exit(f"storage_type_options: {path}: no `sub options` body")
    body = re.sub(r"#.*", "", m.group(1))  # strip comments

    options: dict[str, dict[str, bool]] = {}
    for name_q, name_b, attrs in re.findall(
        r"(?:'([^']+)'|([A-Za-z0-9_-]+))\s*=>\s*\{([^}]*)\}", body
    ):
        name = name_q or name_b
        options[name] = {
            "fixed": bool(re.search(r"\bfixed\b", attrs)),
            "required": not re.search(r"\boptional\b", attrs),
        }
    if not options:
        sys.exit(f"storage_type_options: {path}: empty options hash")
    return stype, options


def apidoc_storage_properties(apidoc_path: str) -> set[str]:
    """Return the POST /storage property names from the vendored apidoc."""
    doc = json.load(open(apidoc_path, encoding="utf-8"))

    def find(nodes, path):
        for n in nodes:
            if n.get("path") == path:
                return n
            hit = find(n.get("children", []), path)
            if hit:
                return hit
        return None

    node = find(doc, "/storage")
    if node is None:
        sys.exit("storage_type_options: /storage not found in apidoc")
    return set(node["info"]["POST"]["parameters"]["properties"])


def source_version(checkout: str) -> str:
    """Return 'name (version) @ commit' from the checkout's debian/changelog."""
    head = open(os.path.join(checkout, "debian", "changelog"), encoding="utf-8").readline()
    m = re.match(r"(\S+)\s+\(([^)]+)\)", head)
    label = f"{m.group(1)} {m.group(2)}" if m else "pve-storage (unknown version)"
    try:
        commit = subprocess.run(
            ["git", "-C", checkout, "rev-parse", "--short", "HEAD"],
            capture_output=True, text=True, check=True,
        ).stdout.strip()
        label += f" @ {commit}"
    except (subprocess.CalledProcessError, OSError):
        pass
    return label


def main() -> None:
    ap = argparse.ArgumentParser()
    ap.add_argument("--pve-storage", required=True, help="path to a pve-storage checkout")
    ap.add_argument("--apidoc", default=None,
                    help="path to apidoc.json (default: resolve the pve-apiclient-go module copy)")
    ap.add_argument("--out", default=DEFAULT_OUT)
    args = ap.parse_args()

    apidoc = args.apidoc
    if apidoc is None:
        mod = subprocess.run(
            ["go", "list", "-m", "-f", "{{.Dir}}", "github.com/fivetwenty-io/pve-apiclient-go/v3"],
            capture_output=True, text=True, check=True, cwd=ROOT,
        ).stdout.strip()
        apidoc = os.path.join(mod, "_data", "apidoc.json")
    known = apidoc_storage_properties(apidoc) - META

    plugin_dir = os.path.join(args.pve_storage, "src", "PVE", "Storage")
    mapping: dict[str, dict[str, dict[str, bool]]] = {}
    for fn in sorted(os.listdir(plugin_dir)):
        if not fn.endswith("Plugin.pm") or fn == "Plugin.pm":
            continue
        parsed = parse_plugin(os.path.join(plugin_dir, fn))
        if parsed is None:
            sys.exit(f"storage_type_options: {fn}: no `sub type` — new abstract base? handle explicitly")
        stype, options = parsed
        unknown = sorted(set(options) - known)
        if unknown:
            sys.exit(f"storage_type_options: {fn} ({stype}): options missing from the "
                     f"apidoc POST /storage schema: {', '.join(unknown)} — plugin/schema drift")
        mapping[stype] = options

    src = source_version(args.pve_storage)
    lines = [
        f"// Code generated by scripts/storage_type_options.py from {src}; DO NOT EDIT.",
        "",
        "package storage",
        "",
        'import "github.com/fivetwenty-io/pve-cli/internal/optionschema"',
        "",
        "// storageTypeOptions maps each storage type to the options its plugin",
        "// accepts, extracted from the PVE::Storage::*Plugin `options` tables.",
        "var storageTypeOptions = map[string]map[string]optionschema.TypeUse{",
    ]
    for stype in sorted(mapping):
        lines.append(f"\t{stype!r}: {{")
        for name in sorted(mapping[stype]):
            use = mapping[stype][name]
            fields = []
            if use["fixed"]:
                fields.append("Fixed: true")
            if use["required"]:
                fields.append("Required: true")
            lines.append(f"\t\t{name!r}: {{{', '.join(fields)}}},")
        lines.append("\t},")
    lines += ["}", ""]
    text = "\n".join(lines).replace("'", "\"")

    with open(args.out, "w", encoding="utf-8") as fh:
        fh.write(text)
    subprocess.run(["gofmt", "-w", args.out], check=True)
    print(f"storage_type_options: wrote {args.out} "
          f"({len(mapping)} types, source: {src})")


if __name__ == "__main__":
    main()
