"""A throwaway LDAP responder staged on an appliance host over root SSH.

Proxmox Backup Server and Proxmox Datacenter Manager both *connect to the
directory* when an LDAP or AD realm is created: the create fails outright if
nothing answers, and an AD realm additionally reads `defaultNamingContext` from
the root DSE to derive its base DN. So the realm verbs cannot be exercised
against an unroutable address the way a notification endpoint can — something
has to answer.

Rather than require a real directory server in the lab, this stages a ~70-line
LDAP responder on the appliance itself, bound to 127.0.0.1 only, that answers a
simple bind and a root-DSE search and returns no users for anything else. It is
started before the realm block and killed after it, and the realm it backs is
never made the default, so no login path can reach it even while it is up.

If the host is unreachable over SSH, `start` returns 0 and the caller records
the LDAP/AD verbs as skips naming that.
"""

from __future__ import annotations

# Bound to loopback on the appliance, on a port no Proxmox service uses.
PORT = 3389
REMOTE_PATH = "/tmp/pmx-cli-e2e-ldapstub.py"
BASE_DN = "dc=example,dc=invalid"

# The responder itself. Written to the host verbatim; it speaks just enough BER
# to echo a message ID and emit three canned responses.
SOURCE = '''"""Throwaway LDAP responder for the pmx-cli e2e suites. Loopback only."""
import socketserver
import sys


def ber_len(b, i):
    """Read a BER length at b[i]; return (length, index-after-length)."""
    n = b[i]
    i += 1
    if n < 0x80:
        return n, i
    k = n & 0x7F
    return int.from_bytes(b[i:i + k], "big"), i + k


def enc_len(n):
    if n < 0x80:
        return bytes([n])
    b = n.to_bytes((n.bit_length() + 7) // 8, "big")
    return bytes([0x80 | len(b)]) + b


def octet(b):
    return b"\\x04" + enc_len(len(b)) + b


def msg(msgid, op_tag, payload):
    """Wrap payload as an LDAPMessage carrying msgid and the given op tag."""
    mid = b"\\x02" + enc_len(2) + msgid.to_bytes(2, "big")
    op = bytes([op_tag]) + enc_len(len(payload)) + payload
    inner = mid + op
    return b"\\x30" + enc_len(len(inner)) + inner


# resultCode success(0), matchedDN "", diagnosticMessage ""
RESULT_OK = b"\\x0a\\x01\\x00\\x04\\x00\\x04\\x00"

# SearchResultEntry for the root DSE: objectName "", one attribute
# defaultNamingContext with a single value. AD realm creation reads it to
# derive the realm's base DN, and refuses to be created without it.
_BASE_DN = b"__BASE_DN__"
_ATTR = (octet(b"defaultNamingContext")
         + b"\\x31" + enc_len(len(octet(_BASE_DN))) + octet(_BASE_DN))
_ATTRS = b"\\x30" + enc_len(len(_ATTR)) + _ATTR
ROOT_DSE = octet(b"") + b"\\x30" + enc_len(len(_ATTRS)) + _ATTRS


class Handler(socketserver.BaseRequestHandler):
    def handle(self):
        buf = b""
        while True:
            try:
                data = self.request.recv(4096)
            except OSError:
                return
            if not data:
                return
            buf += data
            while len(buf) > 2 and buf[0] == 0x30:
                total, i = ber_len(buf, 1)
                if len(buf) < i + total:
                    break
                pdu, buf = buf[:i + total], buf[i + total:]
                j = i
                if pdu[j] != 0x02:
                    return
                ln, j = ber_len(pdu, j + 1)
                msgid = int.from_bytes(pdu[j:j + ln], "big")
                j += ln
                tag = pdu[j]
                if tag == 0x60:        # BindRequest
                    self.request.sendall(msg(msgid, 0x61, RESULT_OK))
                elif tag == 0x63:      # SearchRequest
                    k = j + 1
                    _, k = ber_len(pdu, k)
                    base = b"x"
                    if pdu[k] == 0x04:  # baseObject, the first body field
                        dnlen, k2 = ber_len(pdu, k + 1)
                        base = pdu[k2:k2 + dnlen]
                    if not base:
                        self.request.sendall(msg(msgid, 0x64, ROOT_DSE))
                    # Everything else resolves to zero entries: a user sync
                    # against this stub must never invent an account.
                    self.request.sendall(msg(msgid, 0x65, RESULT_OK))
                elif tag == 0x42:      # UnbindRequest
                    return


class Server(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True


Server(("127.0.0.1", int(sys.argv[1])), Handler).serve_forever()
'''


def start(ssh, host: str, port: int = PORT) -> int:
    """Stage and start the responder on `host`. Returns the port, or 0 on failure.

    `ssh` is the suite's `_ssh_node(host, *cmd, stdin=...)` helper, passed in so
    this module stays free of transport details.
    """
    stop(ssh, host)
    if ssh(host, f"cat > {REMOTE_PATH}",
           stdin=SOURCE.replace("__BASE_DN__", BASE_DN))[0] != 0:
        return 0
    ssh(host, f"nohup python3 {REMOTE_PATH} {port} >/dev/null 2>&1 & echo started")
    # Poll rather than sleep blindly: the listener is up in well under a second.
    for _ in range(10):
        if ssh(host, f"ss -lnt 2>/dev/null | grep -q ':{port} ' && echo UP")[1].strip():
            return port
    stop(ssh, host)
    return 0


def stop(ssh, host: str) -> None:
    """Kill the responder and remove it. Idempotent; errors are ignored."""
    ssh(host, f"pkill -f {REMOTE_PATH} 2>/dev/null; rm -f {REMOTE_PATH}; true")
