//go:build windows

package alerts

// diskUsage на Windows не измеряется: целевая среда — Linux.
func diskUsage(string) (percent int, used uint64, err error) { return 0, 0, nil }
