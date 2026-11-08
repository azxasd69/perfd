//go:build windows

package web

// diskFree на Windows не измеряется: целевая среда — Linux, а локальные
// проверки не должны падать из-за отсутствия statfs. /readyz просто не отдаст процент.
func diskFree(string) (free, total uint64, err error) { return 0, 0, nil }
