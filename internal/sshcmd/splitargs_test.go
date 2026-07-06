package sshcmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSplitPassthrough(t *testing.T) {
	cases := []struct {
		name        string
		args        []string
		wantOpts    []string
		wantCommand []string
	}{
		{
			name:        "detached value",
			args:        []string{"-L", "8080:x:80", "uptime"},
			wantOpts:    []string{"-L", "8080:x:80"},
			wantCommand: []string{"uptime"},
		},
		{
			name:        "attached value",
			args:        []string{"-L8080:x:80", "uptime"},
			wantOpts:    []string{"-L8080:x:80"},
			wantCommand: []string{"uptime"},
		},
		{
			name:        "cluster with no value-taking char stands alone",
			args:        []string{"-4A", "uptime"},
			wantOpts:    []string{"-4A"},
			wantCommand: []string{"uptime"},
		},
		{
			name:        "cluster attached value after non-value char",
			args:        []string{"-vL8080:x:80", "uptime"},
			wantOpts:    []string{"-vL8080:x:80"},
			wantCommand: []string{"uptime"},
		},
		{
			name:        "cluster value-taking char last consumes next token",
			args:        []string{"-vL", "8080:x:80", "uptime"},
			wantOpts:    []string{"-vL", "8080:x:80"},
			wantCommand: []string{"uptime"},
		},
		{
			name:        "detached -o Key=val",
			args:        []string{"-o", "Key=val", "uptime"},
			wantOpts:    []string{"-o", "Key=val"},
			wantCommand: []string{"uptime"},
		},
		{
			name:        "attached -oKey=val",
			args:        []string{"-oKey=val", "uptime"},
			wantOpts:    []string{"-oKey=val"},
			wantCommand: []string{"uptime"},
		},
		{
			name:        "-- before options: everything is command",
			args:        []string{"--", "-p", "22", "uptime"},
			wantOpts:    nil,
			wantCommand: []string{"-p", "22", "uptime"},
		},
		{
			name:        "-- mid: ends option scanning, drops the marker",
			args:        []string{"-p", "2222", "--", "uptime", "-v"},
			wantOpts:    []string{"-p", "2222"},
			wantCommand: []string{"uptime", "-v"},
		},
		{
			name:        "-- after command start is part of the command verbatim",
			args:        []string{"uptime", "--", "-v"},
			wantOpts:    nil,
			wantCommand: []string{"uptime", "--", "-v"},
		},
		{
			name:        "unknown --long option passes through",
			args:        []string{"--foo", "uptime"},
			wantOpts:    []string{"--foo"},
			wantCommand: []string{"uptime"},
		},
		{
			name:        "command-first: no dash-prefixed opts, all command",
			args:        []string{"uptime", "-v"},
			wantOpts:    nil,
			wantCommand: []string{"uptime", "-v"},
		},
		{
			name:        "bare command only",
			args:        []string{"ls"},
			wantOpts:    nil,
			wantCommand: []string{"ls"},
		},
		{
			name:        "empty input",
			args:        nil,
			wantOpts:    nil,
			wantCommand: nil,
		},
		{
			name:        "option-value token that itself starts with dash",
			args:        []string{"-o", "-weird=val", "uptime"},
			wantOpts:    []string{"-o", "-weird=val"},
			wantCommand: []string{"uptime"},
		},
		{
			name:        "-N alone, no command follows",
			args:        []string{"-N"},
			wantOpts:    []string{"-N"},
			wantCommand: []string{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			opts, command := SplitPassthrough(tc.args)
			require.Equal(t, tc.wantOpts, opts)
			require.Equal(t, tc.wantCommand, command)
		})
	}
}
