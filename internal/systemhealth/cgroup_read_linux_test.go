//go:build linux

package systemhealth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCgroupReadRejectsEscapingSymlinkAndBoundsBytes(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "private")
	if err := os.WriteFile(outside, []byte("not a kernel metric"), 0600); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "memory.max")
	if err := os.Symlink(outside, file); err != nil {
		t.Fatal(err)
	}
	if data, err := readCgroupFile(file); err == nil || data != nil {
		t.Fatal("escaping control-file symlink accepted")
	}
	if err := os.Remove(file); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(strings.Repeat("1", maxProcBytes+20)), 0600); err != nil {
		t.Fatal(err)
	}
	data, err := readCgroupFile(file)
	if err != nil || len(data) != maxProcBytes+1 {
		t.Fatal("kernel read is not bounded")
	}
}
