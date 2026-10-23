//go:build !darwin

// Non-mac builds compile and fail loudly at runtime (module-contracts §2).
package other

import (
	"context"
	"errors"

	"memory_pilot/internal/model"
	"memory_pilot/platform/contract"
)

var ErrUnsupported = contract.ErrUnsupported

type Impl struct{}

func New() (*Impl, error) { return &Impl{}, nil }

func (i *Impl) Name() string { return "unsupported" }

func (i *Impl) Info() contract.PlatformInfo { return contract.PlatformInfo{} }

func (i *Impl) SampleSystem(ctx context.Context) (model.SystemSample, error) {
	return model.SystemSample{}, ErrUnsupported
}

func (i *Impl) Processes(ctx context.Context) (model.ProcessSnapshot, error) {
	return model.ProcessSnapshot{}, ErrUnsupported
}
