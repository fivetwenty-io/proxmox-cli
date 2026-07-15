package pve_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/cli/pve"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
)

func TestGroup_HasProductAnnotationAndChildren(t *testing.T) {
	cmd := pve.Group(&cli.Deps{})
	require.Equal(t, "pve", cmd.Name())
	require.Equal(t, config.ProductPVE, cmd.Annotations[cli.ProductAnnotation])

	names := map[string]bool{}
	for _, c := range cmd.Commands() {
		names[c.Name()] = true
	}
	for _, want := range []string{"node", "cluster", "qemu", "lxc", "storage", "sdn", "pool", "access", "task"} {
		require.True(t, names[want], "missing %s", want)
	}
}

func TestChildFactories_MatchesGroupChildren(t *testing.T) {
	deps := &cli.Deps{}
	var groupNames, factoryNames []string
	for _, c := range pve.Group(deps).Commands() {
		groupNames = append(groupNames, c.Name())
	}
	for _, f := range pve.ChildFactories() {
		factoryNames = append(factoryNames, f(deps).Name())
	}
	require.ElementsMatch(t, groupNames, factoryNames)
}
