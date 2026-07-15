package cli

import "github.com/spf13/cobra"

// AnnotationNoVerbAlias marks a command whose verb is not a CRUD synonym
// (for example `apt update` refreshes the package index and `api get` issues
// a raw HTTP GET), so NormalizeAliases must not grant it a synonym alias.
const AnnotationNoVerbAlias = "pmx-no-verb-alias"

// verbAliases maps verb names to the conventional alias granted by
// NormalizeAliases: short forms (ls, rm, cp, mv) plus the cross-dialect
// synonyms that reconcile the subtrees' upstream vocabularies — PVE speaks
// pvesh (get/create/set) while PBS/PDM speak proxmox-backup-manager
// (show/add/update). Pairs are bidirectional so both spellings resolve no
// matter which one a subtree chose as its primary name.
var verbAliases = map[string]string{
	"list":   "ls",
	"ls":     "list",
	"delete": "rm",
	"remove": "rm",
	"copy":   "cp",
	"rename": "mv",
	"get":    "show",
	"show":   "get",
	"create": "add",
	"add":    "create",
	"set":    "update",
	"update": "set",
}

// NormalizeAliases walks the command tree and grants each command whose name
// has a conventional alias (see verbAliases) the matching alias, so every
// persona resolves both spellings uniformly.
//
// An alias is skipped when a sibling already claims it — by name or by
// alias — or when two siblings map to the same alias (for example the
// firewall ipset groups, where delete drops the set and remove drops a member,
// so "rm" would be ambiguous). Commands annotated with AnnotationNoVerbAlias
// are skipped entirely: their verb is not a CRUD synonym, so the alias would
// misdescribe the command.
func NormalizeAliases(cmd *cobra.Command) {
	children := cmd.Commands()

	claimed := make(map[string]bool)
	for _, c := range children {
		claimed[c.Name()] = true
		for _, a := range c.Aliases {
			claimed[a] = true
		}
	}

	want := make(map[string]int)
	for _, c := range children {
		if alias, ok := verbAliases[c.Name()]; ok && c.Annotations[AnnotationNoVerbAlias] == "" {
			want[alias]++
		}
	}

	for _, c := range children {
		if alias, ok := verbAliases[c.Name()]; ok &&
			c.Annotations[AnnotationNoVerbAlias] == "" && !claimed[alias] && want[alias] == 1 {
			c.Aliases = append(c.Aliases, alias)
		}
		NormalizeAliases(c)
	}
}
