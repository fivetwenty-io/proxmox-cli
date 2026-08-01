package config

import (
	"os"
	"strconv"
)

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

// ResolveBool is Resolve for a boolean setting, in the same priority order:
//
//  1. flagSet/flagVal — the flag was passed, whatever its value (highest)
//  2. os.Getenv(envKey), parsed by strconv.ParseBool
//  3. cfgVal — value read from the config file
//
// A bool flag needs flagSet separately from flagVal because --flag=false and
// an absent flag are both false: without it, explicitly disabling a setting
// the config file enables would be indistinguishable from saying nothing.
//
// An unparseable environment value is ignored rather than treated as true, so
// PMX_X=maybe falls through to the config file instead of silently enabling
// the setting.
func ResolveBool(flagSet, flagVal bool, envKey string, cfgVal bool) bool {
	if flagSet {
		return flagVal
	}
	if envKey != "" {
		if raw := os.Getenv(envKey); raw != "" {
			if v, err := strconv.ParseBool(raw); err == nil {
				return v
			}
		}
	}
	return cfgVal
}
