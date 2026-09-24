package apiclient

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	pve "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/client"
)

func TestApplyTimeoutOptions(t *testing.T) {
	spec := TimeoutSpec{
		Connect:      20 * time.Second,
		TLSHandshake: 30 * time.Second,
		Request:      120 * time.Second,
	}

	opts := ApplyTimeoutOptions(pve.Options{}, spec)

	require.Equal(t, 20, opts.DialTimeoutSec)
	require.Equal(t, 30, opts.TLSHandshakeTimeoutSec)
	require.Equal(t, 120*time.Second, opts.Timeout, "Request lands on opts.Timeout")
}

func TestApplyTimeoutOptions_Defaults(t *testing.T) {
	def := DefaultTimeoutSpec()

	require.Equal(t, 5*time.Second, def.Connect)
	require.Equal(t, 10*time.Second, def.TLSHandshake)
	require.Equal(t, 30*time.Second, def.Request)

	opts := ApplyTimeoutOptions(pve.Options{}, def)

	require.Equal(t, 5, opts.DialTimeoutSec)
	require.Equal(t, 10, opts.TLSHandshakeTimeoutSec)
	require.Equal(t, 30*time.Second, opts.Timeout)
}

func TestApplyTimeoutOptions_SubSecondFloorsToOne(t *testing.T) {
	spec := TimeoutSpec{
		Connect:      500 * time.Millisecond,
		TLSHandshake: 1400 * time.Millisecond,
		Request:      900 * time.Millisecond,
	}

	opts := ApplyTimeoutOptions(pve.Options{}, spec)

	require.Equal(t, 1, opts.DialTimeoutSec, "500ms must round up to one second, not truncate to zero")
	require.Equal(t, 2, opts.TLSHandshakeTimeoutSec, "1.4s must round up to two seconds")
	require.Equal(t, 900*time.Millisecond, opts.Timeout,
		"Request is a duration, not seconds, so it is carried through exactly")
}

func TestApplyTimeoutOptions_ZeroFloorsToOneRatherThanBranching(t *testing.T) {
	// ApplyTimeoutOptions has no branch for a zero field: it requires a
	// fully resolved TimeoutSpec, and a zero Connect or TLSHandshake still
	// rounds up to the one-second floor rather than landing on the SDK's
	// "no explicit timeout" zero.
	opts := ApplyTimeoutOptions(pve.Options{}, TimeoutSpec{})

	require.Equal(t, 1, opts.DialTimeoutSec)
	require.Equal(t, 1, opts.TLSHandshakeTimeoutSec)
	require.Zero(t, opts.Timeout)
}

// TestApplyTimeoutOptions_NearMaxDurationDoesNotOverflow pins that rounding
// up never wraps a near-maximum duration into a tiny bound: adding
// time.Second-1 to a duration within one second of time.Duration's maximum
// overflows int64 and wraps negative, which the old floor-of-one logic then
// read as a one-second bound — the opposite of "effectively unbounded".
func TestApplyTimeoutOptions_NearMaxDurationDoesNotOverflow(t *testing.T) {
	spec := TimeoutSpec{
		Connect:      time.Duration(math.MaxInt64),
		TLSHandshake: time.Duration(math.MaxInt64),
		Request:      time.Second,
	}

	opts := ApplyTimeoutOptions(pve.Options{}, spec)

	require.Equal(t, math.MaxInt32, opts.DialTimeoutSec,
		"a near-maximum duration must cap at math.MaxInt32 seconds, not overflow to a one-second bound")
	require.Equal(t, math.MaxInt32, opts.TLSHandshakeTimeoutSec)
}

func TestApplyTimeoutOptions_OverwritesExistingFields(t *testing.T) {
	opts := pve.Options{
		DialTimeoutSec:         99,
		TLSHandshakeTimeoutSec: 99,
		Timeout:                99 * time.Second,
	}

	opts = ApplyTimeoutOptions(opts, DefaultTimeoutSpec())

	require.Equal(t, 5, opts.DialTimeoutSec)
	require.Equal(t, 10, opts.TLSHandshakeTimeoutSec)
	require.Equal(t, 30*time.Second, opts.Timeout)
}
