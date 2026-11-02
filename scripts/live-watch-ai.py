#!/usr/bin/env python3
"""Verify the complete Docker event to AI report path on an empty Docker host."""
import json
import pathlib
import queue
import signal
import subprocess
import threading
import time
import uuid

binary = str(pathlib.Path("bin/whydown").resolve())
existing = subprocess.check_output(["docker", "ps", "-aq"], text=True).strip()
if existing:
    raise SystemExit("This optional check requires an empty Docker host so unrelated container logs are not sent to AI.")
name = "whydown-ai-watch-" + uuid.uuid4().hex[:8]
watch = subprocess.Popen([binary, "watch", "--provider", "groq", "--json"], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
output = queue.Queue()


def read_reports():
    for line in watch.stdout:
        output.put(json.loads(line))


threading.Thread(target=read_reports, daemon=True).start()
try:
    time.sleep(1)
    subprocess.run(["docker", "run", "-d", "--name", name, "--memory", "32m", "--memory-swap", "32m", "python:3.12-alpine", "python", "-c", "x = bytearray(256*1024*1024)"], check=True, capture_output=True)
    report = output.get(timeout=60)
    assert report["container"] == name and report["provider"] == "groq"
    assert "memory" in (report["cause"] + " " + report["summary"]).lower()
    assert report["fixes"] and report["evidence"]
    actual = json.loads(subprocess.check_output(["docker", "inspect", name], text=True))[0]
    assert actual["State"]["OOMKilled"]
    pathlib.Path("artifacts").mkdir(exist_ok=True)
    pathlib.Path("artifacts/live-ai-watch.json").write_text(json.dumps(report, indent=2) + "\n")
    print("PASS Docker OOM event to Groq diagnosis and fixes", flush=True)
finally:
    watch.send_signal(signal.SIGTERM)
    try:
        status = watch.wait(timeout=5)
    except subprocess.TimeoutExpired:
        watch.kill()
        watch.wait()
        raise
    finally:
        subprocess.run(["docker", "rm", "-f", name], capture_output=True, check=False)
        watch.stdout.close()
        watch.stderr.close()
    assert status == 0, "AI watcher did not stop cleanly"
