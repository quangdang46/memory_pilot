//go:build darwin

package platform

import (
	"memory_pilot/internal/platform/macos"
	"memory_pilot/platform/contract"
)

func NewPlatform() (contract.Platform, error) { return macos.New() }
