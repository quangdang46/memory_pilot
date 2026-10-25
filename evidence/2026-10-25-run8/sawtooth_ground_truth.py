#!/usr/bin/env python3
"""Ground truth for the sawtooth probe: does truncating a slice release pages?

Measured answer on darwin 25.4: no. The Go runtime keeps the heap, so
footprint is monotonically non-decreasing and the "cache" label for this probe
was wrong. The daemon calling it sustained growth was correct.
"""
import ctypes
import subprocess
import sys
import time

libc = ctypes.CDLL(None)


class R(ctypes.Structure):
    _fields_ = [("uuid", ctypes.c_uint8 * 16)] + [
        (f"f{i}", ctypes.c_uint64) for i in range(40)
    ]


def main():
    pids = subprocess.run(["pgrep", "-f", "probe-sawtooth -mode"],
                          capture_output=True, text=True).stdout.split()
    if not pids:
        sys.exit("start probe-sawtooth first: ./probe-sawtooth -mode sawtooth -mb 10 -min 10")
    pid = int(pids[0])
    vals = []
    for _ in range(20):
        r = R()
        libc.proc_pid_rusage(ctypes.c_int(pid), ctypes.c_int(4),
                             ctypes.byref(r), None, None, None)
        vals.append(r.f7 / 2**20)
        time.sleep(2)
    print("footprint over 40s:", " ".join(f"{v:.0f}" for v in vals))
    print(f"min {min(vals):.0f}  max {max(vals):.0f}")
    non_decreasing = all(vals[i] <= vals[i + 1] + 3 for i in range(len(vals) - 1))
    print("non-decreasing (never releases):", non_decreasing)


if __name__ == "__main__":
    main()
