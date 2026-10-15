"""Compare the implementation's actual wire fields with native capture data."""
import argparse
import json
import pathlib
import struct
import subprocess
import tempfile
from capture_codex_transport import Capture


def settings(row):
    raw = bytes.fromhex(row.get("http2_initial_bytes", ""))
    if raw.startswith(b"PRI "):
        raw = raw[24:]
    values, flow = [], 0
    while len(raw) >= 9:
        length = int.from_bytes(raw[:3], "big")
        data = raw[9:9 + length]
        if len(data) < length:
            break
        if raw[3] == 4 and data:
            values = [{"id": k, "value": v} for k, v in
                      [struct.unpack(">HI", data[i:i + 6]) for i in range(0, len(data), 6)]]
        if raw[3] == 8:
            flow = int.from_bytes(data, "big")
        raw = raw[9 + length:]
    return values, flow


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--probe", type=pathlib.Path, required=True)
    parser.add_argument("--reference", type=pathlib.Path, required=True)
    parser.add_argument("--output", type=pathlib.Path, required=True)
    args = parser.parse_args()
    reference = json.loads(args.reference.read_text())
    results = {}
    with tempfile.TemporaryDirectory(prefix="codex-transport-verify-") as temp:
        capture = Capture(pathlib.Path(temp))
        for mode in ["http", "http1", "ws"]:
            capture.context.set_alpn_protocols(["http/1.1"] if mode=="http1" else ["h2","http/1.1"])
            start = len(capture.records)
            subprocess.run([str(args.probe.resolve()), "--url", f"https://localhost:{capture.port}/v1/responses",
                            "--ca", str(capture.ca), "--mode", mode], check=True)
            rows = capture.records[start:]
            if not rows or not rows[0]["requests"]:
                raise AssertionError("no wire capture for " + mode)
            expected = reference["profiles"][mode]
            for row in rows:
                actual = row["client_hello"]
                for key in ["cipher_suites", "groups", "key_share_groups", "signature_algorithms", "versions", "psk_modes", "alpn", "session_id_length"]:
                    if actual[key] != expected["tls"][key]:
                        raise AssertionError(f"{mode} TLS {key}: {actual[key]} != {expected['tls'][key]}")
                if sorted(actual["extensions"]) != sorted(expected["tls"]["extensions"]):
                    raise AssertionError(f"{mode} TLS extensions: {actual['extensions']}")
                if actual["extension_data"] != expected["tls"]["extension_data"]:
                    raise AssertionError(f"{mode} TLS extension payloads differ")
            if mode == "http":
                actual_settings, flow = settings(rows[0])
                if actual_settings != expected["settings"] or flow != expected["connection_flow"]:
                    raise AssertionError(f"HTTP2 startup differs: {actual_settings}, flow={flow}")
            elif mode=="ws":
                headers = {k.lower(): v for k, v in rows[0]["requests"][0]["headers"]}
                if headers.get("sec-websocket-extensions") != "permessage-deflate; client_max_window_bits":
                    raise AssertionError("WebSocket compression offer differs: " + str(headers.get("sec-websocket-extensions")))
            names = [k.lower() for k, _ in rows[0]["requests"][0]["headers"]]
            expected_names = expected["pseudo_header_order"] + expected["header_order"]
            if names != expected_names:
                raise AssertionError(f"{mode} header ordering differs: {names}")
            if mode in ("ws", "http1"):
                for key, _ in rows[0]["requests"][0]["headers"]:
                    if key != expected["header_case"].get(key.lower(), key):
                        raise AssertionError("WebSocket header spelling differs: " + key)
            results[mode] = rows
        for listener in capture.listeners:
            listener.close()
        args.output.parent.mkdir(parents=True, exist_ok=True)
        args.output.write_text(json.dumps({"matched": True, "captures": results, "errors": capture.errors}, indent=2) + "\n")
    print("official TLS, HTTP2 startup and WebSocket offer matched")


if __name__ == "__main__":
    main()
