// Package platform: per-OS lookup of the concrete platform.
package platform

import (
	"errors"

	"memory_pilot/platform/contract"
)

var ErrNoPlatform = errors.New("no platform for this OS")

type Platform = contract.Platform
