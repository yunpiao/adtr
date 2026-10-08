//go:build !linux

package systemhealth

func readProc(ProcFile) ([]byte, error) {
	return nil, ErrUnsupported
}

func statFS(string) (FileSystemStats, error) {
	return FileSystemStats{}, ErrUnsupported
}
