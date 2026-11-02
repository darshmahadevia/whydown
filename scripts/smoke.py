#!/usr/bin/env python3
"""Verify the built CLI and its MCP transport without Docker or API keys."""
import json
import pathlib
import signal
import subprocess
import time

binary = str(pathlib.Path("bin/whydown").resolve())
score = json.loads(subprocess.check_output([binary, "eval", "--provider", "offline"], text=True))
assert score["passed"] == score["total"] == 8
assert len(score["results"]) == 8
assert all(result["passed"] for result in score["results"])
requests = [
    {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"protocolVersion": "2025-11-25"}},
    {"jsonrpc": "2.0", "method": "notifications/initialized"},
    {"jsonrpc": "2.0", "id": 2, "method": "tools/list"},
]
result = subprocess.run([binary, "mcp", "--provider", "offline"], input="".join(json.dumps(r) + "\n" for r in requests), text=True, capture_output=True, check=True, timeout=10)
replies = [json.loads(line) for line in result.stdout.splitlines()]
assert len(replies) == 2
assert replies[0]["result"]["protocolVersion"] == "2025-11-25"
assert {t["name"] for t in replies[1]["result"]["tools"]} == {"list_containers", "inspect_container", "diagnose_container"}
process = subprocess.Popen([binary, "mcp", "--provider", "offline"], stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
try:
    time.sleep(0.2)
    process.send_signal(signal.SIGTERM)
    assert process.wait(timeout=5) == 0
finally:
    if process.poll() is None:
        process.kill()
        process.wait()
    process.stdin.close()
    process.stdout.close()
    process.stderr.close()
print("Built CLI passed offline evaluation, MCP discovery, and SIGTERM shutdown.")
