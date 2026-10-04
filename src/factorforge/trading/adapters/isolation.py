"""Linux process boundaries and a revocable, destination-limited TLS tunnel.

The executor runs in an empty network namespace. Its only external route is
this Unix-socket gateway; revocation closes established tunnels as well as
rejecting new ones. TLS remains end-to-end between the executor and Binance.
"""
import base64
import ctypes
import hmac
import os
from pathlib import Path
import secrets
import select
import socket
import socketserver
from threading import Lock, Thread

import httpcore
import httpx

from factorforge.trading.domain.errors import TradingError

TESTNET_HOST = "demo-fapi.binance.com"
TESTNET_URL = "https://" + TESTNET_HOST


class RevocableEgress:
    def __init__(self, path):
        self.path = str(path)
        self.lock, self.tokens, self.connections = Lock(), set(), {}
        self.allowed_connections = 0
        gateway = self

        class Handler(socketserver.BaseRequestHandler):
            def handle(self):
                remote = None
                token = None
                try:
                    self.request.settimeout(10)
                    data = b""
                    while b"\r\n\r\n" not in data and len(data) < 8192:
                        chunk = self.request.recv(1024)
                        if not chunk:
                            return
                        data += chunk
                    headers, extra = data.split(b"\r\n\r\n", 1)
                    lines = headers.split(b"\r\n")
                    fields = {name.lower(): value for name, value in (line.split(b":", 1) for line in lines[1:])}
                    authorization = fields.get(b"proxy-authorization", b"").strip()
                    with gateway.lock:
                        token = next((t for t in gateway.tokens if hmac.compare_digest(authorization,
                            b"Basic " + base64.b64encode(b"executor:" + t.encode()))), None)
                    if lines[0] != b"CONNECT " + TESTNET_HOST.encode() + b":443 HTTP/1.1" or token is None or extra:
                        self.request.sendall(b"HTTP/1.1 403 Forbidden\r\nContent-Length: 0\r\n\r\n")
                        return
                    remote = socket.create_connection((TESTNET_HOST, 443), timeout=10)
                    with gateway.lock:
                        if token not in gateway.tokens:
                            return
                        gateway.connections[self.request] = (token, remote)
                        gateway.allowed_connections += 1
                    self.request.sendall(b"HTTP/1.1 200 Connection established\r\n\r\n")
                    while True:
                        ready, _, _ = select.select([self.request, remote], [], [], 1)
                        with gateway.lock:
                            if token not in gateway.tokens:
                                return
                        for origin in ready:
                            packet = origin.recv(65536)
                            if not packet:
                                return
                            (remote if origin is self.request else self.request).sendall(packet)
                except (OSError, ValueError):
                    return  # never log tunnel headers, URLs, or account payloads
                finally:
                    with gateway.lock:
                        gateway.connections.pop(self.request, None)
                    if remote:
                        remote.close()

        class Server(socketserver.ThreadingUnixStreamServer):
            daemon_threads = True

        self.server = Server(self.path, Handler)
        os.chmod(self.path, 0o600)
        self.thread = Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def issue(self):
        token = secrets.token_hex(32)
        with self.lock:
            self.tokens.add(token)
        return token

    def revoke(self, token):
        with self.lock:
            self.tokens.discard(token)
            active = [(local, remote) for local, (owner, remote) in self.connections.items() if owner == token]
        for pair in active:
            for connection in pair:
                try:
                    connection.shutdown(socket.SHUT_RDWR)
                except OSError:
                    pass

    def close(self):
        for token in list(self.tokens):
            self.revoke(token)
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)
        Path(self.path).unlink(missing_ok=True)


def tunnel_transport(path, token):
    # httpx's proxy branch does not forward its uds parameter. Supply the
    # httpcore pool explicitly; httpx still owns response streaming/cleanup.
    class UnixBackend(httpcore.SyncBackend):
        def connect_tcp(self, host, port, timeout=None, local_address=None, socket_options=None):
            if host != "gateway.invalid" or port != 80:
                raise httpcore.ConnectError("GATEWAY_DESTINATION_FORBIDDEN")
            return self.connect_unix_socket(str(path), timeout=timeout, socket_options=socket_options)
    transport = httpx.HTTPTransport(trust_env=False, retries=0)
    transport._pool.close()
    transport._pool = httpcore.HTTPProxy(proxy_url="http://gateway.invalid", network_backend=UnixBackend(),
        proxy_auth=("executor", token), ssl_context=httpx.create_ssl_context(trust_env=False), retries=0)
    return transport


def restrict_filesystem(read_paths, writable_paths=()):
    """Apply Landlock to the current process and all descendants, fail closed.

    No shell, chmod convention, or secret redaction substitutes for this check.
    Profiles are exact files; the credential directory is never allowlisted.
    """
    if os.name != "posix":
        raise TradingError("LINUX_ISOLATION_REQUIRED", 503)
    libc = ctypes.CDLL(None, use_errno=True)
    abi = libc.syscall(444, 0, 0, 1)
    if abi < 1:
        raise TradingError("LANDLOCK_UNAVAILABLE", 503)
    # ABI 1 filesystem rights, plus REFER and TRUNCATE when supported.
    handled = (1 << 13) - 1
    if abi >= 2:
        handled |= 1 << 13
    if abi >= 3:
        handled |= 1 << 14
    class Ruleset(ctypes.Structure):
        _fields_ = [("handled_access_fs", ctypes.c_uint64)]
    class PathRule(ctypes.Structure):
        _pack_ = 1
        _fields_ = [("allowed_access", ctypes.c_uint64), ("parent_fd", ctypes.c_int32)]
    attr = Ruleset(handled)
    fd = libc.syscall(444, ctypes.byref(attr), ctypes.sizeof(attr), 0)
    if fd < 0:
        raise TradingError("LANDLOCK_UNAVAILABLE", 503)
    try:
        for path, write in [(p, False) for p in read_paths] + [(p, True) for p in writable_paths]:
            target = Path(path).resolve()
            if not target.exists():
                continue
            rights = handled if write else (1 << 0) | (1 << 2) | (1 << 3)
            if not target.is_dir():
                rights &= (1 << 0) | (1 << 1) | (1 << 2) | (1 << 14)
            parent = os.open(target, os.O_PATH | os.O_CLOEXEC)
            try:
                rule = PathRule(rights, parent)
                if libc.syscall(445, fd, 1, ctypes.byref(rule), 0) != 0:
                    raise TradingError("LANDLOCK_RULE_FAILED", 503)
            finally:
                os.close(parent)
        if libc.prctl(38, 1, 0, 0, 0) != 0 or libc.syscall(446, fd, 0) != 0:
            raise TradingError("LANDLOCK_RESTRICTION_FAILED", 503)
    finally:
        os.close(fd)
