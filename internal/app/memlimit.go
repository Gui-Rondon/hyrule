package app

import (
	"log/slog"
	"os"
	"runtime/debug"
	"strconv"
	"strings"
)

const (
	cgroupV2 = "/sys/fs/cgroup/memory.max"
	cgroupV1 = "/sys/fs/cgroup/memory/memory.limit_in_bytes"
	// limits at or above this are "no limit" (v1 reports a page-aligned
	// max int64)
	noLimit = 1 << 62
)

// setMemoryLimit gives the GC a soft limit of 90% of the container's memory
// limit, so that the heap is collected before the pod is OOMKilled: with
// the default GOGC the heap grows to twice the live data, and a few
// concurrent large responses go past the limit. GOMEMLIMIT, when set, wins.
func setMemoryLimit() {
	if limit, ok := containerMemoryLimit(os.ReadFile, os.Getenv); ok {
		debug.SetMemoryLimit(limit)
		slog.Info("memory limit set from the container", "bytes", limit)
	}
}

func containerMemoryLimit(read func(string) ([]byte, error), getenv func(string) string) (int64, bool) {
	if getenv("GOMEMLIMIT") != "" {
		return 0, false
	}
	for _, path := range []string{cgroupV2, cgroupV1} {
		raw, err := read(path)
		if err != nil {
			continue
		}
		v, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
		if err != nil || v <= 0 || v >= noLimit {
			return 0, false
		}
		// v*9/10 without overflowing near the int64 limit
		return v/10*9 + v%10*9/10, true
	}
	return 0, false
}
