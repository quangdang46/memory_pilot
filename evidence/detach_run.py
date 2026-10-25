#!/usr/bin/env python3
"""detach_run.py <run-dir> <minutes>

Starts a labelled run from a double-forked process so the probes' ancestry ends
at launchd instead of at a terminal.

Memory Pilot groups by nearest *anchor ancestor*, and a terminal is an anchor.
Probes started from an interactive shell are therefore attributed to that
terminal, not to themselves, which makes per-probe verdicts unmeasurable
(evidence/2026-10-25-run12-NOTES.md). The parent here must stay alive so that
probe-children keeps its own children: a run whose probes reparent to launchd
cannot test subprocess accumulation (see verify_inputs.py).
"""
import os
import sys

run, minutes = os.path.abspath(sys.argv[1]), sys.argv[2]
out = open(os.path.join(run, "launcher.log"), "w")

if os.fork() > 0:
    sys.exit(0)
os.setsid()
if os.fork() > 0:
    os._exit(0)

os.chdir(os.path.dirname(os.path.abspath(__file__)))
devnull = os.open(os.devnull, os.O_RDWR)
os.dup2(devnull, 0)
os.dup2(out.fileno(), 1)
os.dup2(out.fileno(), 2)
os.execv("/bin/sh", ["/bin/sh", "/tmp/mp-evidence/start_run.sh", run, minutes])
