#!/usr/bin/env -S uv run --script
# /// script
# requires-python = ">=3.11"
# dependencies = []
# ///
"""One-shot migrator: rename pre-context pve config YAML keys to post-rename keys.

Renames top-level keys:
  targets:         → contexts:
  current-target:  → current-context:

All other keys, comments, and formatting are preserved via text-level replacement.
Context entries' inner structure is identical between old and new formats.

Regex anchored to column 0 (^targets:, ^current-target:) is sufficient because
nested YAML keys are always indented — a key inside a mapping block appears at
indent >= 2. A top-level key can only appear at column 0. The claim holds even
when context entry values contain the literal strings "targets:" or
"current-target:" — those would be indented. Verified against the fixture in
the task directory.

Default config path resolves via $XDG_CONFIG_HOME (same logic as loader.go):
  $XDG_CONFIG_HOME/pve/config.yml
  or ~/.config/pmx/config.yml

Usage:
  scripts/migrate-config.py               # migrate default config in-place
  scripts/migrate-config.py --dry-run     # print result to stdout, no file write
  scripts/migrate-config.py --config PATH # override config path
"""

from __future__ import annotations

import argparse
import os
import re
import shutil
import stat
import sys


# Compiled patterns anchored to column 0, word-boundary terminated.
# Matches the full key token so "targets:" does not match "targets-extra:".
_PATTERN_TARGETS = re.compile(r"^(targets):(\s)", re.MULTILINE)
_PATTERN_CURRENT_TARGET = re.compile(r"^(current-target):(\s)", re.MULTILINE)

_OLD_KEYS = {"targets:", "current-target:"}
_NEW_KEYS = {"contexts:", "current-context:"}


def _default_config_path() -> str:
    """Return canonical config path, matching loader.go DefaultPath() logic."""
    base = os.environ.get("XDG_CONFIG_HOME", "")
    if not base:
        home = os.path.expanduser("~")
        base = os.path.join(home, ".config")
    return os.path.join(base, "pmx", "config.yml")


def _detect_state(text: str) -> str:
    """Classify config text as 'old', 'new', 'mixed', or 'empty'.

    Returns:
        'old'   — has old keys, no new keys
        'new'   — has new keys, no old keys
        'mixed' — has both old and new keys (corrupted/partially migrated)
        'empty' — no recognisable top-level context keys at all
    """
    has_old = bool(
        _PATTERN_TARGETS.search(text) or _PATTERN_CURRENT_TARGET.search(text)
    )
    has_new = bool(
        re.search(r"^contexts:\s", text, re.MULTILINE)
        or re.search(r"^current-context:\s", text, re.MULTILINE)
    )
    if has_old and has_new:
        return "mixed"
    if has_old:
        return "old"
    if has_new:
        return "new"
    return "empty"


def _migrate_text(text: str) -> str:
    """Apply key renames to text.  Only top-level keys are affected."""
    text = _PATTERN_TARGETS.sub(r"contexts:\2", text)
    text = _PATTERN_CURRENT_TARGET.sub(r"current-context:\2", text)
    return text


def _get_file_mode(path: str) -> int:
    """Return permission bits for path, or 0o600 if stat fails."""
    try:
        return stat.S_IMODE(os.stat(path).st_mode)
    except OSError:
        return 0o600


def migrate(config_path: str, dry_run: bool) -> int:
    """Perform migration.

    Returns exit code:
      0 — success or no-op
      1 — I/O or unexpected error
      2 — mixed-state refusal
    """
    # --- read ---
    try:
        with open(config_path, "r", encoding="utf-8") as fh:
            original = fh.read()
    except FileNotFoundError:
        print(
            f"error: config file not found: {config_path}",
            file=sys.stderr,
        )
        return 1
    except PermissionError:
        print(
            f"error: permission denied reading {config_path}",
            file=sys.stderr,
        )
        return 1
    except OSError as exc:
        print(f"error: cannot read {config_path}: {exc}", file=sys.stderr)
        return 1

    # --- classify ---
    state = _detect_state(original)

    if state == "mixed":
        print(
            f"error: config at {config_path} contains both old keys "
            "(targets:/current-target:) and new keys (contexts:/current-context:). "
            "Resolve manually before running this migrator.",
            file=sys.stderr,
        )
        return 2

    if state in ("new", "empty"):
        print(
            f"info: {config_path} already uses current key names — nothing to do."
        )
        return 0

    # state == "old": proceed
    migrated = _migrate_text(original)

    if dry_run:
        print(migrated, end="")
        return 0

    # --- backup ---
    backup_path = config_path + ".bak"
    original_mode = _get_file_mode(config_path)
    try:
        shutil.copy2(config_path, backup_path)
        os.chmod(backup_path, original_mode)
    except PermissionError:
        print(
            f"error: permission denied writing backup {backup_path}",
            file=sys.stderr,
        )
        return 1
    except OSError as exc:
        print(f"error: cannot write backup {backup_path}: {exc}", file=sys.stderr)
        return 1

    # --- write in-place ---
    try:
        # Write to a sibling tmp file then rename for atomicity.
        tmp_path = config_path + ".tmp"
        with open(tmp_path, "w", encoding="utf-8") as fh:
            fh.write(migrated)
        os.chmod(tmp_path, original_mode)
        os.replace(tmp_path, config_path)
    except PermissionError:
        print(
            f"error: permission denied writing {config_path}",
            file=sys.stderr,
        )
        return 1
    except OSError as exc:
        print(f"error: cannot write {config_path}: {exc}", file=sys.stderr)
        return 1

    print(f"migrated: {config_path} (backup: {backup_path})")
    return 0


def main() -> None:
    parser = argparse.ArgumentParser(
        prog="migrate-config",
        description="Migrate pve config YAML from pre-rename to post-rename keys.",
        formatter_class=argparse.RawDescriptionHelpFormatter,
        epilog=(
            "Renames top-level YAML keys:\n"
            "  targets:         → contexts:\n"
            "  current-target:  → current-context:\n\n"
            "Comments and formatting are preserved. A .bak file is written before\n"
            "any in-place change. Operation is idempotent."
        ),
    )
    parser.add_argument(
        "--config",
        metavar="PATH",
        default=None,
        help=(
            "path to pve config file "
            "(default: $XDG_CONFIG_HOME/pve/config.yml or ~/.config/pmx/config.yml)"
        ),
    )
    parser.add_argument(
        "--dry-run",
        action="store_true",
        help="print migrated YAML to stdout without modifying any file",
    )
    args = parser.parse_args()

    config_path = args.config if args.config else _default_config_path()
    sys.exit(migrate(config_path, dry_run=args.dry_run))


if __name__ == "__main__":
    main()
