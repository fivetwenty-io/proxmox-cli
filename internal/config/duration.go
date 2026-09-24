package config

import (
	"fmt"
	"time"
)

// ParseTimeout parses a duration string from a configuration field. An empty
// string yields (0, nil), meaning "unset". field names the configuration key
// in the error, so a bad value read from the config file or from a
// --timeout-* flag reports which one it came from.
func ParseTimeout(field, raw string) (time.Duration, error) {
	if raw == "" {
		return 0, nil
	}

	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("%s %q is not a duration (e.g. 5s, 500ms)", field, raw)
	}
	if d <= 0 {
		return 0, fmt.Errorf("%s must be greater than zero", field)
	}

	return d, nil
}

// ParsedTimeouts carries a context's timeout block with every field parsed.
type ParsedTimeouts struct {
	Connect      time.Duration
	TLSHandshake time.Duration
	Request      time.Duration
}

// ParsedTimeouts parses c's timeout block, and the first bad field errors.
// The method cannot be named Timeout, because Go forbids a field and a
// method on one type sharing a name.
func (c *Context) ParsedTimeouts() (ParsedTimeouts, error) {
	connect, err := ParseTimeout("timeout.connect", c.Timeout.Connect)
	if err != nil {
		return ParsedTimeouts{}, err
	}

	tlsHandshake, err := ParseTimeout("timeout.tls-handshake", c.Timeout.TLSHandshake)
	if err != nil {
		return ParsedTimeouts{}, err
	}

	request, err := ParseTimeout("timeout.request", c.Timeout.Request)
	if err != nil {
		return ParsedTimeouts{}, err
	}

	return ParsedTimeouts{
		Connect:      connect,
		TLSHandshake: tlsHandshake,
		Request:      request,
	}, nil
}

// CloneContext returns a deep copy of c. Every field is copied, including
// the blocks a future version adds, and both pointer fields, Auth.Session
// and Proxy.FromEnv, are deep-copied rather than shared. Callers that need a
// defaults-applied view of a stored context clone it first, so that reading
// a context never writes one.
func CloneContext(c *Context) *Context {
	if c == nil {
		return nil
	}

	clone := *c

	if c.Auth.Session != nil {
		session := *c.Auth.Session
		clone.Auth.Session = &session
	}

	if c.Proxy.FromEnv != nil {
		fromEnv := *c.Proxy.FromEnv
		clone.Proxy.FromEnv = &fromEnv
	}

	return &clone
}
