//go:build !darwin

package platform

import "memory_pilot/platform/contract"

func NewPlatform() (contract.Platform, error) { return nil, ErrNoPlatform }
