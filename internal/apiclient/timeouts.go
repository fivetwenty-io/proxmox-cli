package apiclient

import (
	"math"
	"time"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
)

// defaultRequestTimeout is the kit's own request timeout, written out here
// rather than left as a zero, so that a caller which builds a bare
// http.Client from a TimeoutSpec gets a real bound. pve.Options.setDefaults
// falls back to the same thirty seconds when Timeout is left unset, but
// DefaultTimeoutSpec must return a value a caller can rely on without going
// through that fallback.
const defaultRequestTimeout = 30 * time.Second

// TimeoutSpec bounds one client's transport. Every field must be set, and
// the resolver fills all three before it hands the specification to
// ApplyTimeoutOptions.
type TimeoutSpec struct {
	Connect      time.Duration
	TLSHandshake time.Duration
	Request      time.Duration
}

// DefaultTimeoutSpec returns the resolved defaults every connection starts
// from. It builds Connect and TLSHandshake from the existing
// defaultDialTimeoutSec and defaultTLSHandshakeTimeoutSec constants, so the
// defaults have one source, and it sets Request to the kit's own
// thirty-second request timeout.
func DefaultTimeoutSpec() TimeoutSpec {
	return TimeoutSpec{
		Connect:      defaultDialTimeoutSec * time.Second,
		TLSHandshake: defaultTLSHandshakeTimeoutSec * time.Second,
		Request:      defaultRequestTimeout,
	}
}

// ApplyTimeoutOptions overwrites opts.DialTimeoutSec, opts.TLSHandshakeTimeoutSec,
// and opts.Timeout from t. It requires a fully resolved t and has no branch
// for a zero field: every duration is rounded up to whole seconds with a
// floor of one second before it lands on the kit's integer-seconds fields,
// so a sub-second bound never truncates to zero, which the kit reads as "no
// bound" rather than as the tight bound the caller asked for.
func ApplyTimeoutOptions(opts pve.Options, t TimeoutSpec) pve.Options {
	opts.DialTimeoutSec = ceilSecondsFloorOne(t.Connect)
	opts.TLSHandshakeTimeoutSec = ceilSecondsFloorOne(t.TLSHandshake)
	opts.Timeout = t.Request

	return opts
}

// ceilSecondsFloorOne rounds d up to whole seconds with a floor of one
// second: max(1, ceil(d / time.Second)), capped at math.MaxInt32. A
// duration of zero or less still floors to one, because the kit's
// integer-seconds fields read zero as "no explicit timeout" rather than as
// "no delay". The division and remainder run in int64 rather than
// pre-adding time.Second-1 to d, because that addition overflows for any d
// within one second of time.Duration's maximum and would wrap the result
// negative, turning a caller's effectively-unbounded request into a
// one-second one.
func ceilSecondsFloorOne(d time.Duration) int {
	secs := int64(d / time.Second)
	if d%time.Second != 0 {
		secs++
	}

	if secs < 1 {
		secs = 1
	}
	if secs > math.MaxInt32 {
		secs = math.MaxInt32
	}

	return int(secs)
}
