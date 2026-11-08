//go:build !windows

package alerts

import (
	"path/filepath"
	"syscall"
)

// diskUsage возвращает процент занятого места и объём данных на файловой системе.
func diskUsage(path string) (percent int, used uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(filepath.Dir(path), &st); err != nil {
		return 0, 0, err
	}
	total := st.Blocks * uint64(st.Bsize)
	free := st.Bavail * uint64(st.Bsize)
	if total == 0 {
		return 0, 0, nil
	}
	return int((total - free) * 100 / total), total - free, nil
}
