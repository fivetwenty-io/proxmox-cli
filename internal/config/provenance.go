package config

import (
	"fmt"
	"path/filepath"
	"strings"
)

// LabProvenance reports where the lab named name would be resolved from:
// the literal string "config.yml (inline)" when it comes from cfg.Labs, or
// the path of the included file it would be loaded from (cfg.Include /
// cfg.LabsDir glob expansion). It re-derives the same resolution order
// ResolveLabs itself follows (inline first, then include/labs_dir globs) so
// that "pmx lab config show" can report a resolved lab's source without
// ResolveLabs needing to expose its internal provenance tracking as part of
// its public return signature.
//
// Callers are expected to have already resolved name via ResolveLabs (so
// name is known to exist and no duplicate-name condition remains
// unresolved); this function still returns a "not found" error defensively
// if name matches nothing, since it is usable standalone.
func LabProvenance(cfg *Config, configPath, name string) (string, error) {
	if cfg == nil {
		return "", fmt.Errorf("lab provenance: config is nil")
	}
	if name == "" {
		return "", fmt.Errorf("lab provenance: name is required")
	}

	for key, lab := range cfg.Labs {
		if lab == nil {
			continue
		}

		labName := lab.Name
		if labName == "" {
			labName = key
		}
		if labName == name {
			return inlineLabProvenance, nil
		}
	}

	globs := make([]string, 0, len(cfg.Include)+1)
	globs = append(globs, cfg.Include...)
	if cfg.LabsDir != "" {
		globs = append(globs, filepath.Join(cfg.LabsDir, "*.yaml"))
	}

	baseDir := filepath.Dir(configPath)

	for _, pattern := range globs {
		resolvedPattern := pattern
		if !filepath.IsAbs(resolvedPattern) {
			resolvedPattern = filepath.Join(baseDir, resolvedPattern)
		}

		matches, err := filepath.Glob(resolvedPattern)
		if err != nil {
			return "", fmt.Errorf("lab provenance: expand include glob %q: %w", pattern, err)
		}

		for _, file := range matches {
			lab, err := loadLabFile(file)
			if err != nil {
				return "", err
			}

			labName := lab.Name
			if labName == "" {
				labName = strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
			}
			if labName == name {
				return file, nil
			}
		}
	}

	return "", fmt.Errorf("lab provenance: lab %q not found in config.yml or any included file", name)
}
