//go:build windows

package status

import "fmt"

// disk на Windows не измеряется: целевая среда — Linux, локальная сборка только проверяет компиляцию.
func disk(string) (pct int, total, free uint64, err error) {
	return 0, 0, 0, fmt.Errorf("измерение диска доступно только на Linux")
}
