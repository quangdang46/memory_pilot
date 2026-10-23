//go:build !darwin

package selfmonitor

func selfRSSBytes() uint64   { return 0 }
func selfCPURusage() (float64, float64) { return 0, 0 }
