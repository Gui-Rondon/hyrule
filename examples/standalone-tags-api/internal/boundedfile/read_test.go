package boundedfile

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"testing"
)

func TestRegularFileBounds(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "credential")
	if err := os.WriteFile(file, []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int64{6, 7} {
		data, err := Read(file, limit)
		if err != nil || string(data) != "secret" {
			t.Fatal(data, err)
		}
	}
	for _, limit := range []int64{-1, 0, 5, math.MaxInt64} {
		data, err := Read(file, limit)
		if !errors.Is(err, ErrLimit) || data != nil {
			t.Fatal(limit, data, err)
		}
	}
	if _, err := Read(dir, 100); !errors.Is(err, ErrRegular) {
		t.Fatal(err)
	}
	if _, err := Read(filepath.Join(dir, "missing"), 100); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	empty := filepath.Join(dir, "empty")
	if err := os.WriteFile(empty, nil, 0600); err != nil {
		t.Fatal(err)
	}
	if data, err := Read(empty, 0); err != nil || len(data) != 0 {
		t.Fatal(data, err)
	}
}
func TestSingleDescriptorSurvivesPathReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "document")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	opens := 0
	data, err := read(func() (*os.File, error) {
		opens++
		f, e := os.Open(path)
		if e != nil {
			return nil, e
		}
		// The original descriptor remains a regular file, even though its path now
		// names a directory. Validation and reading must both use that descriptor.
		if e = os.Rename(path, path+".old"); e != nil {
			f.Close()
			t.Skipf("open-file rename unavailable: %v", e)
		}
		if e = os.Mkdir(path, 0700); e != nil {
			f.Close()
			return nil, e
		}
		return f, nil
	}, 8)
	if err != nil || string(data) != "original" || opens != 1 {
		t.Fatal(string(data), opens, err)
	}
}
func TestRootConfinement(t *testing.T) {
	dir := t.TempDir()
	rootDir := filepath.Join(dir, "root")
	if err := os.Mkdir(rootDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "outside"), []byte("secret"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootDir, "inside"), []byte("safe"), 0600); err != nil {
		t.Fatal(err)
	}
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		t.Fatal(err)
	}
	defer root.Close()
	if data, err := ReadRoot(root, "inside", 4); err != nil || string(data) != "safe" {
		t.Fatal(data, err)
	}
	if _, err = ReadRoot(root, "../outside", 100); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err = ReadRoot(nil, "inside", 100); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if err = os.Symlink("../outside", filepath.Join(rootDir, "escape")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err = ReadRoot(root, "escape", 100); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}
