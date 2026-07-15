// Command pmx is the Proxmox CLI binary.
// All logic lives in internal/cli; this file is a thin OS entry point.
package main

import (
	"os"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/persona"
)

func main() {
	name := cli.Persona(os.Args[0])
	os.Exit(cli.Main(name, persona.Factories(name)))
}
