import ctypes, subprocess, time, sys
libc = ctypes.CDLL("/usr/lib/libSystem.B.dylib")
class R(ctypes.Structure):
    _fields_=[("uuid",ctypes.c_uint8*16)]+[(f"f{i}",ctypes.c_uint64) for i in range(40)]
code = r'''
import os, time
held=[]
blk=os.urandom(20<<20)
for i in range(20):
    held.append(bytearray(blk)); time.sleep(0.5)
print("ready",flush=True); time.sleep(50)
'''
p=subprocess.Popen([sys.executable,"-c",code],stdout=subprocess.PIPE,text=True)
p.stdout.readline()
def ru():
    r=R(); libc.proc_pid_rusage(ctypes.c_int(p.pid),ctypes.c_int(4),ctypes.byref(r),None,None,None); return r
print(f"{'t':>4} {'ps_rss':>10} {'rusage_resident':>16} {'rusage_footprint':>18}")
for i in range(10):
    pss=subprocess.run(["ps","-o","rss=","-p",str(p.pid)],capture_output=True,text=True).stdout.strip() or "0"
    r=ru()
    print(f"{i*5:>4} {int(pss)/1024:>9.1f}M {r.f6/2**20:>15.1f}M {r.f7/2**20:>17.1f}M")
    time.sleep(5)
p.wait()
