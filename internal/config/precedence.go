package config

import "os"

// Resolve returns the first non-empty value in priority order:
//
//  1. flagVal — value passed via a CLI flag (highest priority)
//  2. os.Getenv(envKey) — environment variable (envKey must not be empty)
//  3. cfgVal — value read from the config file
//  4. def — compiled-in default (lowest priority)
//
// An empty envKey disables the environment-variable lookup step.
func Resolve(flagVal, envKey, cfgVal, def string) string {
	if flagVal != "" {
		return flagVal
	}
	if envKey != "" {
		if val := os.Getenv(envKey); val != "" {
			return val
		}
	}
	if cfgVal != "" {
		return cfgVal
	}
	return def
}
