"""Shared failure-text cleaning for both e2e suites.

The CLI writes operational warnings to stderr ahead of the real error — the
`--insecure` TLS banner alone is ~100 characters. Left in place it consumes the
whole truncation budget of every skip or failure message, so the actual cause
never reaches the report: that is how a genuine `storage volume alloc` defect
hid behind a plausible-looking "environment skip". Every place that renders a
reason goes through here so no message can be truncated down to a banner.

Import-only (stdlib) so it stays usable from a `uv run --script` entry point.
"""

from __future__ import annotations

import re

NOISE_PREFIXES = ("WARN:", "WARNING:", "warn:", "warning:")
# A bare `(code: 0)` trailer says nothing. The same trailer WITH an `errors:`
# payload — `(code: 0, errors: zone: zone is not an EVPN zone)` — is where PVE
# puts its precise complaint, so it must survive: dropping it would take the
# discriminating token out of every skip-marker match.
_BARE_CODE_RE = re.compile(r"^\(code:\s*-?\d+\)$")


def clean_output(text: str) -> str:
    """Drop CLI warning banners, bare `(code: N)` trailers, and blank lines."""
    keep = []
    for line in (text or "").splitlines():
        stripped = line.strip()
        if not stripped:
            continue
        if stripped.startswith(NOISE_PREFIXES):
            continue
        if _BARE_CODE_RE.match(stripped):
            continue
        keep.append(stripped)
    return "\n".join(keep)


def one_line(reason: str, fallback: str, limit: int = 160) -> str:
    """Collapse a cleaned failure reason to one informative line.

    The CLI wraps errors as `<context>: API request failed: <message>`, so the
    trailing API message carries the signal and the wrapper prefixes do not.
    """
    lines = [ln for ln in (reason or "").splitlines() if ln]
    if not lines:
        return fallback
    # PVE's `(code: N, errors: ...)` trailer names the offending parameter and
    # why, which beats the longest wrapper line every time.
    detailed = [ln for ln in lines if "errors:" in ln.lower()]
    line = max(detailed or lines, key=len)
    for marker in ("api request failed:", "api error:"):
        idx = line.lower().rfind(marker)
        if idx != -1:
            line = line[idx + len(marker):].strip()
            break
    return line[:limit] or fallback


def reason_of(stderr: str, stdout: str, rc: int, fallback: str = "",
              limit: int = 160) -> str:
    """One-line reason for a failed command, warnings stripped."""
    cleaned = clean_output(stderr) or clean_output(stdout)
    if not cleaned:
        return fallback or f"exit {rc}"
    return one_line(cleaned, fallback or f"exit {rc}", limit=limit)
