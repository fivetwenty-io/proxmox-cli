package node

import (
	"fmt"
)

// fmtBytes renders a byte count in human-friendly units. Zero renders as "0".
func fmtBytes(n int64) string {
	if n == 0 {
		return "0"
	}
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%dB", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%ciB", float64(n)/float64(div), "KMGTPE"[exp])
}
