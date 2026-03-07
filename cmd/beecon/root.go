package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/terracotta-ai/beecon/internal/engine"
	"github.com/terracotta-ai/beecon/internal/state"
)

var (
	version = "dev"
	commit  = "none"
)

// eng is initialized in PersistentPreRunE for commands that need it.
var eng *engine.Engine

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
	Version:       fmt.Sprintf("%s (%s)", version, commit),
	PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
		// Commands that don't need the engine.
		switch cmd.Name() {
		case "version", "init", "beecon":
			return nil
		}
		cwd, err := os.Getwd()
		if err != nil {
			return err
		}
		eng = engine.New(cwd)
		return nil
	},
}

func init() {
	rootCmd.SetVersionTemplate("beecon {{.Version}}\n")
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
