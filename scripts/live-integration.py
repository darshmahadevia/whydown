#!/usr/bin/env python3
"""Exercise Whydown's built CLI, watcher and MCP tools against Docker."""
import argparse
import json
import pathlib
import queue
import signal
import subprocess
import threading
import time
import uuid

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--binary", default="./bin/whydown")
parser.add_argument("--provider", choices=["offline", "groq", "google"], default="offline")
args = parser.parse_args()
binary = str(pathlib.Path(args.binary).resolve())
prefix = "whydown-integration-" + uuid.uuid4().hex[:8]
created = []
watch = None
reports = []
checks = []
watch_queue = queue.Queue()


def execute(parts, check=True, timeout=120):
    return subprocess.run(parts, check=check, capture_output=True, text=True, timeout=timeout)


def docker(*parts, check=True):
    return execute(["docker", *parts], check=check).stdout.strip()


def create(label, options, start=True):
    name = prefix + "-" + label
    created.append(name)
    docker("run" if start else "create", *( ["-d"] if start else []), "--name", name, *options)
    return name


def state(name):
    return json.loads(docker("inspect", name))[0]


def wait_until(predicate, timeout=40):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        if predicate():
            return
        time.sleep(0.25)
    raise AssertionError("Timed out waiting for Docker state")


def evidence(name):
    return json.loads(execute([binary, "inspect", name]).stdout)


def passed(name):
    checks.append(name)
    print("PASS " + name, flush=True)


def watch_reader():
    for line in watch.stdout:
        try:
            watch_queue.put(json.loads(line))
        except json.JSONDecodeError:
            watch_queue.put({"parse_error": True})


def wait_report(name, cause, timeout=40):
    deadline = time.monotonic() + timeout
    while time.monotonic() < deadline:
        for report in reports:
            if report.get("container") == name and report.get("cause") == cause:
                return report
        try:
            report = watch_queue.get(timeout=1)
            assert not report.get("parse_error"), "Watcher emitted invalid JSON"
            if report.get("container", "").startswith(prefix):
                reports.append(report)
        except queue.Empty:
            assert watch.poll() is None, "Watcher exited unexpectedly"
    raise AssertionError("Watcher did not report " + name + " as " + cause)


try:
    docker("info", "--format", "{{.ServerVersion}}")
    healthy = create("stats", ["busybox:1.37", "sleep", "120"])
    usage = evidence(healthy)
    assert usage["container"]["state"] == "running"
    assert usage["memory_usage_bytes"] > 0 and usage["memory_limit_bytes"] > 0
    assert usage["cpu_percent"] >= 0
    passed("live CPU and memory collection")

    long_secret = "test-long-" + "q" * 5000
    flag_secret = "test-split-" + uuid.uuid4().hex
    url_secret = "https://credential.example/" + uuid.uuid4().hex + "?x=1&y=private"
    privacy = create("privacy", ["-e", "CUSTOM_CREDENTIAL=" + long_secret, "busybox:1.37", "sh", "-c", 'printf "%s\\n" "$@"; echo "permission denied"; exit 1', "app", "--password", flag_secret, "--token=" + url_secret, long_secret])
    wait_until(lambda: not state(privacy)["State"]["Running"])
    sanitized = evidence(privacy)
    serialized = json.dumps(sanitized)
    for secret in [long_secret, flag_secret, url_secret, "q" * 100, "y=private"]:
        assert secret not in serialized, "A synthetic secret leaked in Docker evidence"
    assert "[REDACTED]" in serialized
    passed("real command, environment and log credential redaction")

    watch = subprocess.Popen([binary, "watch", "--provider", "offline", "--json"], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    threading.Thread(target=watch_reader, daemon=True).start()
    wait_report(privacy, "permission")
    passed("watcher initial failure scan")

    crash = create("crash", ["busybox:1.37", "sh", "-c", "sleep 1; echo 'connection refused'; exit 17"])
    wait_report(crash, "network")
    passed("watcher catches new crash events")

    restart = create("restart", ["--restart", "on-failure:3", "busybox:1.37", "sh", "-c", "echo 'startup failed'; exit 23"])
    wait_until(lambda: state(restart)["RestartCount"] >= 3)
    wait_report(restart, "exit")
    assert evidence(restart)["restart_count"] >= 3
    passed("restart loop evidence and monitoring")

    health = create("health", ["--health-cmd", "exit 1", "--health-interval", "1s", "--health-retries", "2", "busybox:1.37", "sleep", "120"])
    wait_report(health, "unhealthy")
    passed("watcher detects unhealthy containers")

    startup = create("startup", ["busybox:1.37", "/whydown-nonexistent"], start=False)
    started = execute(["docker", "start", startup], check=False)
    assert started.returncode != 0 and state(startup)["State"]["Error"]
    assert state(startup)["State"]["Status"] == "created"
    wait_report(startup, "exit")
    passed("watcher detects failed container startup in created state")

    requests = [
        {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"protocolVersion": "2025-11-25"}},
        {"jsonrpc": "2.0", "method": "notifications/initialized"},
        {"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": {"name": "list_containers", "arguments": {}}},
        {"jsonrpc": "2.0", "id": 3, "method": "tools/call", "params": {"name": "inspect_container", "arguments": {"container": privacy}}},
        {"jsonrpc": "2.0", "id": 4, "method": "tools/call", "params": {"name": "diagnose_container", "arguments": {"container": privacy}}},
        {"jsonrpc": "2.0", "id": 5, "method": "tools/call", "params": {"name": "inspect_container", "arguments": {"container": prefix + "-missing"}}},
    ]
    transport = subprocess.run([binary, "mcp", "--provider", args.provider], input="".join(json.dumps(r) + "\n" for r in requests), text=True, capture_output=True, check=True, timeout=90)
    replies = {r["id"]: r for r in map(json.loads, transport.stdout.splitlines())}
    assert len(replies) == 5
    containers = json.loads(replies[2]["result"]["content"][0]["text"])
    assert any(c["name"] == privacy for c in containers)
    inspected = json.loads(replies[3]["result"]["content"][0]["text"])
    assert inspected["container"]["name"] == privacy
    report = json.loads(replies[4]["result"]["content"][0]["text"])
    assert report["container"] == privacy and report["provider"] == args.provider
    assert report["fixes"] and report["summary"] and report["evidence"]
    assert replies[5]["result"]["isError"] is True
    for secret in [long_secret, flag_secret, url_secret, "q" * 100]:
        assert secret not in json.dumps([replies[3], replies[4]])
    passed("MCP list, sanitized inspect, diagnosis and missing-container errors")

    watch.send_signal(signal.SIGTERM)
    assert watch.wait(timeout=5) == 0
    passed("live watcher clean shutdown")
    pathlib.Path("artifacts").mkdir(exist_ok=True)
    pathlib.Path("artifacts/live-integration.json").write_text(json.dumps({"provider": args.provider, "checks": checks, "watch_reports": reports, "mcp_diagnosis": report}, indent=2) + "\n")
finally:
    if watch is not None:
        if watch.poll() is None:
            watch.terminate()
            try:
                watch.wait(timeout=5)
            except subprocess.TimeoutExpired:
                watch.kill()
                watch.wait()
        watch.stdout.close()
        watch.stderr.close()
    for name in reversed(created):
        execute(["docker", "rm", "-f", name], check=False)
    remaining = docker("ps", "-aq", "--filter", "name=" + prefix)
    assert not remaining, "Integration containers were not cleaned up"
