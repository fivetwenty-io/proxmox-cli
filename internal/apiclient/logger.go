package apiclient

import (
	"log/slog"

	pve "github.com/fivetwenty-io/pve-apiclient-go/v3/pkg/client"
)

// slogAdapter bridges the application's *slog.Logger onto the pve.Logger
// interface expected by the underlying HTTP client. Each variadic field map is
// flattened into slog key/value attributes so HTTP request/response activity is
// captured in the JSONL log alongside the rest of the invocation.
type slogAdapter struct {
	log *slog.Logger
}

// newSlogAdapter wraps l in a pve.Logger. It returns nil when l is nil so
// callers can skip installation cleanly.
func newSlogAdapter(l *slog.Logger) pve.Logger {
	if l == nil {
		return nil
	}
	return &slogAdapter{log: l}
}

func fieldsToAttrs(fields map[string]any) []any {
	if len(fields) == 0 {
		return nil
	}
	attrs := make([]any, 0, len(fields)*2)
	for k, v := range fields {
		attrs = append(attrs, k, v)
	}
	return attrs
}

func (a *slogAdapter) Debug(msg string, fields map[string]any) {
	a.log.Debug(msg, fieldsToAttrs(fields)...)
}

func (a *slogAdapter) Info(msg string, fields map[string]any) {
	a.log.Info(msg, fieldsToAttrs(fields)...)
}

func (a *slogAdapter) Warn(msg string, fields map[string]any) {
	a.log.Warn(msg, fieldsToAttrs(fields)...)
}

func (a *slogAdapter) Error(msg string, fields map[string]any) {
	a.log.Error(msg, fieldsToAttrs(fields)...)
}

// SetSlogLogger installs an *slog.Logger as the API client's HTTP logger and
// opts into the library's redaction controls: secret-bearing headers and form
// parameters are redacted and request/response bodies are not logged. It is a
// no-op when l is nil. The returned bool reports whether a logger was installed.
func (ac *APIClient) SetSlogLogger(l *slog.Logger) bool {
	adapter := newSlogAdapter(l)
	if adapter == nil {
		return false
	}
	ac.Raw.SetLogger(adapter)
	ac.Raw.SetLogConfig(pve.LogConfig{
		Enabled:           true,
		RedactHeaders:     []string{"authorization", "cookie", "csrfpreventiontoken"},
		RedactParams:      []string{"password", "token", "secret"},
		LogRequestHeader:  false,
		LogResponseHeader: false,
		LogBody:           false,
		LogResponseBody:   false,
	})
	return true
}
