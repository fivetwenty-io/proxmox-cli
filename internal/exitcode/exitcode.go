// Package exitcode defines semantic exit code constants and maps proxmox-apiclient-go errors to exit codes.
package exitcode

import (
	"errors"

	pveerrors "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/errors"

	"github.com/fivetwenty-io/proxmox-cli/internal/apiclient"
	"github.com/fivetwenty-io/proxmox-cli/internal/exec"
)

// Exit code constants. The values are a stable CLI contract: scripts branch
// on them, so they must never be renumbered.
const (
	// OK indicates successful execution.
	OK = 0
	// Generic indicates an unclassified error.
	Generic = 1
	// BadArgs indicates invalid parameters or argument validation failure.
	BadArgs = 2
	// Infra indicates a connectivity, SSL, or timeout error reaching the PVE API.
	Infra = 3
	// Auth indicates authentication or authorisation failure (wrong credentials, forbidden).
	Auth = 4
	// NotFound indicates the requested resource does not exist.
	NotFound = 5
	// Conflict indicates a resource conflict (already exists, locked, in-use).
	Conflict = 6
	// TFARequired indicates that two-factor authentication is required to proceed.
	TFARequired = 7
	// TaskWarned indicates a task that reached a terminal state with a
	// "WARNINGS: N" exit status while --warnings-as-errors was in effect. It
	// is distinct from Generic so a script can tell "the task ran and warned"
	// from "the command failed": the work was done in the first case.
	TaskWarned = 8
	// AuditFindings indicates an audit that ran to completion and found
	// something an operator has to act on, such as `pmx cpi disk-audit`
	// finding a free-floating persistent disk. It is distinct from Generic so
	// a CI gate can tell "the audit found work" from "the audit could not run".
	AuditFindings = 9
)

// UsageError marks an invalid flag value or argument that pmx rejected itself,
// before it ran the command. It maps to BadArgs, which otherwise only an API
// parameter error reaches.
type UsageError struct {
	Err error
}

// Error returns the wrapped error's text unchanged.
func (e *UsageError) Error() string {
	return e.Err.Error()
}

// Unwrap returns the wrapped error.
func (e *UsageError) Unwrap() error {
	return e.Err
}

// AuditFindingsError reports an audit that completed and found something an
// operator has to act on. Message is the whole line pmx prints on standard
// error, so it reads the same whether a script or a person sees it.
type AuditFindingsError struct {
	Message string
}

// Error returns Message.
func (e *AuditFindingsError) Error() string {
	return e.Message
}

// FromError maps a proxmox-apiclient-go error value to the appropriate exit code.
//
// Mapping rules (tested in priority order):
//  0. *exec.ExitError (child process exit, e.g. from `pmx ssh`/`pmx rsync`) →
//     the child's own exit code, verbatim, regardless of any other mapping
//     the error chain might also match
//  1. *apiclient.TaskWarnedError (a task that finished with "WARNINGS: N"
//     while --warnings-as-errors was in effect) → TaskWarned (8)
//  2. *AuditFindingsError (an audit that found work) → AuditFindings (9)
//  3. *UsageError (a flag value pmx rejected itself) → BadArgs (2)
//  4. TFARequiredError or AuthenticationError with TFA=true → TFARequired (7)
//  5. AuthenticationError (TFA=false) or PermissionError → Auth (4)
//  6. ParameterError → BadArgs (2)
//  7. ErrNotFound sentinel or APIError with IsNotFound() → NotFound (5)
//  8. ErrConflict sentinel or APIError with CodeResourceLocked HTTP code → Conflict (6)
//  9. ConnectionError, SSLError, TimeoutError → Infra (3)
//  10. nil → OK (0)
//  11. anything else → Generic (1)
func FromError(err error) int {
	if err == nil {
		return OK
	}

	// 0. Child process exit code takes precedence over every API-error mapping
	// below: once a subprocess (ssh, rsync) has run and exited non-zero, its
	// own exit code IS the semantically correct code to propagate.
	if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
		return exitErr.Code
	}

	// 1. A warned task is a terminal outcome rather than a class of API
	// failure, so it gets its own code instead of falling through to Generic:
	// the operation ran, and a script that treats "warned" like "failed to
	// run" would retry work that already happened.
	if _, ok := errors.AsType[*apiclient.TaskWarnedError](err); ok {
		return TaskWarned
	}

	// 2. An audit's findings are a completed run's verdict, not a failure to
	// run, so they get their own code for the same reason a warned task does.
	if _, ok := errors.AsType[*AuditFindingsError](err); ok {
		return AuditFindings
	}

	// 3. A usage error is pmx refusing its own input, which is what BadArgs
	// means; it must not fall through to Generic.
	if _, ok := errors.AsType[*UsageError](err); ok {
		return BadArgs
	}

	// 4. TFA required — check before generic auth so TFA path is preferred.
	if pveerrors.IsTFARequired(err) {
		return TFARequired
	}

	// AuthenticationError with TFA flag set is also TFARequired.
	var authErr *pveerrors.AuthenticationError
	if errors.As(err, &authErr) && authErr.TFA {
		return TFARequired
	}

	// 5. Authentication / permission failures.
	if errors.As(err, &authErr) {
		return Auth
	}
	if _, ok := errors.AsType[*pveerrors.PermissionError](err); ok {
		return Auth
	}
	// ErrUnauthorized / ErrForbidden sentinels (may be wrapped without typed structs).
	if errors.Is(err, pveerrors.ErrUnauthorized) || errors.Is(err, pveerrors.ErrForbidden) {
		return Auth
	}

	// 6. Parameter / bad-argument errors.
	if _, ok := errors.AsType[*pveerrors.ParameterError](err); ok {
		return BadArgs
	}

	// 7. Not-found errors.
	if errors.Is(err, pveerrors.ErrNotFound) {
		return NotFound
	}
	var apiErr *pveerrors.APIError
	if errors.As(err, &apiErr) && apiErr.IsNotFound() {
		return NotFound
	}

	// 8. Conflict / resource-locked errors.
	if errors.Is(err, pveerrors.ErrConflict) {
		return Conflict
	}
	if errors.As(err, &apiErr) {
		// CodeResourceLocked = 423, CodeResourceInUse/CodeResourceExists = 409 (ErrConflict sentinel).
		if apiErr.HTTPCode == pveerrors.CodeResourceLocked {
			return Conflict
		}
	}

	// 9. Infrastructure errors: connection, SSL, timeout.
	if pveerrors.IsConnectionError(err) || pveerrors.IsSSLError(err) || pveerrors.IsTimeoutError(err) {
		return Infra
	}

	// 11. Fallback.
	return Generic
}
