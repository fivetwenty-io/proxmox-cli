// Package exitcode defines semantic exit code constants and maps pve-apiclient-go errors to exit codes.
package exitcode

import (
	"errors"

	pveerrors "github.com/fivetwenty-io/proxmox-apiclient-go/v3/pkg/errors"
	"github.com/fivetwenty-io/pve-cli/internal/exec"
)

// Exit code constants. Values match the A-02 decision in the design document.
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
)

// FromError maps a pve-apiclient-go error value to the appropriate exit code.
//
// Mapping rules (tested in priority order):
//  0. *exec.ExitError (child process exit, e.g. from `pve ssh`/`pve rsync`) →
//     the child's own exit code, verbatim, regardless of any other mapping
//     the error chain might also match
//  1. TFARequiredError or AuthenticationError with TFA=true → TFARequired (7)
//  2. AuthenticationError (TFA=false) or PermissionError → Auth (4)
//  3. ParameterError → BadArgs (2)
//  4. ErrNotFound sentinel or APIError with IsNotFound() → NotFound (5)
//  5. ErrConflict sentinel or APIError with CodeResourceLocked HTTP code → Conflict (6)
//  6. ConnectionError, SSLError, TimeoutError → Infra (3)
//  7. nil → OK (0)
//  8. anything else → Generic (1)
func FromError(err error) int {
	if err == nil {
		return OK
	}

	// 0. Child process exit code takes precedence over every API-error mapping
	// below: once a subprocess (ssh, rsync) has run and exited non-zero, its
	// own exit code IS the semantically correct code to propagate.
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.Code
	}

	// 1. TFA required — check before generic auth so TFA path is preferred.
	if pveerrors.IsTFARequired(err) {
		return TFARequired
	}

	// AuthenticationError with TFA flag set is also TFARequired.
	var authErr *pveerrors.AuthenticationError
	if errors.As(err, &authErr) && authErr.TFA {
		return TFARequired
	}

	// 2. Authentication / permission failures.
	if errors.As(err, &authErr) {
		return Auth
	}
	var permErr *pveerrors.PermissionError
	if errors.As(err, &permErr) {
		return Auth
	}
	// ErrUnauthorized / ErrForbidden sentinels (may be wrapped without typed structs).
	if errors.Is(err, pveerrors.ErrUnauthorized) || errors.Is(err, pveerrors.ErrForbidden) {
		return Auth
	}

	// 3. Parameter / bad-argument errors.
	var paramErr *pveerrors.ParameterError
	if errors.As(err, &paramErr) {
		return BadArgs
	}

	// 4. Not-found errors.
	if errors.Is(err, pveerrors.ErrNotFound) {
		return NotFound
	}
	var apiErr *pveerrors.APIError
	if errors.As(err, &apiErr) && apiErr.IsNotFound() {
		return NotFound
	}

	// 5. Conflict / resource-locked errors.
	if errors.Is(err, pveerrors.ErrConflict) {
		return Conflict
	}
	if errors.As(err, &apiErr) {
		// CodeResourceLocked = 423, CodeResourceInUse/CodeResourceExists = 409 (ErrConflict sentinel).
		if apiErr.HTTPCode == pveerrors.CodeResourceLocked {
			return Conflict
		}
	}

	// 6. Infrastructure errors: connection, SSL, timeout.
	if pveerrors.IsConnectionError(err) || pveerrors.IsSSLError(err) || pveerrors.IsTimeoutError(err) {
		return Infra
	}

	// 8. Fallback.
	return Generic
}
