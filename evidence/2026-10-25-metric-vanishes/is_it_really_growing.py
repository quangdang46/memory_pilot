import ctypes, subprocess, sys, time
libc = ctypes.CDLL(None)
class R(ctypes.Structure):
    _fields_=[("uuid",ctypes.c_uint8*16)]+[(f"f{i}",ctypes.c_uint64) for i in range(40)]
def fp(pid):
    r=R(); rc=libc.proc_pid_rusage(ctypes.c_int(pid),ctypes.c_int(4),ctypes.byref(r),None,None,None)
    return r.f7/2**20 if rc==0 else float('nan')
# Child prints its own Python-level allocated bytes, so we know whether the
# process thinks it is still growing while the kernel says it is not.
CODE = r'''
import os, sys, time
held=[]
blk=os.urandom(5<<20)
for i in range(28):
    held.append(bytearray(blk))
    print(f"alloc={(i+1)*5}", flush=True)
    time.sleep(1)
time.sleep(20)
'''
p=subprocess.Popen([sys.executable,"-c",CODE],stdout=subprocess.PIPE,text=True)
print(f"{'child_says_MB':>14} {'footprint_MB':>13} {'ps_rss_MB':>10}")
last=None
for _ in range(30):
    line=p.stdout.readline().strip()
    if not line: break
    mb=int(line.split("=")[1])
    if mb % 25 == 0 or mb >= 130:
        rss=subprocess.run(["ps","-o","rss=","-p",str(p.pid)],capture_output=True,text=True).stdout.strip() or "0"
        print(f"{mb:>14} {fp(p.pid):>13.1f} {int(rss)/1024:>10.1f}")
p.wait()
