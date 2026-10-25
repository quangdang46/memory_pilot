import os, subprocess, time, sys
# Allocate a fixed 400MB in a child, keep it referenced, and watch:
#   - RSS from ps (what Memory Pilot samples)
#   - phys_footprint from proc_pid_rusage (what the collector ALSO stores)
#   - system compressor pages
def vm(key):
    out = subprocess.run(["vm_stat"], capture_output=True, text=True).stdout
    for line in out.splitlines():
        if line.startswith(key):
            return int(line.split(":")[1].strip().rstrip("."))
    return -1

code = r'''
import os, time, sys
held = []
for i in range(20):
    held.append(bytearray(20<<20))
    for j in range(0, 20<<20, 4096):
        held[-1][j] = 1
    time.sleep(1)
print("allocated", len(held)*20, "MB", flush=True)
time.sleep(45)
'''
p = subprocess.Popen([sys.executable, "-c", code], stdout=subprocess.PIPE, text=True)
time.sleep(3)
print(f"{'t':>4} {'rss_MB':>8} {'compressor_pages':>17}")
comp0 = vm("Pages occupied by compressor")
for i in range(12):
    rss = subprocess.run(["ps", "-o", "rss=", "-p", str(p.pid)],
                         capture_output=True, text=True).stdout.strip() or "0"
    print(f"{i*3:>4} {int(rss)/1024:>8.1f} {vm('Pages occupied by compressor'):>17}")
    time.sleep(3)
p.wait()
