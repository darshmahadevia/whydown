#!/usr/bin/env python3
"""Create isolated Docker faults, collect actual evidence, then score it."""
import argparse
import json
import pathlib
import subprocess
import time
import uuid

parser = argparse.ArgumentParser(description=__doc__)
parser.add_argument("--binary", default="./bin/whydown")
parser.add_argument("--provider", default="offline", choices=["offline", "groq", "google"])
parser.add_argument("--output", default="artifacts/live-cases.json")
args = parser.parse_args()
names = []
cases = []
prefix = "whydown-eval-" + uuid.uuid4().hex[:8]


def docker(*parts):
    return subprocess.check_output(["docker", *parts], text=True).strip()


def wait_for(name, predicate):
    deadline = time.monotonic() + 120
    while time.monotonic() < deadline:
        state = json.loads(docker("inspect", name))[0]["State"]
        if predicate(state):
            return
        time.sleep(0.5)
    raise RuntimeError("Timed out waiting for fault in " + name)


faults = [
    ("oom", ["--memory", "32m", "--memory-swap", "32m", "python:3.12-alpine", "python", "-c", "x = bytearray(256*1024*1024)"], ["memory"], lambda s: not s["Running"] and s["OOMKilled"]),
    ("network", ["python:3.12-alpine", "python", "-c", "import socket; socket.create_connection(('127.0.0.1',5432),2)"], ["service"], lambda s: not s["Running"] and s["ExitCode"] != 0),
    ("permission", ["--user", "65534:65534", "busybox:1.37", "sh", "-c", "touch /root/whydown-test"], ["permission"], lambda s: not s["Running"] and s["ExitCode"] != 0),
    ("port", ["busybox:1.37", "sh", "-c", "httpd -p 8080; exec httpd -f -p 8080"], ["port"], lambda s: not s["Running"] and s["ExitCode"] != 0),
    ("health", ["--health-cmd", "exit 1", "--health-interval", "1s", "--health-retries", "2", "busybox:1.37", "sleep", "300"], ["health"], lambda s: s.get("Health", {}).get("Status") == "unhealthy"),
]
try:
    docker("info")
    for label, options, expected, predicate in faults:
        name = prefix + "-" + label
        names.append(name)
        docker("run", "-d", "--name", name, *options)
        wait_for(name, predicate)
        evidence = json.loads(subprocess.check_output([args.binary, "inspect", name], text=True))
        cases.append({"name": label, "evidence": evidence, "expected": expected})
    path = pathlib.Path(args.output)
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_text(json.dumps(cases, indent=2) + "\n")
    subprocess.run([args.binary, "eval", "--provider", args.provider, "--cases", str(path)], check=True)
finally:
    for name in names:
        subprocess.run(["docker", "rm", "-f", name], stdout=subprocess.DEVNULL, check=False)
