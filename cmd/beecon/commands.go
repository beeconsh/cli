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
	"github.com/terracotta-ai/beecon/internal/btest"
	"github.com/terracotta-ai/beecon/internal/cli"
	"github.com/terracotta-ai/beecon/internal/cost"
	"github.com/terracotta-ai/beecon/internal/engine"
	"github.com/terracotta-ai/beecon/internal/scaffold"
	"github.com/terracotta-ai/beecon/internal/security"
	"github.com/terracotta-ai/beecon/internal/state"
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
		out.Blank()
		out.Line(out.Green(out.OK()), "Created %s", p)
		out.Blank()
		out.Next("edit "+p+" with your infrastructure intent,",
			"then run `beecon validate` to check syntax.")
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
		out.Blank()
		out.Line(out.Green(out.OK()), "%s is valid", path)
		out.Blank()
		out.Next("run `beecon plan` to see what actions will be taken.")
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

		if formatFlag == "json" {
			for i := range res.Graph.Nodes {
				res.Graph.Nodes[i].Intent = security.ScrubStringMap(res.Graph.Nodes[i].Intent)
				res.Graph.Nodes[i].Env = security.ScrubStringMap(res.Graph.Nodes[i].Env)
			}
			for _, a := range res.Plan.Actions {
				a.Changes = security.ScrubChanges(a.Changes)
			}
			return cli.WriteJSON(os.Stdout, res)
		}

		// Domain header
		domainName := ""
		if res.Graph.Domain != nil {
			domainName = res.Graph.Domain.Name
		}
		cloudInfo := ""
		if res.CloudProvider != "" {
			cloudInfo = fmt.Sprintf("(%s", res.CloudProvider)
			if res.CloudRegion != "" {
				cloudInfo += " / " + res.CloudRegion
			}
			cloudInfo += ")"
		}
		out.Blank()
		out.Line(out.Dot(), "domain %s  %s", out.Bold(domainName), out.Dim(cloudInfo))
		out.Blank()

		actions := res.Plan.Actions
		if len(actions) == 0 {
			out.Line(out.Green(out.OK()), "No changes needed.")
			return nil
		}

		// Count auto vs approval
		approvalCount := 0
		for _, a := range actions {
			if a.RequiresApproval {
				approvalCount++
			}
		}
		autoCount := len(actions) - approvalCount
		planSummary := fmt.Sprintf("%d actions", len(actions))
		if approvalCount > 0 {
			planSummary += fmt.Sprintf(" (%d auto, %d approval)", autoCount, approvalCount)
		}
		out.Header("Plan: %s", planSummary)
		out.Blank()

		// Action list
		for i, a := range actions {
			annotation := ""
			if a.RequiresApproval {
				annotation = out.Yellow(fmt.Sprintf("%s requires approval (%s)", out.Warn(), a.BoundaryTag))
			} else if a.Operation == "FORBIDDEN" {
				annotation = out.Red(fmt.Sprintf("%s forbidden (%s)", out.Fail(), a.BoundaryTag))
			} else if len(a.DependsOn) > 0 {
				annotation = out.Dim(fmt.Sprintf("%s depends on %s", out.Arrow(), strings.Join(a.DependsOn, ", ")))
			}
			out.NumberedAction(i+1, a.Operation, a.NodeID, annotation)

			// Show diff details for CREATE/UPDATE/DELETE
			if a.Operation == "CREATE" {
				node := res.Graph.NodeByName(a.NodeName)
				if node != nil {
					for _, k := range sortedKeys(node.Intent) {
						if k == "inline_policy" {
							continue // too verbose
						}
						v := node.Intent[k]
						if security.IsSensitiveKey(k) {
							v = "**REDACTED**"
						}
						out.DiffLine("CREATE", k, v)
					}
				}
			} else if a.Operation == "DELETE" {
				out.DiffLine("DELETE", a.NodeID, "(removed)")
			} else if a.Operation == "UPDATE" && len(a.Changes) > 0 {
				for _, k := range sortedKeys(a.Changes) {
					v := a.Changes[k]
					if security.IsSensitiveKey(k) {
						v = "**REDACTED**"
					}
					out.DiffLine("UPDATE", k, v)
				}
			}
		}

		if approvalCount > 0 {
			out.Blank()
			out.Line(out.Yellow(out.Warn()), "%d action(s) require approval before execution.", approvalCount)
		}

		// Compliance report
		if cr := res.ComplianceReport; cr != nil && len(cr.Mutations) > 0 {
			out.Blank()
			out.Header("Compliance")
			for _, m := range cr.Mutations {
				out.Line(out.Dot(), "%s: set %s = %s %s",
					m.NodeID, m.Field, m.Value, out.Dim("("+m.Framework+": "+m.Reason+")"))
			}
		}

		// Cost report
		if cr := res.CostReport; cr != nil && len(cr.Estimates) > 0 {
			out.Blank()
			out.Header("Cost estimate: %s", cost.FormatDelta(cr))
			for _, est := range cr.Estimates {
				detail := est.ResourceType
				if est.InstanceType != "" {
					detail += " (" + est.InstanceType + ")"
				}
				out.Line(out.Dot(), "%s: ~$%.0f/mo %s", est.NodeID, est.MonthlyCost, out.Dim(detail))
			}
			if cr.BudgetExceeded && cr.Budget != nil {
				out.Blank()
				out.Line(out.Red(out.Fail()), "Exceeds budget of $%.0f/mo", cr.Budget.MonthlyAmount())
			}
			for _, w := range cr.Warnings {
				out.Line(out.Yellow(out.Warn()), "%s", w)
			}
			// Cheaper alternatives
			if len(cr.Alternatives) > 0 {
				out.Blank()
				for _, alt := range cr.Alternatives {
					out.Line(out.Yellow("!"), "%s: %s costs ~$%.0f/mo",
						alt.NodeName, alt.CurrentInstance, alt.CurrentCost)
					out.Line(" ", "  > %s at ~$%.0f/mo saves $%.0f/mo",
						alt.SuggestedInstance, alt.SuggestedCost, alt.MonthlySavings)
				}
			}
		}

		out.Blank()
		out.Next("run `beecon apply` to execute this plan.")
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

		if formatFlag == "json" {
			for i := range res.Actions {
				res.Actions[i].Action.Changes = security.ScrubChanges(res.Actions[i].Action.Changes)
			}
			return cli.WriteJSON(os.Stdout, res)
		}

		out.Blank()

		// Run header with sim/live mode
		if res.Simulated {
			out.Header("Run %s %s", res.RunID, out.Dim("(simulated)"))
			out.Summary("No cloud resources were modified.")
		} else {
			out.Header("Run %s %s", res.RunID, out.Red("(LIVE)"))
			out.Summary("Cloud resources were modified.")
		}
		out.Blank()

		// Per-action status
		for _, ao := range res.Actions {
			a := ao.Action
			switch ao.Status {
			case engine.ActionExecuted:
				out.ActionLine(out.Green(out.OK()), a.Operation, a.NodeID, "")
			case engine.ActionPending:
				out.ActionLine(out.Yellow(out.Warn()), "PENDING", a.NodeID,
					out.Dim(fmt.Sprintf("requires approval (%s)", a.BoundaryTag)))
			case engine.ActionForbidden:
				out.ActionLine(out.Red(out.Fail()), "FORBIDDEN", a.NodeID,
					out.Dim(fmt.Sprintf("(%s)", a.BoundaryTag)))
			}
		}

		if res.ApprovalRequestID != "" && res.Pending > 0 {
			out.Blank()
			approved := false
			if yesFlag {
				approved = true
			} else if out.ColorEnabled() {
				approved = out.Confirm(fmt.Sprintf("Approve %d pending action(s)? [y/N]", res.Pending))
			}
			if approved {
				approveRes, err := eng.Approve(cmd.Context(), res.ApprovalRequestID, "cli-user")
				if err != nil {
					return err
				}
				out.Blank()
				out.Line(out.Green(out.OK()), "Approved and executed %d action(s)", approveRes.Executed)
				for _, ao := range approveRes.Actions {
					out.ActionLine(out.Green(out.OK()), ao.Action.Operation, ao.Action.NodeID, "")
				}
			} else {
				out.Next(fmt.Sprintf("run `beecon approve %s` to approve", res.ApprovalRequestID),
					"the pending action(s).")
			}
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

		if formatFlag == "json" {
			for _, rec := range st.Resources {
				rec.IntentSnapshot = security.ScrubMap(rec.IntentSnapshot)
				rec.LiveState = security.ScrubMap(rec.LiveState)
			}
			for _, a := range st.Actions {
				a.Changes = security.ScrubChanges(a.Changes)
			}
			return cli.WriteJSON(os.Stdout, st)
		}

		out.Blank()
		out.Summary("Resources: %d     Runs: %d     Pending approvals: %d",
			len(st.Resources), len(st.Runs), pendingApprovals(st))
		out.Blank()

		if len(st.Resources) == 0 {
			return nil
		}
		ids := make([]string, 0, len(st.Resources))
		for id := range st.Resources {
			ids = append(ids, id)
		}
		sort.Strings(ids)

		// Apply status filter if provided
		filterSet := parseFilter(statusFilter)

		for _, id := range ids {
			r := st.Resources[id]
			if len(filterSet) > 0 && !filterSet[string(r.Status)] {
				continue
			}
			detail := ""
			if r.LastOperation != "" {
				detail = "last: " + r.LastOperation
			}
			out.StatusLine(id, string(r.Status), detail)
		}

		// Show approval timeout warnings
		for _, a := range st.Approvals {
			if a.Status == state.ApprovalPending {
				remaining := time.Until(a.ExpiresAt)
				if remaining < 4*time.Hour && remaining > 0 {
					out.Blank()
					out.Line(out.Yellow(out.Warn()), "Approval %s expires in %s", a.ID, remaining.Round(time.Minute))
				}
			}
		}

		out.Blank()
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
		out.Blank()
		if len(paths) == 0 {
			out.Line(out.Dim(out.Dot()), "No .beecon files found")
		} else {
			for _, p := range paths {
				out.Line(out.Dot(), "%s", p)
			}
		}
		out.Blank()
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

		if formatFlag == "json" {
			errStrs := make([]string, len(observeErrors))
			for i, e := range observeErrors {
				errStrs[i] = e.Error()
			}
			for _, rec := range drifted {
				rec.IntentSnapshot = security.ScrubMap(rec.IntentSnapshot)
				rec.LiveState = security.ScrubMap(rec.LiveState)
			}
			return cli.WriteJSON(os.Stdout, struct {
				Drifted []*state.ResourceRecord `json:"drifted"`
				Errors  []string                `json:"errors,omitempty"`
			}{drifted, errStrs})
		}

		for _, e := range observeErrors {
			fmt.Fprintln(os.Stderr, "warning:", e)
		}
		out.Blank()
		if len(drifted) == 0 {
			out.Line(out.Green(out.OK()), "No drift detected")
		} else {
			out.Line(out.Yellow(out.Warn()), "%d resource(s) have drifted", len(drifted))
			out.Blank()
			for _, r := range drifted {
				out.StatusLine(r.ResourceID, "DRIFTED", "("+strings.ToLower(r.NodeType)+")")
			}
			out.Blank()
			out.Next("run `beecon plan` to generate a reconciliation plan.")
		}
		out.Blank()
		return nil
	},
}

var importCmd = &cobra.Command{
	Use:         "import <provider> <resource-type> <provider-id> [region]",
	Short:       "Import an existing cloud resource into beecon state",
	Args:        cobra.RangeArgs(3, 4),
	Annotations: needsEngine,
	RunE: func(cmd *cobra.Command, args []string) error {
		region := ""
		if len(args) > 3 {
			region = args[3]
		}
		resourceID, err := eng.Import(cmd.Context(), args[0], args[1], args[2], region)
		if err != nil {
			return err
		}
		out.Blank()
		out.Line(out.Green(out.OK()), "Imported %s as %s", args[2], out.Bold(resourceID))
		out.Blank()
		out.Next("run `beecon status` to see the imported resource.")
		return nil
	},
}

var refreshCmd = &cobra.Command{
	Use:         "refresh [path]",
	Short:       "Refresh live state for all managed resources",
	Args:        cobra.MaximumNArgs(1),
	Annotations: needsEngine,
	RunE: func(cmd *cobra.Command, args []string) error {
		path := beaconPathArg(args)
		refreshed, observeErrors, err := eng.Refresh(cmd.Context(), path)
		if err != nil {
			return err
		}
		for _, e := range observeErrors {
			fmt.Fprintln(os.Stderr, "warning:", e)
		}
		out.Blank()
		out.Line(out.Green(out.OK()), "Refreshed %d resource(s)", refreshed)
		if len(observeErrors) > 0 {
			out.Line(out.Yellow(out.Warn()), "%d observe error(s)", len(observeErrors))
		}
		out.Blank()
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
		out.Blank()
		out.Line(out.Green(out.OK()), "Approved %s", args[0])
		out.Blank()
		if res.Simulated {
			out.Header("Run %s %s", res.RunID, out.Dim("(simulated)"))
		} else {
			out.Header("Run %s", res.RunID)
		}
		out.Summary("Executed %d remaining action(s).", res.Executed)
		out.Blank()

		for _, ao := range res.Actions {
			out.ActionLine(out.Green(out.OK()), ao.Action.Operation, ao.Action.NodeID, "")
		}
		out.Blank()
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
		out.Blank()
		out.Line(out.Green(out.OK()), "Rejected %s by %s", args[0], approver)
		out.Blank()
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
		out.Blank()
		if len(events) == 0 {
			out.Line(out.Dim(out.Dot()), "No history for %s", args[0])
		} else {
			out.Header("History: %s", args[0])
			out.Blank()
			for _, ev := range events {
				ts := ev.Timestamp.Format("2006-01-02 15:04:05")
				out.Line(out.Dot(), "%s  %s  %s", out.Dim(ts), ev.Type, ev.Message)
			}
		}
		out.Blank()
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
		out.Blank()
		out.Line(out.Green(out.OK()), "Rolled back %s", args[0])
		out.Summary("Rollback run: %s", id)
		out.Blank()
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
		out.Blank()
		connMsg := args[0]
		if region != "" {
			connMsg += " (" + region + ")"
		}
		out.Line(out.Green(out.OK()), "Connected %s", connMsg)
		out.Blank()
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
		out.Blank()
		if len(candidates) > 0 {
			out.Header("Candidate remediations")
			for _, c := range candidates {
				out.Line(out.Arrow(), "%s  %s", c.Action, out.Dim(c.Reason))
			}
			out.Blank()
		}
		id, err := eng.IngestPerformanceBreach(cmd.Context(), args[0], args[1], args[2], args[3], dur)
		if err != nil {
			return err
		}
		out.Line(out.Green(out.OK()), "Recorded performance event %s", id)
		out.Blank()
		return nil
	},
}

var testCmd = &cobra.Command{
	Use:         "test <test-file> [beacon-path]",
	Short:       "Run assertions against a plan result",
	Args:        cobra.RangeArgs(1, 2),
	Annotations: needsEngine,
	RunE: func(cmd *cobra.Command, args []string) error {
		testPath := args[0]
		beaconPath := "infra.beecon"
		if len(args) > 1 {
			beaconPath = args[1]
		}
		res, err := eng.Plan(cmd.Context(), beaconPath)
		if err != nil {
			return err
		}
		tr, err := btest.RunFile(testPath, res)
		if err != nil {
			return err
		}
		out.Blank()
		out.Header("Test: %s", testPath)
		out.Blank()
		for _, a := range tr.Assertions {
			if a.Passed {
				out.Line(out.Green(out.OK()), "line %d: %s", a.Line, a.Raw)
			} else {
				out.Line(out.Red(out.Fail()), "line %d: %s", a.Line, a.Raw)
				if a.Message != "" {
					out.Line(" ", "  %s", out.Red(a.Message))
				}
			}
		}
		out.Blank()
		out.Summary("%d passed, %d failed", tr.Passed, tr.Failed)
		out.Blank()
		if tr.Failed > 0 {
			return fmt.Errorf("%d assertion(s) failed", tr.Failed)
		}
		return nil
	},
}

var watchCmd = &cobra.Command{
	Use:         "watch [path]",
	Short:       "Watch for drift on a recurring interval",
	Args:        cobra.MaximumNArgs(1),
	Annotations: needsEngine,
	RunE: func(cmd *cobra.Command, args []string) error {
		path := beaconPathArg(args)
		interval, err := time.ParseDuration(watchInterval)
		if err != nil {
			return fmt.Errorf("invalid interval %q: %w", watchInterval, err)
		}
		out.Blank()
		out.Line(out.Dot(), "Watching %s every %s (Ctrl+C to stop)", path, interval)
		out.Blank()

		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		runCheck := func() {
			drifted, observeErrors, err := eng.Drift(cmd.Context(), path)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  %s drift check failed: %v\n", out.Red(out.Fail()), err)
				return
			}
			for _, e := range observeErrors {
				fmt.Fprintln(os.Stderr, "  warning:", e)
			}
			ts := time.Now().Format("15:04:05")
			if len(drifted) == 0 {
				out.Line(out.Dim(ts), "%s no drift", out.Green(out.OK()))
			} else {
				out.Line(out.Dim(ts), "%s %d resource(s) drifted", out.Yellow(out.Warn()), len(drifted))
				for _, r := range drifted {
					out.Line(" ", "  %s %s", r.ResourceID, out.Dim("("+strings.ToLower(r.NodeType)+")"))
				}
			}
		}

		// Run immediately on start
		runCheck()

		for {
			select {
			case <-cmd.Context().Done():
				out.Blank()
				out.Line(out.Dot(), "Watch stopped")
				return nil
			case <-ticker.C:
				runCheck()
			}
		}
	},
}

// sortedKeys returns the keys of a map sorted alphabetically.
func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// parseFilter splits a comma-separated filter string into a set of uppercase tokens.
func parseFilter(filter string) map[string]bool {
	if strings.TrimSpace(filter) == "" {
		return nil
	}
	parts := strings.Split(filter, ",")
	set := make(map[string]bool, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(strings.ToUpper(p))
		if p != "" {
			set[p] = true
		}
	}
	return set
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

		out.Blank()
		if !strings.HasPrefix(addr, ":") {
			out.Line(out.Green(out.OK()), "Serving Mission Control + API on %s", out.Bold(addr))
		} else {
			out.Line(out.Green(out.OK()), "Serving Mission Control on %s", out.Bold("http://localhost"+addr))
			out.Summary("API base: http://localhost%s/api", addr)
		}
		out.Blank()

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
