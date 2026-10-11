//go:build linux

package systemhealth

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"strings"
	"syscall"
	"time"
)

func readContainerMemory(now time.Time) ContainerMemoryMetric {
	return sampleCgroup(now, readCgroupFile, cgroupIdentity)
}
func readCgroupFile(file string) ([]byte, error) {
	var f *os.File
	var err error
	if file == "/proc/self/cgroup" || file == "/proc/self/mountinfo" {
		f, err = os.Open(file)
	} else {
		split := strings.LastIndexByte(file, '/')
		if split <= 0 {
			return nil, errInvalidData
		}
		root, e := os.OpenRoot(file[:split])
		if e != nil {
			return nil, e
		}
		defer root.Close()
		f, err = root.Open(file[split+1:])
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, maxProcBytes+1))
}

// This opaque continuity token is not a permanent container ID. Boot identity,
// namespace, directory inode and change time distinguish observable replacements.
func cgroupIdentity(directory string) (string, error) {
	ns, err := os.Readlink("/proc/self/ns/cgroup")
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(ns, "cgroup:[") || !strings.HasSuffix(ns, "]") {
		return "", errInvalidData
	}
	if _, err = unsigned(strings.TrimSuffix(strings.TrimPrefix(ns, "cgroup:["), "]")); err != nil {
		return "", err
	}
	f, err := os.Open("/proc/sys/kernel/random/boot_id")
	if err != nil {
		return "", err
	}
	boot, err := io.ReadAll(io.LimitReader(f, 65))
	f.Close()
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(string(boot))
	if len(id) != 36 {
		return "", errInvalidData
	}
	for i, c := range id {
		if i == 8 || i == 13 || i == 18 || i == 23 {
			if c != '-' {
				return "", errInvalidData
			}
		} else if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return "", errInvalidData
		}
	}
	root, err := os.OpenRoot(directory)
	if err != nil {
		return "", err
	}
	defer root.Close()
	info, err := root.Stat(".")
	if err != nil {
		return "", err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() {
		return "", errInvalidData
	}
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s:%s:%d:%d:%d:%d", id, ns, stat.Dev, stat.Ino, stat.Ctim.Sec, stat.Ctim.Nsec)))
	return hex.EncodeToString(sum[:]), nil
}
