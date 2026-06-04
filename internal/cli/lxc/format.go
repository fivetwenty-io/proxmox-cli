package lxc

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

// fmtUptime renders a duration in seconds as a compact d/h/m string. Zero or
// negative renders as "-".
func fmtUptime(secs int64) string {
	if secs <= 0 {
		return "-"
	}
	d := secs / 86400
	h := (secs % 86400) / 3600
	m := (secs % 3600) / 60
	switch {
	case d > 0:
		return fmt.Sprintf("%dd%dh", d, h)
	case h > 0:
		return fmt.Sprintf("%dh%dm", h, m)
	default:
		return fmt.Sprintf("%dm", m)
	}
}

// fmtFloat renders a *float64 with two decimals, or "-" when nil.
func fmtFloat(f *float64) string {
	if f == nil {
		return "-"
	}
	return fmt.Sprintf("%.2f", *f)
}

// derefStr returns the pointed-to string or "" when nil.
func derefStr(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// derefInt returns the pointed-to int64 or 0 when nil.
func derefInt(i *int64) int64 {
	if i == nil {
		return 0
	}
	return *i
}
