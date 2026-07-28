package storage

import (
	"slices"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/optionschema"
)

// newDescribeCmd builds `pmx pve storage describe`, an offline catalog of every
// storage option from the PVE API schema (see options_schema_gen.go). The
// schema is type-polymorphic — which options a storage accepts depends on its
// type — so the catalog carries a TYPES column and a --type filter backed by
// the plugin-derived mapping in type_options_gen.go.
func newDescribeCmd() *cobra.Command {
	return optionschema.NewDescribeCmd(optionschema.DescribeConfig{
		Schemas: storageOptionSchemas,
		Short:   "Describe all storage options and the types that accept them",
		Long: "List every storage option from the PVE API schema, with its type, built-in " +
			"default, allowed values, and the storage types that accept it.\n\n" +
			"The option set is type-polymorphic, so pass --type to see exactly what one " +
			"storage type takes, its required and create-only options included.\n\n" +
			"Pass an option name to narrow the output to that option alone, with full " +
			"descriptions and sub-keys.\n\n" +
			"Runs entirely offline.",
		CommandHint:         "pmx pve storage describe",
		SubKeyRowsInCatalog: false,
		TypeSets:            storageTypeOptions,
	})
}

// defaultsForType returns the option schemas valid for one storage type, so
// `get --defaults` never lists a default for an option the storage cannot
// hold. An unknown type (e.g. an out-of-tree plugin) returns nil: no schema
// knowledge, no defaults claimed. Credential options are excluded: `get`
// strips them from the response, so the merge cannot tell a configured
// secret from an unset one and must not claim either.
func defaultsForType(storageType string) []optionschema.Schema {
	set, ok := storageTypeOptions[storageType]
	if !ok {
		return nil
	}
	kept := make([]optionschema.Schema, 0, len(set))
	for _, s := range storageOptionSchemas {
		if _, ok := set[s.Name]; !ok {
			continue
		}
		if slices.Contains(storageSecretKeys, s.Name) {
			continue
		}
		kept = append(kept, s)
	}
	return kept
}

// commonOptionSchemas returns the options every storage type accepts — the
// only ones whose set-flag enrichment cannot mislead, since the flat API
// schema does not say which types the rest apply to.
func commonOptionSchemas() []optionschema.Schema {
	var common []optionschema.Schema
	for _, s := range storageOptionSchemas {
		accepted := 0
		for _, set := range storageTypeOptions {
			if _, ok := set[s.Name]; ok {
				accepted++
			}
		}
		if accepted == len(storageTypeOptions) {
			common = append(common, s)
		}
	}
	return common
}
