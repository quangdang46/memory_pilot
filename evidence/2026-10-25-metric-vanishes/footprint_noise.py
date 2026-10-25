"""Is phys_footprint stable enough to measure small growth against?

A process holds a fixed 200MB and does nothing. Sample both metrics for 60s.
A metric used for growth detection must not wander on a static process.
"""
import ctypes, subprocess, sys, time
libc = ctypes.CDLL(None)
class R(ctypes.Structure):
    _fields_=[("uuid",ctypes.c_uint8*16)]+[(f"f{i}",ctypes.c_uint64) for i in range(40)]
def fp(pid):
    r=R(); rc=libc.proc_pid_rusage(ctypes.c_int(pid),ctypes.c_int(4),ctypes.byref(r),None,None,None)
    return r.f7/2**20 if rc==0 else float('nan')
def rss(pid):
    o=subprocess.run(["ps","-o","rss=","-p",str(pid)],capture_output=True,text=True).stdout.strip()
    return int(o or 0)/1024
CODE=r'''
import os,time
held=[bytearray(os.urandom(10<<20)) for _ in range(20)]
print("ready",flush=True); time.sleep(70)
'''
p=subprocess.Popen([sys.executable,"-c",CODE],stdout=subprocess.PIPE,text=True)
p.stdout.readline()
fps=[]; rsss=[]
for i in range(13):
    fps.append(fp(p.pid)); rsss.append(rss(p.pid)); time.sleep(5)
p.wait()
def spread(v): return max(v)-min(v)
print(f"footprint: min={min(fps):.1f} max={max(fps):.1f} spread={spread(fps):.1f}MB")
print(f"rss      : min={min(rsss):.1f} max={max(rsss):.1f} spread={spread(rsss):.1f}MB")
print()
print("A stable metric on an idle 200MB process should have spread near 0.")
