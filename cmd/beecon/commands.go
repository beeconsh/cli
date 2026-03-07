package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/spf13/cobra"
	"github.com/terracotta-ai/beecon/internal/api"
	"github.com/terracotta-ai/beecon/internal/resolver"
	"github.com/terracotta-ai/beecon/internal/scaffold"
	"github.com/terracotta-ai/beecon/internal/ui"
	"github.com/terracotta-ai/beecon/internal/witness"
)

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the version",
	Args:  cobra.NoArgs,
	Run: func(_ *cobra.Command, _ []string) {
		fmt.Printf("beecon %s (%s)\n", version, commit)
	},
}

var initCmd = &cobra.Command{
	Use:   "init [dir]",
	Short: "Initialize a new beecon project",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		dir := "."
		if len(args) > 0 {
			dir = args[0]
		}
		p, err := scaffold.Init(dir)
		if err != nil {
			return err
		}
		fmt.Println("created", p)
		return nil
	},
}

var validateCmd = &cobra.Command{
	Use:         "validate [path]",
	Short:       "Validate a beacon file",
	Args:        cobra.MaximumNArgs(1),
	Annotations: needsEngine,
	RunE: func(_ *cobra.Command, args []string) error {
		path := beaconPathArg(args)
		if err := eng.Validate(path); err != nil {
			return err
		}
		fmt.Println("valid", path)
		return nil
	},
}

var planCmd = &cobra.Command{
	Use:         "plan [path]",
	Short:       "Show the execution plan for a beacon file",
	Args:        cobra.MaximumNArgs(1),
	Annotations: needsEngine,
	RunE: func(cmd *cobra.Command, args []string) error {
		path := beaconPathArg(args)
		res, err := eng.Plan(cmd.Context(), path)
		if err != nil {
			return err
		}
		fmt.Printf("domain: %s\n", res.Graph.Domain.Name)
		fmt.Printf("nodes: %d edges: %d\n", len(res.Graph.Nodes), len(res.Graph.Edges))
		fmt.Print(resolver.FormatPlan(res.Plan))
		return nil
	},
}

var applyCmd = &cobra.Command{
	Use:         "apply [path]",
	Short:       "Apply changes from a beacon file",
	Args:        cobra.MaximumNArgs(1),
	Annotations: needsEngine,
	RunE: func(cmd *cobra.Command, args []string) error {
		path := beaconPathArg(args)
		res, err := eng.Apply(cmd.Context(), path)
		if err != nil {
			return err
		}
		fmt.Printf("run: %s\n", res.RunID)
		fmt.Printf("executed: %d\n", res.Executed)
		if res.ApprovalRequestID != "" {
			fmt.Printf("approval_required: %s\n", res.ApprovalRequestID)
			fmt.Printf("pending_actions: %d\n", res.Pending)
		}
		return nil
	},
}

var statusCmd = &cobra.Command{
	Use:         "status",
	Short:       "Show current infrastructure status",
	Args:        cobra.NoArgs,
	Annotations: needsEngine,
	RunE: func(cmd *cobra.Command, _ []string) error {
		st, err := eng.Status(cmd.Context())
		if err != nil {
			return err
		}
		fmt.Printf("resources: %d\n", len(st.Resources))
		fmt.Printf("runs: %d\n", len(st.Runs))
		fmt.Printf("approvals_pending: %d\n", pendingApprovals(st))
		fmt.Printf("audit_events: %d\n", len(st.Audit))
		if len(st.Resources) == 0 {
			return nil
		}
		ids := make([]string, 0, len(st.Resources))
		for id := range st.Resources {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			r := st.Resources[id]
			fmt.Printf("- %s status=%s managed=%v last_op=%s\n", id, r.Status, r.Managed, r.LastOperation)
		}
		return nil
	},
}

var beaconsCmd = &cobra.Command{
	Use:         "beacons",
	Short:       "List discovered beacon files",
	Args:        cobra.NoArgs,
	Annotations: needsEngine,
	RunE: func(_ *cobra.Command, _ []string) error {
		paths, err := eng.DiscoverBeacons()
		if err != nil {
			return err
		}
		if len(paths) == 0 {
			fmt.Println("no .beecon files found")
			return nil
		}
		for _, p := range paths {
			fmt.Println(p)
		}
		return nil
	},
}

var driftCmd = &cobra.Command{
	Use:         "drift [path]",
	Short:       "Detect configuration drift",
	Args:        cobra.MaximumNArgs(1),
	Annotations: needsEngine,
	RunE: func(cmd *cobra.Command, args []string) error {
		path := beaconPathArg(args)
		drifted, observeErrors, err := eng.Drift(cmd.Context(), path)
		if err != nil {
			return err
		}
		for _, e := range observeErrors {
			fmt.Fprintln(os.Stderr, "warning:", e)
		}
		if len(drifted) == 0 {
			fmt.Println("no drift detected")
			return nil
		}
		fmt.Printf("drifted: %d\n", len(drifted))
		for _, r := range drifted {
			fmt.Printf("- %s (%s)\n", r.ResourceID, r.NodeType)
		}
		return nil
	},
}

var approveCmd = &cobra.Command{
	Use:         "approve <request-id> [approver]",
	Short:       "Approve a pending approval request",
	Args:        cobra.RangeArgs(1, 2),
	Annotations: needsEngine,
	RunE: func(cmd *cobra.Command, args []string) error {
		approver := "cli-user"
		if len(args) > 1 {
			approver = args[1]
		}
		res, err := eng.Approve(cmd.Context(), args[0], approver)
		if err != nil {
			return err
		}
		fmt.Printf("run: %s\n", res.RunID)
		fmt.Printf("executed_after_approval: %d\n", res.Executed)
		return nil
	},
}

var rejectCmd = &cobra.Command{
	Use:         "reject <request-id> [approver] [reason]",
	Short:       "Reject a pending approval request",
	Args:        cobra.RangeArgs(1, 3),
	Annotations: needsEngine,
	RunE: func(cmd *cobra.Command, args []string) error {
		approver := "cli-user"
		if len(args) > 1 {
			approver = args[1]
		}
		reason := "rejected by user"
		if len(args) > 2 {
			reason = args[2]
		}
		if err := eng.Reject(cmd.Context(), args[0], approver, reason); err != nil {
			return err
		}
		fmt.Printf("rejected: %s by %s\n", args[0], approver)
		return nil
	},
}

var historyCmd = &cobra.Command{
	Use:         "history <resource-id>",
	Short:       "Show history for a resource",
	Args:        cobra.ExactArgs(1),
	Annotations: needsEngine,
	RunE: func(cmd *cobra.Command, args []string) error {
		events, err := eng.History(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		if len(events) == 0 {
			fmt.Println("no history")
			return nil
		}
		for _, ev := range events {
			fmt.Printf("%s %s %s %s\n", ev.Timestamp.Format("2006-01-02T15:04:05Z07:00"), ev.Type, ev.ResourceID, ev.Message)
		}
		return nil
	},
}

var rollbackCmd = &cobra.Command{
	Use:         "rollback <run-id>",
	Short:       "Rollback a previous run",
	Args:        cobra.ExactArgs(1),
	Annotations: needsEngine,
	RunE: func(cmd *cobra.Command, args []string) error {
		id, err := eng.Rollback(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		fmt.Println("rollback_run:", id)
		return nil
	},
}

var connectCmd = &cobra.Command{
	Use:         "connect <provider> [region]",
	Short:       "Connect a cloud provider",
	Args:        cobra.RangeArgs(1, 2),
	Annotations: needsEngine,
	RunE: func(cmd *cobra.Command, args []string) error {
		region := ""
		if len(args) > 1 {
			region = args[1]
		}
		if err := eng.Connect(cmd.Context(), args[0], region); err != nil {
			return err
		}
		fmt.Printf("connected %s %s\n", args[0], region)
		return nil
	},
}

var performanceCmd = &cobra.Command{
	Use:         "performance <resource-id> <metric> <observed> <threshold> [duration]",
	Short:       "Report a performance breach",
	Args:        cobra.RangeArgs(4, 5),
	Annotations: needsEngine,
	RunE: func(cmd *cobra.Command, args []string) error {
		dur := "5m"
		if len(args) > 4 {
			dur = args[4]
		}
		candidates := witness.EvaluateBreach(args[1], args[2], args[3])
		for _, c := range candidates {
			fmt.Printf("candidate: %s (%s)\n", c.Action, c.Reason)
		}
		id, err := eng.IngestPerformanceBreach(cmd.Context(), args[0], args[1], args[2], args[3], dur)
		if err != nil {
			return err
		}
		fmt.Println("event_id:", id)
		return nil
	},
}

var serveCmd = &cobra.Command{
	Use:         "serve [addr]",
	Short:       "Start the API server and Mission Control UI",
	Args:        cobra.MaximumNArgs(1),
	Annotations: needsEngine,
	RunE: func(cmd *cobra.Command, args []string) error {
		addr := "127.0.0.1:8080"
		if len(args) > 0 {
			addr = args[0]
		}
		apiHandler := api.New(eng).Handler()
		uiHandler := ui.Handler(os.Getenv("BEECON_API_KEY"))

		mux := http.NewServeMux()
		mux.Handle("/api/", apiHandler)
		mux.Handle("/", uiHandler)

		if !strings.HasPrefix(addr, ":") {
			fmt.Println("serving Mission Control + API on", addr)
		} else {
			fmt.Println("serving Mission Control on http://localhost" + addr)
			fmt.Println("API base: http://localhost" + addr + "/api")
		}

		srv := &http.Server{Addr: addr, Handler: mux}
		go func() {
			<-cmd.Context().Done()
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = srv.Shutdown(shutdownCtx)
		}()
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	},
}
