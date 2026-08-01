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
)

// FromError maps a proxmox-apiclient-go error value to the appropriate exit code.
//
// Mapping rules (tested in priority order):
//  0. *exec.ExitError (child process exit, e.g. from `pmx ssh`/`pmx rsync`) →
//     the child's own exit code, verbatim, regardless of any other mapping
//     the error chain might also match
//  1. *apiclient.TaskWarnedError (a task that finished with "WARNINGS: N"
//     while --warnings-as-errors was in effect) → TaskWarned (8)
//  2. TFARequiredError or AuthenticationError with TFA=true → TFARequired (7)
//  3. AuthenticationError (TFA=false) or PermissionError → Auth (4)
//  4. ParameterError → BadArgs (2)
//  5. ErrNotFound sentinel or APIError with IsNotFound() → NotFound (5)
//  6. ErrConflict sentinel or APIError with CodeResourceLocked HTTP code → Conflict (6)
//  7. ConnectionError, SSLError, TimeoutError → Infra (3)
//  8. nil → OK (0)
//  9. anything else → Generic (1)
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

	// 2. TFA required — check before generic auth so TFA path is preferred.
	if pveerrors.IsTFARequired(err) {
		return TFARequired
	}

	// AuthenticationError with TFA flag set is also TFARequired.
	var authErr *pveerrors.AuthenticationError
	if errors.As(err, &authErr) && authErr.TFA {
		return TFARequired
	}

	// 3. Authentication / permission failures.
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

	// 4. Parameter / bad-argument errors.
	if _, ok := errors.AsType[*pveerrors.ParameterError](err); ok {
		return BadArgs
	}

	// 5. Not-found errors.
	if errors.Is(err, pveerrors.ErrNotFound) {
		return NotFound
	}
	var apiErr *pveerrors.APIError
	if errors.As(err, &apiErr) && apiErr.IsNotFound() {
		return NotFound
	}

	// 6. Conflict / resource-locked errors.
	if errors.Is(err, pveerrors.ErrConflict) {
		return Conflict
	}
	if errors.As(err, &apiErr) {
		// CodeResourceLocked = 423, CodeResourceInUse/CodeResourceExists = 409 (ErrConflict sentinel).
		if apiErr.HTTPCode == pveerrors.CodeResourceLocked {
			return Conflict
		}
	}

	// 7. Infrastructure errors: connection, SSL, timeout.
	if pveerrors.IsConnectionError(err) || pveerrors.IsSSLError(err) || pveerrors.IsTimeoutError(err) {
		return Infra
	}

	// 9. Fallback.
	return Generic
}
