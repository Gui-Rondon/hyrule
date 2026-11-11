package app

import (
	"errors"
	"io/fs"
	"testing"
)

func TestContainerMemoryLimit(t *testing.T) {
	const mi = 1 << 20
	for _, tc := range []struct {
		name     string
		files    map[string]string
		env      string
		want     int64
		wantSets bool
	}{
		{name: "cgroup v2", files: map[string]string{cgroupV2: "268435456\n"}, want: 256 * mi * 9 / 10, wantSets: true},
		{name: "cgroup v2 without limit", files: map[string]string{cgroupV2: "max\n"}},
		{name: "cgroup v1", files: map[string]string{cgroupV1: "134217728\n"}, want: 128 * mi * 9 / 10, wantSets: true},
		// v1 without a limit is the page-aligned max int64
		{name: "cgroup v1 without limit", files: map[string]string{cgroupV1: "9223372036854771712\n"}},
		{name: "GOMEMLIMIT set", files: map[string]string{cgroupV2: "268435456\n"}, env: "200MiB"},
		{name: "no cgroup files"},
		{name: "invalid", files: map[string]string{cgroupV2: "abc\n"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			read := func(path string) ([]byte, error) {
				if v, ok := tc.files[path]; ok {
					return []byte(v), nil
				}
				return nil, fs.ErrNotExist
			}
			got, ok := containerMemoryLimit(read, func(string) string { return tc.env })
			if ok != tc.wantSets || got != tc.want {
				t.Errorf("= %d %v, want %d %v", got, ok, tc.want, tc.wantSets)
			}
		})
	}
	// a read error other than a missing file is ignored too
	if _, ok := containerMemoryLimit(func(string) ([]byte, error) { return nil, errors.New("eperm") },
		func(string) string { return "" }); ok {
		t.Error("read error: limit set")
	}
}
