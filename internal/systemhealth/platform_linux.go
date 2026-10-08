//go:build linux

package systemhealth

import (
	"io"
	"os"
	"strconv"
	"syscall"
)

func readProc(file ProcFile) ([]byte, error) {
	var path string
	switch file {
	case ProcStat:
		path = "/proc/stat"
	case ProcMeminfo:
		path = "/proc/meminfo"
	case ProcUptime:
		path = "/proc/uptime"
	case ProcLoadavg:
		path = "/proc/loadavg"
	default:
		return nil, errInvalidData
	}
	fileHandle, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer fileHandle.Close()
	return io.ReadAll(io.LimitReader(fileHandle, maxProcBytes+1))
}

func statFS(path string) (FileSystemStats, error) {
	var stats syscall.Statfs_t
	if err := syscall.Statfs(path, &stats); err != nil {
		return FileSystemStats{}, err
	}
	return FileSystemStats{
		BlockSize: int64(stats.Bsize), Blocks: uint64(stats.Blocks),
		FreeBlocks: uint64(stats.Bfree), AvailableBlocks: uint64(stats.Bavail),
		FS: filesystemName(uint64(stats.Type)),
	}, nil
}

func filesystemName(kind uint64) string {
	// statfs reports filesystem type, not a device path or a remote endpoint.
	switch kind {
	case 0x794c7630:
		return "overlay"
	case 0xef53:
		return "ext"
	case 0x58465342:
		return "xfs"
	case 0x9123683e:
		return "btrfs"
	case 0x01021994:
		return "tmpfs"
	case 0x6969:
		return "nfs"
	case 0x2fc12fc1:
		return "zfs"
	case 0x65735546:
		return "fuse"
	default:
		return "type-0x" + strconv.FormatUint(kind, 16)
	}
}
