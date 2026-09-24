package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

// ── ParseTimeout ─────────────────────────────

func TestParseTimeout_Empty_ReturnsZeroNoError(t *testing.T) {
	d, err := config.ParseTimeout("timeout.connect", "")
	require.NoError(t, err)
	require.Zero(t, d)
}

func TestParseTimeout_Valid_ReturnsDuration(t *testing.T) {
	d, err := config.ParseTimeout("timeout.request", "1m30s")
	require.NoError(t, err)
	require.Equal(t, time.Minute+30*time.Second, d)
}

func TestParseTimeout_Unparseable_NamesFieldAndValue(t *testing.T) {
	_, err := config.ParseTimeout("timeout.connect", "5 seconds")
	require.Error(t, err)
	require.Equal(t, `timeout.connect "5 seconds" is not a duration (e.g. 5s, 500ms)`, err.Error())
}

func TestParseTimeout_Zero_ReturnsGreaterThanZeroError(t *testing.T) {
	_, err := config.ParseTimeout("timeout.connect", "0s")
	require.Error(t, err)
	require.Equal(t, "timeout.connect must be greater than zero", err.Error())
}

func TestParseTimeout_Negative_ReturnsGreaterThanZeroError(t *testing.T) {
	_, err := config.ParseTimeout("timeout.tls-handshake", "-5s")
	require.Error(t, err)
	require.Equal(t, "timeout.tls-handshake must be greater than zero", err.Error())
}

// ── (*Context).ParsedTimeouts ────────────────────────

func TestContextParsedTimeouts_AllSet_ParsesEachField(t *testing.T) {
	c := &config.Context{
		Timeout: config.TimeoutBlock{
			Connect:      "5s",
			TLSHandshake: "10s",
			Request:      "30s",
		},
	}
	got, err := c.ParsedTimeouts()
	require.NoError(t, err)
	require.Equal(t, 5*time.Second, got.Connect)
	require.Equal(t, 10*time.Second, got.TLSHandshake)
	require.Equal(t, 30*time.Second, got.Request)
}

func TestContextParsedTimeouts_AllUnset_ParsesToZero(t *testing.T) {
	c := &config.Context{}
	got, err := c.ParsedTimeouts()
	require.NoError(t, err)
	require.Zero(t, got.Connect)
	require.Zero(t, got.TLSHandshake)
	require.Zero(t, got.Request)
}

func TestContextParsedTimeouts_FirstBadFieldErrors(t *testing.T) {
	c := &config.Context{
		Timeout: config.TimeoutBlock{
			Connect:      "not-a-duration",
			TLSHandshake: "10s",
			Request:      "30s",
		},
	}
	_, err := c.ParsedTimeouts()
	require.Error(t, err)
	require.Contains(t, err.Error(), "timeout.connect")
}

func TestContextParsedTimeouts_SecondFieldBad_NamesTLSHandshake(t *testing.T) {
	c := &config.Context{
		Timeout: config.TimeoutBlock{
			Connect:      "5s",
			TLSHandshake: "0s",
			Request:      "30s",
		},
	}
	_, err := c.ParsedTimeouts()
	require.Error(t, err)
	require.Equal(t, "timeout.tls-handshake must be greater than zero", err.Error())
}

func TestContextParsedTimeouts_ThirdFieldBad_NamesRequest(t *testing.T) {
	c := &config.Context{
		Timeout: config.TimeoutBlock{
			Connect:      "5s",
			TLSHandshake: "10s",
			Request:      "bogus",
		},
	}
	_, err := c.ParsedTimeouts()
	require.Error(t, err)
	require.Equal(t, `timeout.request "bogus" is not a duration (e.g. 5s, 500ms)`, err.Error())
}
