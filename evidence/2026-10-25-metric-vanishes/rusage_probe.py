import ctypes, ctypes.util, subprocess, sys, time

libc = ctypes.CDLL("/usr/lib/libSystem.B.dylib", use_errno=True)

# rusage_info_v4 is a flat array of uint64 after the 16-byte uuid.
class RUsageV4(ctypes.Structure):
    _fields_ = [("uuid", ctypes.c_uint8 * 16)] + [
        (f"f{i}", ctypes.c_uint64) for i in range(40)
    ]

NAMES = [
 "user_time","system_time","pkg_idle_wkups","interrupt_wkups","pageins",
 "wired_size","resident_size","phys_footprint","proc_start_abstime",
 "proc_exit_abstime","child_user_time","child_system_time",
 "child_pkg_idle_wkups","child_interrupt_wkups","child_pageins",
 "child_elapsed_abstime","diskio_bytesread","diskio_byteswritten",
 "cpu_time_qos_default","cpu_time_qos_maintenance","cpu_time_qos_background",
 "cpu_time_qos_utility","cpu_time_qos_legacy","cpu_time_qos_user_initiated",
 "cpu_time_qos_user_interactive","ri_service_time","ri_interrupt_wkups2",
 "ri_lifetime_max_phys_footprint","ri_instructions","ri_cycles",
 "ri_billed_energy","ri_serviced_energy","ri_interval_max_phys_footprint",
]

code = r'''
import os, time
held = []
blk = os.urandom(20<<20)
for i in range(20):
    held.append(bytearray(blk))
    time.sleep(1)
print("ready", flush=True)
time.sleep(40)
'''
p = subprocess.Popen([sys.executable, "-c", code], stdout=subprocess.PIPE, text=True)
p.stdout.readline()  # wait for "ready": 400MB allocated and held

def read(pid):
    ru = RUsageV4()
    r = libc.proc_pid_rusage(ctypes.c_int(pid), ctypes.c_int(4),
                             ctypes.byref(ru), None, None, None)
    return ru if r == 0 else None

print(f"{'field':<32}{'now':>14}")
ru = read(p.pid)
for i, name in enumerate(NAMES):
    v = getattr(ru, f"f{i}")
    if v > 1<<20:
        print(f"{name:<32}{v/2**20:>11.1f} MB")
print()
print("child still holds 400 MB of touched, incompressible data.")
print("watch resident_size / phys_footprint fall while the process lives:")
for i in range(8):
    ru = read(p.pid)
    print(f"  t={i*5:>2}s resident={ru.f7/2**20:7.1f}MB "
          f"footprint={ru.f8/2**20:7.1f}MB "
          f"lifetime_max={ru.f27/2**20:7.1f}MB")
    time.sleep(5)
p.wait()
