package config

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	yaml "github.com/goccy/go-yaml"
)

// permOwnerOnly is the file permission for config files (owner read/write only).
const permOwnerOnly fs.FileMode = 0o600

// permDirOwnerOnly is the directory permission for config directories (owner rwx only).
const permDirOwnerOnly fs.FileMode = 0o700

// groupWorldReadMask masks the group-read, group-write, other-read, other-write bits.
const groupWorldReadMask fs.FileMode = 0o066

// groupWorldDirMask masks all group and other permission bits (rwx) so a config
// directory can be checked for any group/world accessibility.
const groupWorldDirMask fs.FileMode = 0o077

// Save writes cfg to path atomically (write tmp → fsync → rename).
// The parent directory is created with 0700 if it does not exist.
// Returns an error if the existing file is group- or world-readable.
// Use SaveForce to overwrite even when those bits are set.
func Save(path string, cfg *Config) error {
	return save(path, cfg, false)
}

// SaveForce writes cfg to path atomically regardless of existing file permissions.
// It is identical to Save except it bypasses the group/world-readable check.
func SaveForce(path string, cfg *Config) error {
	return save(path, cfg, true)
}

// save is the shared implementation for Save and SaveForce.
func save(path string, cfg *Config, force bool) error {
	if cfg == nil {
		return fmt.Errorf("save config: cfg is nil")
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	return WriteRaw(path, data, force)
}

// WriteRaw writes data to path atomically (write tmp → fsync → rename) with the
// same directory and file permissions as Save: the parent directory is created
// 0700 (and tightened if it predates us), and the file is 0600. When force is
// false it refuses to overwrite an existing group- or world-readable file.
// It is used to emit hand-authored config templates (`pmx init config`) that
// carry comments a struct marshal cannot preserve.
func WriteRaw(path string, data []byte, force bool) error {
	dir := filepath.Dir(path)

	// Ensure the directory exists with restricted permissions.
	if err := os.MkdirAll(dir, permDirOwnerOnly); err != nil {
		return fmt.Errorf("create config dir %s: %w", dir, err)
	}

	// MkdirAll does not tighten an already-existing directory, so a leaf config
	// directory that predates us (e.g. created 0755) would leave the secret-
	// bearing file group/world-traversable. Tighten only the leaf directory to
	// 0700; never touch shared parents such as ~/.config.
	if info, err := os.Stat(dir); err == nil && info.IsDir() {
		if info.Mode().Perm()&groupWorldDirMask != 0 {
			if err := os.Chmod(dir, permDirOwnerOnly); err != nil {
				return fmt.Errorf("restrict config dir %s permissions: %w", dir, err)
			}
		}
	}

	// Check existing file permissions unless force is set.
	if !force {
		if info, err := os.Stat(path); err == nil {
			if info.Mode().Perm()&groupWorldReadMask != 0 {
				return fmt.Errorf(
					"config file %s has group or world read/write bits set (%04o); "+
						"fix permissions or use SaveForce",
					path, info.Mode().Perm(),
				)
			}
		}
	}

	// Write to a temporary file in the same directory so rename is atomic.
	tmp, err := os.CreateTemp(dir, ".pmx-config-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp config file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()

	// Ensure tmp is cleaned up on any failure path.
	success := false
	defer func() {
		if !success {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	// Restrict permissions immediately after creation.
	if err := tmp.Chmod(permOwnerOnly); err != nil {
		return fmt.Errorf("chmod temp config file: %w", err)
	}

	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write temp config file: %w", err)
	}

	// fsync to ensure durability before rename.
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("fsync temp config file: %w", err)
	}

	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp config file: %w", err)
	}

	// Atomic rename.
	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("rename temp config file to %s: %w", path, err)
	}

	success = true
	return nil
}
