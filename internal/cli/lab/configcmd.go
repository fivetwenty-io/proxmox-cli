package lab

import (
	"fmt"
	"net"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/fivetwenty-io/proxmox-cli/internal/cli"
	"github.com/fivetwenty-io/proxmox-cli/internal/config"
	"github.com/fivetwenty-io/proxmox-cli/internal/output"
	"github.com/fivetwenty-io/proxmox-cli/internal/peppi"
)

// configDefaultLabsDirName is the labs_dir basename used when neither
// --labs-dir nor config.yml's labs_dir key supplies one, resolved relative
// to the config file's own directory.
const configDefaultLabsDirName = "labs.d"

// Schema defaults applied by `pmx lab config add` before flag overrides.
// These mirror the standard per-member lab shape, not the "pmx" or
// "pipelines" outliers.
const (
	configDefaultMode        = "nested"
	configDefaultVCPU        = 16
	configDefaultMemoryMinGB = 32
	configDefaultMemoryMaxGB = 96
	configDefaultOSDiskGB    = 64
	configDefaultDataDiskGB  = 400
	configDefaultRefquotaGB  = 480
	configDefaultAccessRole  = "PVEVMUser"
)

// configVnetIDPattern enforces the vnet ID constraint documented in the
// design schema: at most 8 alphanumeric characters, no hyphen.
var configVnetIDPattern = regexp.MustCompile(`^[A-Za-z0-9]{1,8}$`)

// newConfigCmd builds the `pmx lab config` parent command and its
// init/add/show sub-commands. Every sub-command is local-only: they read
// and write config.yml and labs_dir files but never build or call a
// Proxmox API client (see each sub-command's noClient annotation).
func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "config",
		Short: "Scaffold, write, and inspect local lab definition files",
		Long: "Manage the lab definitions pmx lab resolves against: scaffold a labs_dir " +
			"with a commented example (init), write a new lab definition from flags " +
			"(add), or inspect a resolved lab and where it came from (show).\n\n" +
			"Every sub-command here operates on the local filesystem and config.yml " +
			"only; none of them build or call a Proxmox API client.",
	}
	cmd.AddCommand(newConfigInitCmd(), newConfigAddCmd(), newConfigShowCmd())
	return cmd
}

// configResolveLabsDir resolves the effective labs_dir for the init/add
// sub-commands: an explicit --labs-dir flag wins, then the config file's own
// labs_dir key, then configDefaultLabsDirName. A relative result is resolved
// against the directory containing the active config file, matching
// config.ResolveLabs' own glob-resolution base.
func configResolveLabsDir(deps *cli.Deps, flagValue string) string {
	dir := flagValue
	if dir == "" {
		dir = deps.Cfg.LabsDir
	}
	if dir == "" {
		dir = configDefaultLabsDirName
	}
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(filepath.Dir(deps.ConfigPath), dir)
}

// configSchemaDefaultLab returns a Lab named name populated with the config
// add schema defaults documented above. Fields with no sensible fleet-wide
// default (network.vxlan_tag, network.cidr, dns.zone, network.mgmt) are left
// at their zero value: vxlan_tag and cidr are required overrides (see
// validateConfigAddLab), while dns.zone and network.mgmt have no add flag at
// all and are left for the operator to fill in by hand afterward.
func configSchemaDefaultLab(name string) *config.Lab {
	return &config.Lab{
		Name: name,
		Mode: configDefaultMode,
		Network: config.LabNetwork{
			VnetID:    name,
			VnetAlias: "lab-" + name,
			MTU:       1450,
		},
		Compute: config.LabCompute{
			VCPU:     configDefaultVCPU,
			CPUType:  "host",
			NUMA:     true,
			Machine:  "q35",
			Firmware: "ovmf",
			Memory: config.LabMemory{
				MinGB: configDefaultMemoryMinGB,
				MaxGB: configDefaultMemoryMaxGB,
			},
		},
		Storage: config.LabStorage{
			Pool:       "tank",
			OSDiskGB:   configDefaultOSDiskGB,
			DataDiskGB: configDefaultDataDiskGB,
			RefquotaGB: configDefaultRefquotaGB,
			Controller: "virtio-scsi-single",
			IOThread:   true,
			Discard:    true,
			SSD:        true,
		},
		Provisioning: config.LabProvisioning{
			Mode:           "answer-toml",
			AnswerTemplate: "templates/answer.toml.tmpl",
		},
		Access: config.LabAccess{
			Realm: "pve",
			Pool:  "lab-" + name,
			Role:  configDefaultAccessRole,
		},
	}
}

// configExampleLab returns a fully-populated Lab, documenting every lab schema
// field with realistic example values, for `pmx lab config init` to
// render into labs_dir/example.yaml. It intentionally does not correspond to
// any real lab assignment.
func configExampleLab() *config.Lab {
	return &config.Lab{
		Name:  "example",
		Mode:  "nested",
		Owner: "member@pve",
		Network: config.LabNetwork{
			VnetID:    "example",
			VnetAlias: "lab-example",
			VxlanTag:  5001,
			CIDR:      "10.108.0.0/16",
			Mgmt: config.LabMgmt{
				Subnet:  "10.108.0.0/24",
				HostIP:  "10.108.0.10",
				Gateway: "10.108.0.1",
			},
			BoshBloc: "10.108.16.0/20",
			MTU:      1450,
		},
		Compute: config.LabCompute{
			VCPU:     configDefaultVCPU,
			CPUType:  "host",
			NUMA:     true,
			Machine:  "q35",
			Firmware: "ovmf",
			Memory: config.LabMemory{
				MinGB: configDefaultMemoryMinGB,
				MaxGB: configDefaultMemoryMaxGB,
			},
		},
		Storage: config.LabStorage{
			Pool:       "tank",
			OSDiskGB:   configDefaultOSDiskGB,
			DataDiskGB: configDefaultDataDiskGB,
			RefquotaGB: configDefaultRefquotaGB,
			Controller: "virtio-scsi-single",
			IOThread:   true,
			Discard:    true,
			SSD:        true,
		},
		DNS: config.LabDNS{
			Zone: "example.lab.fivetwenty.io",
		},
		Provisioning: config.LabProvisioning{
			Mode:           "answer-toml",
			AnswerTemplate: "templates/answer.toml.tmpl",
			SSHKeys:        []string{"~/.ssh/example-lab.pub"},
		},
		Access: config.LabAccess{
			Realm: "pve",
			Pool:  "lab-example",
			Role:  configDefaultAccessRole,
		},
	}
}

// newConfigInitCmd builds `pmx lab config init`.
func newConfigInitCmd() *cobra.Command {
	var (
		labsDirFlag string
		force       bool
	)

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Scaffold labs_dir with a commented example lab",
		Long: "Ensure the lab include directory exists (0700) and write a fully-commented " +
			"example.yaml documenting every lab schema field to it, ready to copy by hand " +
			"or as a starting point for 'pmx lab config add'.\n\n" +
			"Never rewrites config.yml: if labs_dir is not already set there, the one line " +
			"to add is printed instead of being written for you, so config.yml's comments " +
			"are never lost to a struct-marshal rewrite.",
		Example: `  pmx lab config init
  pmx lab config init --labs-dir ~/labs.d
  pmx lab config init --force`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			deps := cli.GetDeps(cmd)
			dir := configResolveLabsDir(deps, labsDirFlag)

			path, err := config.WriteLabFile(dir, configExampleLab(), force)
			if err != nil {
				return fmt.Errorf("lab config init: %w", err)
			}

			msg := fmt.Sprintf("Wrote example lab template to %s.", path)
			if deps.Cfg.LabsDir == "" {
				keyValue := labsDirFlag
				if keyValue == "" {
					keyValue = configDefaultLabsDirName + "/"
				}
				msg += fmt.Sprintf(
					"\nAdd `labs_dir: %s` to %s to include every lab under %s automatically.",
					keyValue, deps.ConfigPath, dir)
			}

			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg}, deps.Format)
		},
	}

	cmd.Flags().StringVar(&labsDirFlag, "labs-dir", "",
		"directory for lab include files (default: labs_dir from config.yml, or labs.d/ relative to it)")
	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing example.yaml")

	cmd.Annotations = map[string]string{"noClient": "true"}
	return cmd
}

// newConfigAddCmd builds `pmx lab config add <name>`.
func newConfigAddCmd() *cobra.Command {
	var (
		labsDirFlag string
		force       bool
		vnetID      string
		vxlanTag    int
		cidr        string
		vcpu        int
		memoryMaxGB int
		dataDiskGB  int
		refquotaGB  int
		owner       string
		pool        string
		role        string
		mode        string
	)

	cmd := &cobra.Command{
		Use:   "add <name>",
		Short: "Write a new lab definition to labs_dir",
		Long: "Build a lab definition from schema defaults plus any flag overrides and write " +
			"it to a file named after the lab inside labs_dir.\n\n" +
			"Refuses to overwrite an existing file at that path, and refuses when the name " +
			"already resolves via the current config (inline or another included file), " +
			"unless --force. The written file never carries default_user_password: Lab has " +
			"no such field, so a per-lab file cannot leak the fleet's bootstrap secret.",
		Example: `  pmx lab config add wayne --vnet-id wayne --vxlan-tag 5001 --cidr 10.108.0.0/16
  pmx lab config add pipeline --vxlan-tag 5008 --cidr 10.115.0.0/16 --vcpu 24 --role PVEVMUser`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := strings.TrimSpace(args[0])
			if name == "" {
				return fmt.Errorf("lab config add: name is required")
			}

			lab := configSchemaDefaultLab(name)
			flags := cmd.Flags()
			if flags.Changed("vnet-id") {
				lab.Network.VnetID = vnetID
			}
			if flags.Changed("vxlan-tag") {
				lab.Network.VxlanTag = vxlanTag
			}
			if flags.Changed("cidr") {
				lab.Network.CIDR = cidr
			}
			if flags.Changed("vcpu") {
				lab.Compute.VCPU = vcpu
			}
			if flags.Changed("memory-max-gb") {
				lab.Compute.Memory.MaxGB = memoryMaxGB
			}
			if flags.Changed("data-disk-gb") {
				lab.Storage.DataDiskGB = dataDiskGB
			}
			if flags.Changed("refquota-gb") {
				lab.Storage.RefquotaGB = refquotaGB
			}
			if flags.Changed("owner") {
				lab.Owner = owner
			}
			if flags.Changed("pool") {
				lab.Access.Pool = pool
			}
			if flags.Changed("role") {
				lab.Access.Role = role
			}
			if flags.Changed("mode") {
				lab.Mode = mode
			}

			if err := validateConfigAddLab(lab); err != nil {
				return fmt.Errorf("lab config add %q: %w", name, err)
			}

			// Guard the same full identifier set resolveLabForMutate guards
			// (vnet ID, resolved pool, storage ID, DNS zone, VM name), so a
			// definition that would be refused at deploy time is refused at
			// write time too, not persisted and discovered later.
			target := peppi.Target{
				Names: []string{
					lab.Network.VnetID,
					labPoolID(lab),
					storageID(lab),
					lab.DNS.Zone,
					lab.Name,
				},
			}
			if err := peppi.Guard(target); err != nil {
				return fmt.Errorf("lab config add %q: %w", name, err)
			}

			if !force {
				labs, err := config.ResolveLabs(deps.Cfg, deps.ConfigPath)
				if err != nil {
					return fmt.Errorf("lab config add %q: resolve labs: %w", name, err)
				}
				if _, exists := labs[name]; exists {
					return fmt.Errorf(
						"lab config add %q: a lab named %q already resolves via the current config; "+
							"pass --force to write anyway (any existing inline definition is left untouched, "+
							"which will cause a duplicate-lab error on next resolve until you remove it by hand)",
						name, name)
				}
			}

			dir := configResolveLabsDir(deps, labsDirFlag)
			path, err := config.WriteLabFile(dir, lab, force)
			if err != nil {
				return fmt.Errorf("lab config add %q: %w", name, err)
			}

			msg := fmt.Sprintf("Wrote lab %q to %s.", name, path)
			if lab.DNS.Zone == "" {
				msg += " No --dns-zone flag exists yet: set dns.zone (and network.mgmt, if needed) by hand before deploying."
			}
			return deps.Out.Render(cmd.OutOrStdout(), output.Result{Message: msg}, deps.Format)
		},
	}

	cmd.Flags().StringVar(&labsDirFlag, "labs-dir", "",
		"directory for lab include files (default: labs_dir from config.yml, or labs.d/ relative to it)")
	cmd.Flags().BoolVar(&force, "force", false,
		"overwrite an existing file, or write despite the name already resolving")
	cmd.Flags().StringVar(&vnetID, "vnet-id", "",
		"SDN vnet ID, 1-8 alphanumeric characters, no hyphen (default: the lab name)")
	cmd.Flags().IntVar(&vxlanTag, "vxlan-tag", 0,
		"VXLAN tag for the lab's vnet (required; must be unique across labs)")
	cmd.Flags().StringVar(&cidr, "cidr", "",
		"overall subnet CIDR allocated to the lab (required)")
	cmd.Flags().IntVar(&vcpu, "vcpu", configDefaultVCPU, "vCPUs assigned to the lab's VM")
	cmd.Flags().IntVar(&memoryMaxGB, "memory-max-gb", configDefaultMemoryMaxGB,
		"maximum (ballooned) memory in GB")
	cmd.Flags().IntVar(&dataDiskGB, "data-disk-gb", configDefaultDataDiskGB, "data disk size in GB")
	cmd.Flags().IntVar(&refquotaGB, "refquota-gb", configDefaultRefquotaGB,
		"ZFS refquota enforced on the lab's dataset, in GB")
	cmd.Flags().StringVar(&owner, "owner", "",
		`pve user this lab is assigned to ("user@realm"); empty means none`)
	cmd.Flags().StringVar(&pool, "pool", "",
		"pve resource pool for the lab's access grant (derived from the lab name, e.g. lab-wayne, when omitted)")
	cmd.Flags().StringVar(&role, "role", configDefaultAccessRole, "pve role granted to the lab's owner")
	cmd.Flags().StringVar(&mode, "mode", configDefaultMode, `lab mode: "nested" or "hardware"`)

	cmd.Annotations = map[string]string{"noClient": "true"}
	return cmd
}

// validateConfigAddLab checks the fields `config add` cannot safely leave at
// a fleet-wide default or an unvalidated flag value: the vnet ID's format,
// mode's enum, and every numeric/CIDR field that would otherwise silently
// write a non-functional lab definition to disk.
func validateConfigAddLab(lab *config.Lab) error {
	if !configVnetIDPattern.MatchString(lab.Network.VnetID) {
		return fmt.Errorf(
			"vnet-id %q must be 1-8 alphanumeric characters with no hyphen", lab.Network.VnetID)
	}

	if lab.Mode != "nested" && lab.Mode != "hardware" {
		return fmt.Errorf(`mode %q must be "nested" or "hardware"`, lab.Mode)
	}

	if lab.Network.VxlanTag <= 0 {
		return fmt.Errorf("--vxlan-tag is required and must be > 0")
	}

	if lab.Network.CIDR == "" {
		return fmt.Errorf("--cidr is required")
	}
	if _, _, err := net.ParseCIDR(lab.Network.CIDR); err != nil {
		return fmt.Errorf("cidr %q is invalid: %w", lab.Network.CIDR, err)
	}
	if issues := labNetworkPlanIssues(lab.Network); len(issues) > 0 {
		return fmt.Errorf("network plan is incoherent:\n  %s", strings.Join(issues, "\n  "))
	}

	if lab.Compute.VCPU <= 0 {
		return fmt.Errorf("vcpu must be > 0, got %d", lab.Compute.VCPU)
	}
	if lab.Compute.Memory.MaxGB <= 0 {
		return fmt.Errorf("memory-max-gb must be > 0, got %d", lab.Compute.Memory.MaxGB)
	}
	if lab.Storage.DataDiskGB <= 0 {
		return fmt.Errorf("data-disk-gb must be > 0, got %d", lab.Storage.DataDiskGB)
	}
	if lab.Storage.RefquotaGB <= 0 {
		return fmt.Errorf("refquota-gb must be > 0, got %d", lab.Storage.RefquotaGB)
	}

	return nil
}

// newConfigShowCmd builds `pmx lab config show <name>`.
func newConfigShowCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <name>",
		Short: "Show a resolved lab and where it came from",
		Long: "Resolve the named lab the same way every other lab verb does (inline config.yml plus " +
			"labs_dir/include files) and render its merged definition alongside its " +
			"provenance: \"config.yml (inline)\", or the path of the file it was loaded from.\n\n" +
			"-o/--output (inherited from the root command) selects table, plain, json, or yaml.",
		Example: `  pmx lab config show wayne
  pmx lab config show wayne -o json`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			deps := cli.GetDeps(cmd)
			name := args[0]

			labs, err := config.ResolveLabs(deps.Cfg, deps.ConfigPath)
			if err != nil {
				return fmt.Errorf("lab config show %q: resolve labs: %w", name, err)
			}

			lab, ok := labs[name]
			if !ok {
				return fmt.Errorf("lab %q not found; available: %s", name, availableLabNames(labs))
			}

			provenance, err := config.LabProvenance(deps.Cfg, deps.ConfigPath, name)
			if err != nil {
				return fmt.Errorf("lab config show %q: %w", name, err)
			}

			single := map[string]string{
				"name":          lab.Name,
				"mode":          lab.Mode,
				"owner":         lab.Owner,
				"vnet_id":       lab.Network.VnetID,
				"vxlan_tag":     strconv.Itoa(lab.Network.VxlanTag),
				"cidr":          lab.Network.CIDR,
				"vcpu":          strconv.Itoa(lab.Compute.VCPU),
				"memory_max_gb": strconv.Itoa(lab.Compute.Memory.MaxGB),
				"data_disk_gb":  strconv.Itoa(lab.Storage.DataDiskGB),
				"refquota_gb":   strconv.Itoa(lab.Storage.RefquotaGB),
				"dns_zone":      lab.DNS.Zone,
				"pool":          lab.Access.Pool,
				"role":          lab.Access.Role,
				"provenance":    provenance,
			}

			res := output.Result{
				Single: single,
				Raw: map[string]any{
					"lab":        lab,
					"provenance": provenance,
				},
			}
			return deps.Out.Render(cmd.OutOrStdout(), res, deps.Format)
		},
	}

	cmd.Annotations = map[string]string{"noClient": "true"}
	return cmd
}
