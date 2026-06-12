// Command pve is the Proxmox VE CLI binary.
// All logic lives in internal/cli; this file is a thin OS entry point.
package main

import (
	"os"

	"github.com/fivetwenty-io/pve-cli/internal/cli"
	"github.com/fivetwenty-io/pve-cli/internal/cli/access"
	"github.com/fivetwenty-io/pve-cli/internal/cli/api"
	"github.com/fivetwenty-io/pve-cli/internal/cli/cluster"
	"github.com/fivetwenty-io/pve-cli/internal/cli/context"
	"github.com/fivetwenty-io/pve-cli/internal/cli/initcmd"
	"github.com/fivetwenty-io/pve-cli/internal/cli/lxc"
	"github.com/fivetwenty-io/pve-cli/internal/cli/node"
	"github.com/fivetwenty-io/pve-cli/internal/cli/pool"
	"github.com/fivetwenty-io/pve-cli/internal/cli/qemu"
	"github.com/fivetwenty-io/pve-cli/internal/cli/sdn"
	"github.com/fivetwenty-io/pve-cli/internal/cli/storage"
	"github.com/fivetwenty-io/pve-cli/internal/cli/task"
	"github.com/fivetwenty-io/pve-cli/internal/cli/version"
)

// factories is the ordered list of group command factories wired into the root
// command. The order here controls the help-output listing order and must
// match the former init()-registration order (= import order in the old
// blank-import block).
var factories = []cli.GroupFactory{
	access.Group,
	api.Group,
	api.AuthAlias,
	cluster.Group,
	context.Group,
	context.CtxAlias,
	initcmd.Group,
	lxc.Group,
	node.Group,
	pool.Group,
	qemu.Group,
	sdn.Group,
	storage.Group,
	task.Group,
	version.Group,
}

func main() {
	os.Exit(cli.Main(factories))
}
