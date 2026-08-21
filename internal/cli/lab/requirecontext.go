package lab

import (
	"errors"
	"fmt"

	"github.com/spf13/pflag"
)

// Registering the lab-<name> pmx context after a create, a cluster init, or a
// scale is deliberately best-effort. The provisioning work has already
// succeeded by the time it runs, and a context that failed to register costs
// nothing but a later `pmx lab context sync`, so failing the whole command
// over it would misreport what actually happened to the lab.
//
// That is right for an operator watching the STEP table, and wrong for an
// unattended caller: a warning row on stdout is invisible to a script, which
// sees exit 0 and moves on to the next step against a context that does not
// exist. --require-context is the opt-in that makes those callers' exit code
// reflect the context outcome, without changing the default for everyone
// already relying on it.
//
// The flag covers context registration, refresh, and reachability only. It
// deliberately does not promote the other deferred rows these commands emit
// (a node awaiting OS provisioning, a bond needing `hostnet apply`), which
// describe work that was never this run's to finish.
const labRequireContextUsage = "exit non-zero when the lab-<name> pmx context cannot be registered, " +
	"refreshed, or reached (the provisioning work still runs, and is still reported, either way)"

// registerRequireContextFlag registers --require-context against f, bound to
// p, so every command offering it carries identical wording.
func registerRequireContextFlag(f *pflag.FlagSet, p *bool) {
	f.BoolVar(p, "require-context", false, labRequireContextUsage)
}

// requireContextErr builds the error a command returns when --require-context
// was given and at least one lab-context step failed. It returns nil when the
// flag is off or nothing failed, so callers can return it unconditionally
// after rendering.
//
// Callers render their STEP table BEFORE returning this: the rows are the
// record of what did succeed, and suppressing them would leave the operator
// with an exit code and nothing to read.
func requireContextErr(require bool, name string, errs []error) error {
	if !require {
		return nil
	}
	// errors.Join drops nils but still returns non-nil when at least one
	// entry is real, so the emptiness check has to be its own: callers
	// accumulate one slot per step and leave the ones that succeeded nil.
	if errors.Join(errs...) == nil {
		return nil
	}
	return fmt.Errorf(
		"lab %q: --require-context was given and the lab context did not converge; "+
			"run `pmx lab context sync %s` once the lab is reachable: %w",
		name, name, errors.Join(errs...))
}
