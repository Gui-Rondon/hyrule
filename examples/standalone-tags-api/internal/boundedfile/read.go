// Package boundedfile reads configured regular files through a single validated
// descriptor. Errors omit paths and file contents, including credentials.
package boundedfile

import (
	"errors"
	"io"
	"math"
	"os"
)

var (
	ErrUnavailable = errors.New("configured file unavailable")
	ErrRegular     = errors.New("regular file required")
	ErrLimit       = errors.New("configured file exceeds limit")
)

func Read(path string, limit int64) ([]byte, error) {
	return read(func() (*os.File, error) { return os.Open(path) }, limit)
}

// ReadRoot preserves os.Root confinement. Stat and read use the same descriptor;
// replacing its path cannot redirect the read to an unvalidated second file.
func ReadRoot(root *os.Root, path string, limit int64) ([]byte, error) {
	if root == nil {
		return nil, ErrUnavailable
	}
	return read(func() (*os.File, error) { return root.Open(path) }, limit)
}
func read(open func() (*os.File, error), limit int64) ([]byte, error) {
	if limit < 0 || limit == math.MaxInt64 {
		return nil, ErrLimit
	}
	f, err := open()
	if err != nil {
		return nil, ErrUnavailable
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, ErrUnavailable
	}
	if !info.Mode().IsRegular() {
		return nil, ErrRegular
	}
	if info.Size() > limit {
		return nil, ErrLimit
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, ErrUnavailable
	}
	if int64(len(data)) > limit {
		return nil, ErrLimit
	}
	return data, nil
}
