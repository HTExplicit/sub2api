"""Capture a fixed official Codex binary against loopback only; no real API calls.

The capture includes complete ClientHello parameters and decoded HTTP/2 headers,
not just a JA3 digest. Certificates, keys and the isolated CODEX_HOME are scratch.
"""
import argparse
import base64
import hashlib
import json
import os
import pathlib
import ipaddress
import platform
import queue
import socket
import ssl
import struct
import subprocess
import tempfile
import threading
import time
from datetime import datetime, timedelta, timezone

import h2.config
import h2.connection
import h2.events
from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.x509.oid import NameOID

VERSION = "0.162.1"
SOURCE = "092d3acd6bec3e3a14bdc7e7a2810ab628ab759d"


def u16(data):
    return list(struct.unpack(">" + "H" * (len(data) // 2), data))


def client_hello(raw):
    # A ClientHello may span TLS records. Preserve handshake bytes only.
    data, offset = b"", 0
    while offset + 5 <= len(raw):
        size = int.from_bytes(raw[offset + 3:offset + 5], "big")
        if offset + 5 + size > len(raw):
            return None
        if raw[offset] == 22:
            data += raw[offset + 5:offset + 5 + size]
        offset += 5 + size
        if len(data) >= 4 and len(data) >= 4 + int.from_bytes(data[1:4], "big"):
            break
    if len(data) < 4 or data[0] != 1 or len(data) < 4 + int.from_bytes(data[1:4], "big"):
        return None
    body = data[4:4 + int.from_bytes(data[1:4], "big")]
    pos = 34
    session_length = body[pos]
    pos += 1 + session_length
    length = int.from_bytes(body[pos:pos + 2], "big")
    ciphers = u16(body[pos + 2:pos + 2 + length])
    pos += 2 + length
    compression = list(body[pos + 1:pos + 1 + body[pos]])
    pos += 1 + body[pos]
    end = pos + 2 + int.from_bytes(body[pos:pos + 2], "big")
    pos += 2
    result = dict(legacy_version=int.from_bytes(body[:2], "big"), cipher_suites=ciphers,
                  compression_methods=compression, session_id_length=session_length,
                  extensions=[], extension_data={}, alpn=[], groups=[], key_share_groups=[],
                  signature_algorithms=[], versions=[], psk_modes=[])
    while pos + 4 <= end:
        kind, length = struct.unpack(">HH", body[pos:pos + 4])
        value = body[pos + 4:pos + 4 + length]
        pos += 4 + length
        result["extensions"].append(kind)
        if kind not in (0, 21, 35, 41, 51):
            result["extension_data"][str(kind)] = value.hex()
        if kind in (10, 13):
            result["groups" if kind == 10 else "signature_algorithms"] = u16(value[2:])
        elif kind == 43:
            result["versions"] = u16(value[1:])
        elif kind == 45:
            result["psk_modes"] = list(value[1:])
        elif kind == 16:
            p = 2
            while p < len(value):
                n = value[p]
                result["alpn"].append(value[p + 1:p + 1 + n].decode())
                p += 1 + n
        elif kind == 51:
            p = 2
            while p + 4 <= len(value):
                group, size = struct.unpack(">HH", value[p:p + 4])
                result["key_share_groups"].append(group)
                p += 4 + size
    return result


def events(number, prewarm=False):
    rid = f"resp_local_capture_{number}"
    output = [] if prewarm else [{"type": "message", "id": f"msg_{number}", "role": "assistant",
                                 "content": [{"type": "output_text", "text": "ok"}]}]
    values = [{"type": "response.created", "response": {"id": rid}}]
    if output:
        values.append({"type": "response.output_item.done", "item": output[0]})
    values.append({"type": "response.completed", "response": {"id": rid, "status": "completed",
                  "output": output, "usage": {"input_tokens": 1, "output_tokens": 1,
                  "total_tokens": 2}}})
    return values


def recv_exact(conn, size):
    out = b""
    while len(out) < size:
        part = conn.recv(size - len(out))
        if not part:
            raise EOFError()
        out += part
    return out


def ws_send(conn, value, opcode=1):
    raw = json.dumps(value).encode() if opcode == 1 else value
    header = bytes([0x80 | opcode])
    header += bytes([len(raw)]) if len(raw) < 126 else b"\x7e" + struct.pack(">H", len(raw))
    conn.sendall(header + raw)


class Capture:
    def __init__(self, directory):
        self.records, self.errors = [], []
        self.lock = threading.Lock()
        self.context = ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
        key = rsa.generate_private_key(public_exponent=65537, key_size=2048)
        name = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, "Codex loopback capture")])
        now = datetime.now(timezone.utc)
        cert = (x509.CertificateBuilder().subject_name(name).issuer_name(name).public_key(key.public_key())
                .serial_number(x509.random_serial_number()).not_valid_before(now - timedelta(minutes=5))
                .not_valid_after(now + timedelta(days=1))
                .add_extension(x509.BasicConstraints(ca=True, path_length=None), critical=True)
                .add_extension(x509.SubjectAlternativeName([x509.DNSName("localhost"), x509.IPAddress(ipaddress.ip_address("127.0.0.1"))]), critical=False)
                .sign(key, hashes.SHA256()))
        self.ca = directory / "ca.pem"
        self.ca.write_bytes(cert.public_bytes(serialization.Encoding.PEM))
        leaf_name = x509.Name([x509.NameAttribute(NameOID.COMMON_NAME, "localhost")])
        leaf = (x509.CertificateBuilder().subject_name(leaf_name).issuer_name(name).public_key(key.public_key())
                .serial_number(x509.random_serial_number()).not_valid_before(now - timedelta(minutes=5))
                .not_valid_after(now + timedelta(days=1))
                .add_extension(x509.BasicConstraints(ca=False, path_length=None), critical=True)
                .add_extension(x509.ExtendedKeyUsage([x509.oid.ExtendedKeyUsageOID.SERVER_AUTH]), critical=False)
                .add_extension(x509.SubjectAlternativeName([x509.DNSName("localhost"), x509.IPAddress(ipaddress.ip_address("127.0.0.1"))]), critical=False)
                .sign(key, hashes.SHA256()))
        leaf_path = directory / "server.pem"
        leaf_path.write_bytes(leaf.public_bytes(serialization.Encoding.PEM))
        private = directory / "key.pem"
        private.write_bytes(key.private_bytes(serialization.Encoding.PEM, serialization.PrivateFormat.PKCS8,
                                              serialization.NoEncryption()))
        self.context.load_cert_chain(leaf_path, private)
        self.context.set_alpn_protocols(["h2", "http/1.1"])
        self.listener = socket.socket()
        self.listener.bind(("127.0.0.1", 0))
        self.listener.listen()
        self.port = self.listener.getsockname()[1]
        self.listeners = [self.listener]
        try:
            ipv6 = socket.socket(socket.AF_INET6)
            ipv6.setsockopt(socket.IPPROTO_IPV6, socket.IPV6_V6ONLY, 1)
            ipv6.bind(("::1", self.port))
            ipv6.listen()
            self.listeners.append(ipv6)
        except OSError:
            pass
        for listener in self.listeners:
            threading.Thread(target=self.accept, args=(listener,), daemon=True).start()

    def accept(self, listener):
        while True:
            try:
                conn, _ = listener.accept()
            except OSError:
                return
            threading.Thread(target=self.handle, args=(conn,), daemon=True).start()

    def handle(self, raw):
        record = None
        try:
            raw.settimeout(20)
            deadline = time.monotonic() + 15
            hello = None
            while time.monotonic() < deadline:
                hello = client_hello(raw.recv(65535, socket.MSG_PEEK))
                if hello:
                    break
                time.sleep(0.01)
            if not hello:
                raise ValueError("incomplete ClientHello")
            record = {"client_hello": hello, "requests": []}
            with self.lock:
                self.records.append(record)
            conn = self.context.wrap_socket(raw, server_side=True)
            record.update(negotiated_alpn=conn.selected_alpn_protocol(), tls_version=conn.version(),
                          cipher=conn.cipher()[0], session_reused=conn.session_reused)
            with conn:
                if conn.selected_alpn_protocol() == "h2":
                    self.http2(conn, record)
                else:
                    self.http1(conn, record)
        except (EOFError, ConnectionResetError, BrokenPipeError):
            pass
        except Exception as exc:
            # Local dummy traffic only. Keep failures distinguishable from captures.
            with self.lock:
                self.errors.append(type(exc).__name__ + ": " + str(exc))
        finally:
            raw.close()

    def http1(self, conn, record):
        data = b""
        while b"\r\n\r\n" not in data:
            data += recv_exact(conn, 1)
        lines = data.decode("latin1").split("\r\n")
        headers = [line.split(":", 1) for line in lines[1:] if ":" in line]
        headers = [(k, v.strip()) for k, v in headers]
        lower = {k.lower(): v for k, v in headers}
        request = {"request_line": lines[0], "headers": headers}
        record["requests"].append(request)
        if lower.get("upgrade", "").lower() == "websocket":
            accept = base64.b64encode(hashlib.sha1((lower["sec-websocket-key"] +
                          "258EAFA5-E914-47DA-95CA-C5AB0DC85B11").encode()).digest())
            conn.sendall(b"HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\nSec-WebSocket-Accept: " + accept + b"\r\n\r\n")
            request["frames"] = []
            while True:
                a, z = recv_exact(conn, 2)
                size = z & 127
                if size == 126:
                    size = int.from_bytes(recv_exact(conn, 2), "big")
                elif size == 127:
                    size = int.from_bytes(recv_exact(conn, 8), "big")
                mask = recv_exact(conn, 4) if z & 128 else b""
                payload = recv_exact(conn, size)
                if mask:
                    payload = bytes(c ^ mask[i % 4] for i, c in enumerate(payload))
                op = a & 15
                if op == 8:
                    return
                if op == 9:
                    ws_send(conn, payload, 10)
                elif op == 1:
                    value = json.loads(payload)
                    request["frames"].append({"opcode": op, "masked": bool(mask), "keys": sorted(value)})
                    for event in events(len(request["frames"]), value.get("generate") is False):
                        ws_send(conn, event)
                    if value.get("generate") is not False:
                        ws_send(conn, b"\x03\xe8", 8)
                        return
            return
        if "content-length" in lower:
            recv_exact(conn, int(lower["content-length"]))
        body = b"".join(("event: " + value["type"] + "\ndata: " + json.dumps(value) + "\n\n").encode()
                        for value in events(len(self.records)))
        if lines[0].startswith("GET "):
            body = b'{"models":[]}'
        conn.sendall(b"HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nConnection: close\r\nContent-Length: "
                     + str(len(body)).encode() + b"\r\n\r\n" + body)

    def http2(self, conn, record):
        h = h2.connection.H2Connection(config=h2.config.H2Configuration(client_side=False, header_encoding="utf-8"))
        h.initiate_connection()
        conn.sendall(h.data_to_send())
        initial = bytearray()
        while True:
            data = conn.recv(65535)
            if not data:
                return
            if not record["requests"]:
                initial.extend(data)
                record["http2_initial_bytes"] = initial.hex()
            for event in h.receive_data(data):
                if isinstance(event, h2.events.RequestReceived):
                    record["requests"].append({"headers": event.headers})
                elif isinstance(event, h2.events.DataReceived):
                    h.acknowledge_received_data(event.flow_controlled_length, event.stream_id)
                elif isinstance(event, h2.events.StreamEnded):
                    body = b"".join(("event: " + v["type"] + "\ndata: " + json.dumps(v) + "\n\n").encode()
                                    for v in events(len(self.records)))
                    h.send_headers(event.stream_id, [(":status", "200"), ("content-type", "text/event-stream")])
                    h.send_data(event.stream_id, body, end_stream=True)
                    h.close_connection(last_stream_id=event.stream_id)
                    conn.sendall(h.data_to_send())
                    return
            conn.sendall(h.data_to_send())


def drive(binary, capture, scratch, mode):
    home = scratch / ("home-" + mode)
    home.mkdir()
    workspace = scratch / "workspace"
    workspace.mkdir(exist_ok=True)
    env = dict(os.environ)
    for key in list(env):
        if key.startswith(("CODEX_", "OPENAI_")):
            env.pop(key, None)
    for key in ["HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "http_proxy", "https_proxy", "all_proxy",
                "OPENAI_API_KEY", "CODEX_API_KEY", "ACCESS_TOKEN"]:
        env.pop(key, None)
    env.update(CODEX_HOME=str(home), CODEX_CA_CERTIFICATE=str(capture.ca),
               SSL_CERT_FILE=str(capture.ca), CODEX_PROFILE_TEST_KEY="local-dummy-key", NO_PROXY="localhost,127.0.0.1")
    env["RUST_LOG"] = "codex_http_client=debug,reqwest=debug,hyper_util=debug"
    provider = dict(name="Loopback only", base_url=f"https://localhost:{capture.port}/v1",
                    env_key="CODEX_PROFILE_TEST_KEY", wire_api="responses", requires_openai_auth=False,
                    supports_websockets=mode == "ws", request_max_retries=0, stream_max_retries=0)
    def toml(value):
        return str(value).lower() if isinstance(value, bool) else json.dumps(value)
    config = "model_providers.capture={" + ",".join(k + "=" + toml(v) for k, v in provider.items()) + "}"
    args = [str(binary), "app-server", "--listen", "stdio://", "-c", 'model_provider="capture"',
            "-c", config, "-c", 'model="gpt-5.4"', "-c", 'approval_policy="never"',
            "-c", 'sandbox_mode="read-only"', "-c", 'features.responses_websockets_v2=' + str(mode == "ws").lower(),
            "-c", 'analytics.enabled=false', "-c", 'features.plugins=false']
    process = subprocess.Popen(args, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                               cwd=workspace, env=env, text=True, encoding="utf-8")
    incoming, errors = queue.Queue(), []
    def reader():
        for line in process.stdout:
            try:
                incoming.put(json.loads(line))
            except json.JSONDecodeError:
                pass
    threading.Thread(target=reader, daemon=True).start()
    def read_errors():
        for line in process.stderr:
            errors.append(line.rstrip())
    threading.Thread(target=read_errors, daemon=True).start()
    def send(value):
        process.stdin.write(json.dumps(value) + "\n")
        process.stdin.flush()
    def until(predicate):
        deadline = time.monotonic() + 45
        observed = []
        while time.monotonic() < deadline:
            try:
                value = incoming.get(timeout=1)
            except queue.Empty:
                if process.poll() is not None:
                    break
                continue
            if "error" in value:
                raise RuntimeError(json.dumps(value["error"]))
            observed.append(value)
            if predicate(value):
                return value
        raise RuntimeError("local app-server did not complete: " + json.dumps(observed[-5:]) + "\n" + "\n".join(errors[-8:]))
    try:
        send({"id": 1, "method": "initialize", "params": {"clientInfo": {"name": "codex-tui", "title": "Capture", "version": VERSION}}})
        until(lambda v: v.get("id") == 1)
        send({"method": "initialized"})
        send({"id": 2, "method": "thread/start", "params": {"model": "gpt-5.4", "cwd": str(workspace)}})
        thread = until(lambda v: v.get("id") == 2)["result"]["thread"]["id"]
        for i in range(2):
            send({"id": 3 + i, "method": "turn/start", "params": {"threadId": thread,
                  "input": [{"type": "text", "text": "Reply ok from the loopback fixture."}]}})
            result = until(lambda v: v.get("method") == "turn/completed")
            if result["params"]["turn"]["status"] != "completed":
                raise RuntimeError("local fixture turn failed: " + json.dumps(result) + "\n" + "\n".join(errors[-15:]))
    finally:
        process.stdin.close()
        try:
            process.wait(timeout=5)
        except subprocess.TimeoutExpired:
            process.terminate()
            process.wait(timeout=5)


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--binary", required=True, type=pathlib.Path)
    parser.add_argument("--output", required=True, type=pathlib.Path)
    args = parser.parse_args()
    binary = args.binary.resolve()
    version = subprocess.check_output([str(binary), "--version"], text=True).strip()
    if version != "codex-cli " + VERSION:
        raise SystemExit("unexpected official binary version: " + version)
    with tempfile.TemporaryDirectory(prefix="codex-transport-capture-", ignore_cleanup_errors=True) as temp:
        scratch = pathlib.Path(temp)
        capture = Capture(scratch)
        modes = {}
        for mode in ["http", "ws"]:
            print("capturing " + mode, flush=True)
            start = len(capture.records)
            try:
                drive(binary, capture, scratch, mode)
            except Exception:
                args.output.parent.mkdir(parents=True, exist_ok=True)
                args.output.write_text(json.dumps({"incomplete": True, "mode": mode, "captures": capture.records,
                                                 "errors": capture.errors}, indent=2), encoding="utf-8")
                raise
            modes[mode] = capture.records[start:]
            if not modes[mode] or not any(row["requests"] for row in modes[mode]):
                raise RuntimeError("no completed " + mode + " capture")
            if mode == "ws" and not any("frames" in req for row in modes[mode] for req in row["requests"]):
                raise RuntimeError("official client did not use WebSocket")
        for listener in capture.listeners:
            listener.close()
        result = dict(schema=1, version=VERSION, source_commit=SOURCE, binary_sha256=hashlib.sha256(binary.read_bytes()).hexdigest(),
                      system=platform.system(), release=platform.release(), machine=platform.machine(),
                      trust_mode="isolated CODEX_CA_CERTIFICATE", real_model_requests=0, captures=modes,
                      errors=capture.errors)
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps(result, indent=2) + "\n", encoding="utf-8")
        print(json.dumps({"output": str(args.output), "system": result["system"],
                          "connections": {k: len(v) for k, v in modes.items()}, "errors": capture.errors}))


if __name__ == "__main__":
    main()
