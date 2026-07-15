package cli_test

import (
	"context"
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"

	"github.com/fivetwenty-io/pmx-cli/internal/cli"
	"github.com/fivetwenty-io/pmx-cli/internal/persona"
)

func aliasCmd(name string, aliases ...string) *cobra.Command {
	return &cobra.Command{Use: name, Aliases: aliases, Run: func(*cobra.Command, []string) {}}
}

func aliasGroup(name string, children ...*cobra.Command) *cobra.Command {
	g := &cobra.Command{Use: name}
	g.AddCommand(children...)
	return g
}

func findChild(parent *cobra.Command, name string) *cobra.Command {
	for _, c := range parent.Commands() {
		if c.Name() == name {
			return c
		}
	}
	return nil
}

func TestNormalizeAliases_ListGainsLs(t *testing.T) {
	root := aliasGroup("root", aliasGroup("thing", aliasCmd("list")))
	cli.NormalizeAliases(root)
	require.Equal(t, []string{"ls"}, findChild(findChild(root, "thing"), "list").Aliases)
}

func TestNormalizeAliases_LsGainsList(t *testing.T) {
	root := aliasGroup("root", aliasGroup("thing", aliasCmd("ls")))
	cli.NormalizeAliases(root)
	require.Equal(t, []string{"list"}, findChild(findChild(root, "thing"), "ls").Aliases)
}

func TestNormalizeAliases_ExistingAliasNotDuplicated(t *testing.T) {
	root := aliasGroup("root", aliasGroup("thing", aliasCmd("ls", "list")))
	cli.NormalizeAliases(root)
	require.Equal(t, []string{"list"}, findChild(findChild(root, "thing"), "ls").Aliases)
}

func TestNormalizeAliases_SiblingNameConflictSkipped(t *testing.T) {
	// A parent with both a `list` and an `ls` child must gain nothing: the
	// alias would shadow a real sibling command.
	root := aliasGroup("root", aliasGroup("thing", aliasCmd("list"), aliasCmd("ls")))
	cli.NormalizeAliases(root)
	require.Empty(t, findChild(findChild(root, "thing"), "list").Aliases)
	require.Empty(t, findChild(findChild(root, "thing"), "ls").Aliases)
}

func TestNormalizeAliases_SiblingAliasConflictSkipped(t *testing.T) {
	// A sibling that already claims `ls` as an alias blocks `list` from
	// gaining it (e.g. `ha status` aliases list+ls next to other commands).
	root := aliasGroup("root", aliasGroup("thing", aliasCmd("list"), aliasCmd("status", "ls")))
	cli.NormalizeAliases(root)
	require.Empty(t, findChild(findChild(root, "thing"), "list").Aliases)
}

func TestNormalizeAliases_DeleteGainsRm(t *testing.T) {
	root := aliasGroup("root", aliasGroup("thing", aliasCmd("delete")))
	cli.NormalizeAliases(root)
	require.Equal(t, []string{"rm"}, findChild(findChild(root, "thing"), "delete").Aliases)
}

func TestNormalizeAliases_RemoveGainsRm(t *testing.T) {
	root := aliasGroup("root", aliasGroup("thing", aliasCmd("remove")))
	cli.NormalizeAliases(root)
	require.Equal(t, []string{"rm"}, findChild(findChild(root, "thing"), "remove").Aliases)
}

func TestNormalizeAliases_DeleteAndRemoveSiblings_NeitherGainsRm(t *testing.T) {
	// `firewall ipset` groups have delete (drop the set) and remove (drop a
	// member) side by side; `rm` would be ambiguous, so neither gains it.
	root := aliasGroup("root", aliasGroup("thing", aliasCmd("delete"), aliasCmd("remove")))
	cli.NormalizeAliases(root)
	require.Empty(t, findChild(findChild(root, "thing"), "delete").Aliases)
	require.Empty(t, findChild(findChild(root, "thing"), "remove").Aliases)
}

func TestNormalizeAliases_GetAndShowCrossAlias(t *testing.T) {
	root := aliasGroup("root", aliasGroup("a", aliasCmd("get")), aliasGroup("b", aliasCmd("show")))
	cli.NormalizeAliases(root)
	require.Equal(t, []string{"show"}, findChild(findChild(root, "a"), "get").Aliases)
	require.Equal(t, []string{"get"}, findChild(findChild(root, "b"), "show").Aliases)
}

func TestNormalizeAliases_CreateAndAddCrossAlias(t *testing.T) {
	root := aliasGroup("root", aliasGroup("a", aliasCmd("create")), aliasGroup("b", aliasCmd("add")))
	cli.NormalizeAliases(root)
	require.Equal(t, []string{"add"}, findChild(findChild(root, "a"), "create").Aliases)
	require.Equal(t, []string{"create"}, findChild(findChild(root, "b"), "add").Aliases)
}

func TestNormalizeAliases_SetAndUpdateCrossAlias(t *testing.T) {
	root := aliasGroup("root", aliasGroup("a", aliasCmd("set")), aliasGroup("b", aliasCmd("update")))
	cli.NormalizeAliases(root)
	require.Equal(t, []string{"update"}, findChild(findChild(root, "a"), "set").Aliases)
	require.Equal(t, []string{"set"}, findChild(findChild(root, "b"), "update").Aliases)
}

func TestNormalizeAliases_GetAndShowSiblings_NeitherCrossAliased(t *testing.T) {
	// A group with both get and show siblings must gain nothing: the alias
	// would shadow a real sibling command.
	root := aliasGroup("root", aliasGroup("thing", aliasCmd("get"), aliasCmd("show")))
	cli.NormalizeAliases(root)
	require.Empty(t, findChild(findChild(root, "thing"), "get").Aliases)
	require.Empty(t, findChild(findChild(root, "thing"), "show").Aliases)
}

func TestNormalizeAliases_AnnotationOptOutSkipsCommand(t *testing.T) {
	// Commands whose verb is not a CRUD synonym (apt update = refresh the
	// package index, api get = raw HTTP GET) opt out via annotation.
	optOut := aliasCmd("update")
	optOut.Annotations = map[string]string{cli.AnnotationNoVerbAlias: "true"}
	root := aliasGroup("root", aliasGroup("apt", optOut))
	cli.NormalizeAliases(root)
	require.Empty(t, findChild(findChild(root, "apt"), "update").Aliases)
}

func TestNormalizeAliases_CopyGainsCp(t *testing.T) {
	root := aliasGroup("root", aliasGroup("thing", aliasCmd("copy")))
	cli.NormalizeAliases(root)
	require.Equal(t, []string{"cp"}, findChild(findChild(root, "thing"), "copy").Aliases)
}

func TestNormalizeAliases_RenameGainsMv(t *testing.T) {
	root := aliasGroup("root", aliasGroup("thing", aliasCmd("rename")))
	cli.NormalizeAliases(root)
	require.Equal(t, []string{"mv"}, findChild(findChild(root, "thing"), "rename").Aliases)
}

func TestNormalizeAliases_Recurses(t *testing.T) {
	root := aliasGroup("root", aliasGroup("a", aliasGroup("b", aliasCmd("list"))))
	cli.NormalizeAliases(root)
	require.Equal(t, []string{"ls"}, findChild(findChild(findChild(root, "a"), "b"), "list").Aliases)
}

// TestNormalizeAliases_PersonaTrees proves the real command trees resolve the
// conventional short forms after AddGroups wiring.
func TestNormalizeAliases_PersonaTrees(t *testing.T) {
	build := func(name string) *cobra.Command {
		root, cleanup := cli.NewRootCmd(name)
		t.Cleanup(cleanup)
		root.SetContext(context.Background())
		cli.AddGroups(root, &cli.Deps{}, persona.Factories(name))
		return root
	}

	t.Run("pmx lab ls resolves to list", func(t *testing.T) {
		root := build("pmx")
		cmd, _, err := root.Find([]string{"lab", "ls"})
		require.NoError(t, err)
		require.Equal(t, "list", cmd.Name())
	})

	t.Run("pbs datastore list resolves to ls", func(t *testing.T) {
		root := build("pbs")
		cmd, _, err := root.Find([]string{"datastore", "list"})
		require.NoError(t, err)
		require.Equal(t, "ls", cmd.Name())
	})

	t.Run("pve node disks ls resolves to physical disk list", func(t *testing.T) {
		root := build("pve")
		cmd, _, err := root.Find([]string{"node", "disks", "ls"})
		require.NoError(t, err)
		require.Equal(t, "list", cmd.Name())

		pools, _, err := root.Find([]string{"node", "disks", "pools"})
		require.NoError(t, err)
		require.Equal(t, "pools", pools.Name())
		require.Empty(t, pools.Aliases)
	})

	t.Run("pve ipset delete and remove stay rm-free", func(t *testing.T) {
		root := build("pve")
		del, _, err := root.Find([]string{"cluster", "firewall", "ipset", "delete"})
		require.NoError(t, err)
		require.NotContains(t, del.Aliases, "rm")
		rem, _, err := root.Find([]string{"cluster", "firewall", "ipset", "remove"})
		require.NoError(t, err)
		require.NotContains(t, rem.Aliases, "rm")
	})

	t.Run("pve qemu delete gains rm", func(t *testing.T) {
		root := build("pve")
		cmd, _, err := root.Find([]string{"qemu", "rm"})
		require.NoError(t, err)
		require.Equal(t, "delete", cmd.Name())
	})

	t.Run("pve access user show resolves to get", func(t *testing.T) {
		root := build("pve")
		cmd, _, err := root.Find([]string{"access", "user", "show"})
		require.NoError(t, err)
		require.Equal(t, "get", cmd.Name())
	})

	t.Run("pbs datastore get resolves to show", func(t *testing.T) {
		root := build("pbs")
		cmd, _, err := root.Find([]string{"datastore", "get"})
		require.NoError(t, err)
		require.Equal(t, "show", cmd.Name())
	})

	t.Run("pbs user create resolves to add", func(t *testing.T) {
		root := build("pbs")
		cmd, _, err := root.Find([]string{"user", "create"})
		require.NoError(t, err)
		require.Equal(t, "add", cmd.Name())
	})

	t.Run("pbs node config set resolves to update", func(t *testing.T) {
		root := build("pbs")
		cmd, _, err := root.Find([]string{"node", "config", "set"})
		require.NoError(t, err)
		require.Equal(t, "update", cmd.Name())
	})

	t.Run("pve node dns set gains update", func(t *testing.T) {
		root := build("pve")
		cmd, _, err := root.Find([]string{"node", "dns", "update"})
		require.NoError(t, err)
		require.Equal(t, "set", cmd.Name())
	})

	t.Run("apt update keeps refresh semantics, no set alias", func(t *testing.T) {
		for _, name := range []string{"pve", "pbs"} {
			root := build(name)
			cmd, _, err := root.Find([]string{"node", "apt", "update"})
			require.NoError(t, err)
			require.NotContains(t, cmd.Aliases, "set", "persona %s", name)
		}
	})

	t.Run("api get keeps raw-HTTP semantics, no show alias", func(t *testing.T) {
		root := build("pmx")
		cmd, _, err := root.Find([]string{"api", "get"})
		require.NoError(t, err)
		require.NotContains(t, cmd.Aliases, "show")
	})

	t.Run("pve pool show resolves to get", func(t *testing.T) {
		root := build("pve")
		cmd, _, err := root.Find([]string{"pool", "show"})
		require.NoError(t, err)
		require.Equal(t, "get", cmd.Name())
	})

	t.Run("ipset add and create stay cross-alias-free", func(t *testing.T) {
		root := build("pve")
		add, _, err := root.Find([]string{"cluster", "firewall", "ipset", "add"})
		require.NoError(t, err)
		require.NotContains(t, add.Aliases, "create")
		create, _, err := root.Find([]string{"cluster", "firewall", "ipset", "create"})
		require.NoError(t, err)
		require.NotContains(t, create.Aliases, "add")
	})
}
