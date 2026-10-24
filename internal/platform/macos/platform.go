//go:build darwin

// macOS platform implementation. Evidence: spike/mach (S-1), spike/pressure
// (S-3), spike/footprint (S-5/S-7). Coverage counts are part of the return
// value (MP-REQ-032); a dead collector never looks calm (SI-4).
package macos

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"memory_pilot/internal/model"
	"memory_pilot/platform/contract"
)

type Impl struct {
	pageSize uint64
	totalRAM uint64

	mu    sync.Mutex
	start map[int32]uint64 // pid -> ri_proc_start_abstime for identity
}

func New() (*Impl, error) {
	if err := loadLibSystem(); err != nil {
		return nil, err
	}
	ps, ok := sysctlUint64("hw.pagesize")
	if !ok {
		ps = 16384
	}
	ram, ok := sysctlUint64("hw.memsize")
	if !ok {
		ram = 0
	}
	return &Impl{pageSize: ps, totalRAM: ram, start: map[int32]uint64{}}, nil
}

func (i *Impl) Name() string { return "macos" }

func (i *Impl) Info() contract.PlatformInfo {
	osv, _ := sysctlInt32("kern.osproductversion") // dummy read keeps helper used
	_ = osv
	return contract.PlatformInfo{PageSizeBytes: i.pageSize, TotalRAMBytes: i.totalRAM}
}

// SampleSystem reads host_statistics64 + kern.memorystatus_vm_pressure_level.
func (i *Impl) SampleSystem(ctx context.Context) (model.SystemSample, error) {
	if err := ctx.Err(); err != nil {
		return model.SystemSample{}, err
	}
	st, ret := hostStats()
	s := model.SystemSample{
		At:               time.Now(),
		MonotonicMs:      machContinuousMs(),
		PageBytes:        i.pageSize,
		FreePages:        uint64(st.Free),
		SpecPages:        uint64(st.Speculative),
		ActivePages:      uint64(st.Active),
		InactivePages:    uint64(st.Inactive),
		WiredPages:       uint64(st.Wire),
		CompressorPages:  uint64(st.CompressorPage),
		InternalPages:    uint64(st.Internal),
		ExternalPages:    uint64(st.External),
		Swapins:          st.Swapins,
		Swapouts:         st.Swapouts,
		MachStatsQuality: model.QualGood,
	}
	if ret != 0 {
		// Failed read must NOT look like zeros-as-calm: mark missing (SI-4).
		s.MachStatsQuality = model.QualMissing
		return s, fmt.Errorf("host_statistics64 kern_return=%d", ret)
	}
	if lvl, ok := sysctlInt32("kern.memorystatus_vm_pressure_level"); ok {
		s.KernelLevel = decodeLevel(lvl)
		s.KernelLevelQuality = model.QualGood
	} else {
		s.KernelLevelQuality = model.QualMissing
	}
	if ms, ok := sysctlInt32("kern.memorystatus_level"); ok {
		s.MemStatusLevel = ms
	} else {
		s.MemStatusLevel = -1
	}
	return s, nil
}

// decodeLevel — masks 1/2/4 (S-3); anything else is Unknown, never calm.
func decodeLevel(v int32) model.PressureLevel {
	switch v {
	case 1, 2, 4:
		return model.PressureLevel(v)
	default:
		return model.LevelUnknown
	}
}

// Processes enumerates processes with the identity tuple and phys_footprint.
func (i *Impl) Processes(ctx context.Context) (model.ProcessSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return model.ProcessSnapshot{}, err
	}
	pids := listPids()
	snap := model.ProcessSnapshot{
		At:          time.Now(),
		MonotonicMs: machContinuousMs(),
		Processes:   make([]model.ProcessSample, 0, len(pids)),
		Quality:     model.QualGood,
	}
	snap.Coverage.Attempted = len(pids)
	for _, pid := range pids {
		if err := ctx.Err(); err != nil {
			snap.Quality = model.QualDegraded
			break
		}
		ru, ok := rusageV4(pid)
		if !ok {
			// An unprivileged daemon cannot read root or other users'
			// processes — that is the boundary of the design, not a failure.
			// Counting them as "denied" made coverage read 228/657 broken on
			// an idle desktop, and a permanently-bad coverage number trains
			// the user to ignore the one place that matters.
			if uid, known := bsdUID(pid); !known || uid != os.Getuid() {
				snap.Coverage.Foreign++
			} else {
				snap.Coverage.Denied++
			}
			continue
		}
		// Identity tuple is (pid, start_sec, start_usec) + executable name
		// (MP-ALG-004 §2). Without the start time a recycled pid would be
		// read as one continuous series — the double-counting bug the spec
		// calls out.
		bi, bsdOK := bsdInfo(pid)
		ident := model.ProcessIdentity{Pid: pid}
		quality := model.QualGood
		if bsdOK {
			ident.StartTime = time.Unix(int64(bi.StartTvsec), int64(bi.StartTvusec)*1000)
			ident.Executable = bi.commName()
			ident.Ppid = int32(bi.Ppid)
		} else {
			quality = model.QualDegraded
		}
		snap.Coverage.Succeeded++
		if st, ok := taskInfo(pid); ok {
			snap.Processes = append(snap.Processes, model.ProcessSample{
				Identity:       ident,
				FootprintBytes: ru.PhysFootprint,
				ResidentBytes:  st.ResidentSize,
				CpuUserS:       float64(ru.UserTime) / 1e9,
				CpuSystemS:     float64(ru.SystemTime) / 1e9,
				Quality:        quality,
			})
		} else {
			snap.Processes = append(snap.Processes, model.ProcessSample{
				Identity:       ident,
				FootprintBytes: ru.PhysFootprint,
				ResidentBytes:  ru.ResidentSize,
				Quality:        model.QualDegraded,
			})
		}
	}
	if snap.Coverage.Succeeded == 0 && snap.Coverage.Attempted > 0 {
		snap.Quality = model.QualMissing
		return snap, fmt.Errorf("no processes readable (attempted %d)", snap.Coverage.Attempted)
	}
	return snap, nil
}
