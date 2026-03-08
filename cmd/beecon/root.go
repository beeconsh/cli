package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/terracotta-ai/beecon/internal/cli"
	"github.com/terracotta-ai/beecon/internal/engine"
	"github.com/terracotta-ai/beecon/internal/logging"
	"github.com/terracotta-ai/beecon/internal/state"
	"gopkg.in/yaml.v3"
)

var (
	version = "dev"
	commit  = "none"
)

// eng is initialized in PersistentPreRunE for commands that need it.
var eng *engine.Engine

// out is the CLI output writer, initialized once.
var out = cli.New(os.Stdout)

// CLI flags
var profileFlag string
var forceFlag bool
var formatFlag string
var debugFlag bool
var yesFlag bool
var statusFilter string

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	if err := rootCmd.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:           "beecon",
	Short:         "Infrastructure-as-code engine for cloud resources",
	SilenceUsage:  true,
	SilenceErrors: true,
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
		if debugFlag {
			logging.Enable()
		}
		if cmd.Annotations["needs_engine"] != "true" {
			return nil
		}
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		profile, err := resolveProfile(cwd)
		if err != nil {
			return err
		}
		eng = engine.New(cwd)
		eng.ActiveProfile = profile
		eng.Force = forceFlag
		return nil
	},
}

// needsEngine is the annotation map added to commands that require engine initialization.
var needsEngine = map[string]string{"needs_engine": "true"}

func init() {
	rootCmd.Version = fmt.Sprintf("%s (%s)", version, commit)
	rootCmd.SetVersionTemplate("beecon {{.Version}}\n")

	// Profile flag: --profile (persistent across subcommands)
	rootCmd.PersistentFlags().StringVar(&profileFlag, "profile", "", "active profile (e.g. production, staging)")

	// Format flag: --format (persistent across subcommands)
	rootCmd.PersistentFlags().StringVar(&formatFlag, "format", "text", "output format (text, json)")

	// Debug flag: --debug (persistent across subcommands)
	rootCmd.PersistentFlags().BoolVar(&debugFlag, "debug", false, "enable debug logging to stderr")

	// Force flag on apply command
	applyCmd.Flags().BoolVar(&forceFlag, "force", false, "bypass budget enforcement")

	// Yes flag on apply command
	applyCmd.Flags().BoolVar(&yesFlag, "yes", false, "auto-approve pending actions")

	// Filter flag on status command
	statusCmd.Flags().StringVar(&statusFilter, "filter", "", "filter by status (DRIFTED,MATCHED,PENDING_APPROVAL,OBSERVED)")

	rootCmd.AddCommand(
		versionCmd,
		initCmd,
		validateCmd,
		planCmd,
		applyCmd,
		statusCmd,
		beaconsCmd,
		driftCmd,
		approveCmd,
		rejectCmd,
		historyCmd,
		rollbackCmd,
		connectCmd,
		performanceCmd,
		serveCmd,
	)
}

// resolveProfile determines the active profile from CLI flag > env var > config file.
func resolveProfile(cwd string) (string, error) {
	// 1. CLI flag (highest precedence)
	if profileFlag != "" {
		return profileFlag, nil
	}
	// 2. Environment variable
	if env := os.Getenv("BEECON_PROFILE"); env != "" {
		return env, nil
	}
	// 3. Config file
	configPath := filepath.Join(cwd, ".beecon", "config.yaml")
	data, err := os.ReadFile(configPath)
	if err != nil {
		// File doesn't exist or can't be read — no config profile
		return "", nil
	}
	var cfg struct {
		Profile string `yaml:"profile"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return "", fmt.Errorf("parse %s: %w", configPath, err)
	}
	return cfg.Profile, nil
}

func beaconPathArg(args []string) string {
	if len(args) == 0 {
		return "infra.beecon"
	}
	return args[0]
}

func pendingApprovals(st *state.State) int {
	n := 0
	for _, a := range st.Approvals {
		if a.Status == state.ApprovalPending {
			n++
		}
	}
	return n
}
