package contract

// DiskHeadroomSource reports boot-volume headroom (swap-risk input, gk9.1).
// ok=false is missing evidence and must be surfaced, never zeroed (SI-4).
type DiskHeadroomSource interface {
	DiskHeadroom() (freeBytes, totalBytes uint64, ok bool)
}
