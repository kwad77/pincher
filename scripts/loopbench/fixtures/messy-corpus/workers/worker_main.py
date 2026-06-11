"""orderflow fulfillment worker.

Reads JSON-line jobs published by the Go service (internal/queue) and routes
each job to whatever handler registered its job_type. The mapping lives in
lib/registry.py and is populated by decorator side effects when the handlers
package is imported -- there is no direct call from this file to any handler.
"""

import json
import sys
import time

import handlers  # noqa: F401  -- side effect: registers all live handlers
from lib.registry import HANDLERS

QUEUE_PATH = "/tmp/orderflow-queue.jsonl"


def run_forever():
    while True:
        drain_once()
        time.sleep(1.0)


def drain_once():
    try:
        with open(QUEUE_PATH) as fh:
            lines = fh.readlines()
    except FileNotFoundError:
        return
    for line in lines:
        job = json.loads(line)
        route(job)


def route(job):
    job_type = job.get("job_type", "")
    handler = HANDLERS.get(job_type)
    if handler is None:
        print(f"worker: no handler for job_type={job_type!r}", file=sys.stderr)
        return
    handler(job)


if __name__ == "__main__":
    run_forever()
