//go:build !windows

package web

import (
	"path/filepath"
	"syscall"
)

// diskFree возвращает свободное и общее место на файловой системе пути.
// На сервере (Linux) — через statfs; /readyz отдаёт процент в ответе.
func diskFree(path string) (free, total uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(filepath.Dir(path), &st); err != nil {
		return 0, 0, err
	}
	return st.Bavail * uint64(st.Bsize), st.Blocks * uint64(st.Bsize), nil
}
