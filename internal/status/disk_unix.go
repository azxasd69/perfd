//go:build !windows

package status

import (
	"fmt"
	"path/filepath"
	"syscall"
)

// disk возвращает процент занятого места, общий и свободный объём.
func disk(path string) (pct int, total, free uint64, err error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(filepath.Dir(path), &st); err != nil {
		return 0, 0, 0, err
	}
	total = st.Blocks * uint64(st.Bsize)
	free = st.Bavail * uint64(st.Bsize)
	if total == 0 {
		return 0, 0, 0, fmt.Errorf("нулевой размер файловой системы")
	}
	return int((total - free) * 100 / total), total, free, nil
}
