package lab

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestRequireContextErr_SilentUnlessAsked pins the default that every
// existing caller depends on: without the flag, a failed context registration
// stays a warning row and the command still exits 0. Making that non-zero for
// everyone would fail runs whose VMs provisioned perfectly well.
func TestRequireContextErr_SilentUnlessAsked(t *testing.T) {
	assert.NoError(t, requireContextErr(false, "wayne", []error{errors.New("sync failed")}))
}

// TestRequireContextErr_NilSlotsAreNotFailures guards the shape callers use:
// they append one slot per context step, leaving the ones that succeeded nil.
// A slice of nils is a clean run, not a failure, and errors.Join's non-nil
// return for a partially-nil slice is exactly what makes this worth pinning.
func TestRequireContextErr_NilSlotsAreNotFailures(t *testing.T) {
	assert.NoError(t, requireContextErr(true, "wayne", nil))
	assert.NoError(t, requireContextErr(true, "wayne", []error{nil, nil}))

	err := requireContextErr(true, "wayne", []error{nil, errors.New("refresh context lab-wayne: boom")})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

// TestRequireContextErr_NamesTheRecoveryCommand keeps the error actionable.
// The whole point of the flag is an unattended caller that cannot read the
// warning row, so the exit-code path has to carry the same instruction the row
// would have shown.
func TestRequireContextErr_NamesTheRecoveryCommand(t *testing.T) {
	err := requireContextErr(true, "wayne", []error{errors.New("unreachable")})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "pmx lab context sync wayne")
	assert.Contains(t, err.Error(), "--require-context")
}
