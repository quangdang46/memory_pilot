// Package platform defines the hardware-facing contracts (MP-ARCH-002 §2).
package contract

import (
	"context"
	"errors"

	"memory_pilot/internal/model"
)

var (
	ErrUnsupported = errors.New("platform unsupported")
	ErrDenied      = errors.New("operation denied")
	ErrUnavailable = errors.New("source unavailable")
)

type Source interface {
	Name() string
	SampleSystem(ctx context.Context) (model.SystemSample, error)
}

type ProcessSource interface {
	Processes(ctx context.Context) (model.ProcessSnapshot, error)
}

type PlatformInfo struct {
	PageSizeBytes uint64
	TotalRAMBytes uint64
	OSVersion     string
}

type Platform interface {
	Source
	ProcessSource
	Info() PlatformInfo
}
