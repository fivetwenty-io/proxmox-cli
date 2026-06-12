package task_test

import (
	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/cli/task"
)

// addTaskGroup wires the task command group into root for tests that need the
// full task sub-tree. It replaces the former global-registry approach.
func addTaskGroup(root *cobra.Command) {
	cli.AddGroups(root, &cli.Deps{}, []cli.GroupFactory{task.Group})
}
