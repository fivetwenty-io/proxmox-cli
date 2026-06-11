// Command pve is the Proxmox VE CLI binary.
// All logic lives in internal/cli; this file is a thin OS entry point.
package main

import (
	"os"

	"github.com/fivetwenty-io/pve-cli/internal/cli"

	// Blank imports trigger each group package's init(), which calls
	// cli.RegisterGroup to wire the group command into the root. internal/cli
	// itself imports no group package, so there is no import cycle.
	_ "github.com/fivetwenty-io/pve-cli/internal/cli/access"
	_ "github.com/fivetwenty-io/pve-cli/internal/cli/api"
	_ "github.com/fivetwenty-io/pve-cli/internal/cli/cluster"
	_ "github.com/fivetwenty-io/pve-cli/internal/cli/context"
	_ "github.com/fivetwenty-io/pve-cli/internal/cli/initcmd"
	_ "github.com/fivetwenty-io/pve-cli/internal/cli/lxc"
	_ "github.com/fivetwenty-io/pve-cli/internal/cli/node"
	_ "github.com/fivetwenty-io/pve-cli/internal/cli/pool"
	_ "github.com/fivetwenty-io/pve-cli/internal/cli/qemu"
	_ "github.com/fivetwenty-io/pve-cli/internal/cli/sdn"
	_ "github.com/fivetwenty-io/pve-cli/internal/cli/storage"
	_ "github.com/fivetwenty-io/pve-cli/internal/cli/task"
	_ "github.com/fivetwenty-io/pve-cli/internal/cli/version"
)

func main() {
	os.Exit(cli.Main())
}
