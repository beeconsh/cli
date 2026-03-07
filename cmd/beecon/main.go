package main

import (
	"fmt"
	"net/http"
	"os"
	"sort"
	"strings"

	"github.com/terracotta-ai/beecon/internal/api"
	"github.com/terracotta-ai/beecon/internal/engine"
	"github.com/terracotta-ai/beecon/internal/resolver"
	"github.com/terracotta-ai/beecon/internal/scaffold"
	"github.com/terracotta-ai/beecon/internal/state"
	"github.com/terracotta-ai/beecon/internal/ui"
	"github.com/terracotta-ai/beecon/internal/witness"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(1)
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	eng := engine.New(cwd)

	cmd := os.Args[1]
	args := os.Args[2:]

	switch cmd {
	case "version", "--version", "-v":
		fmt.Printf("beecon %s (%s)\n", version, commit)
		return
	case "init":
		err = runInit(args)
	case "validate":
		err = runValidate(eng, args)
	case "plan":
		err = runPlan(eng, args)
	case "apply":
		err = runApply(eng, args)
	case "status":
		err = runStatus(eng)
	case "beacons":
		err = runBeacons(eng)
	case "drift":
		err = runDrift(eng, args)
	case "approve":
		err = runApprove(eng, args)
	case "reject":
		err = runReject(eng, args)
	case "history":
		err = runHistory(eng, args)
	case "rollback":
		err = runRollback(eng, args)
	case "connect":
		err = runConnect(eng, args)
	case "performance":
		err = runPerformance(eng, args)
	case "serve":
		err = runServe(eng, args)
	default:
		usage()
		err = fmt.Errorf("unknown command %q", cmd)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Println("beecon <command> [args]")
	fmt.Println("commands: init, validate, plan, apply, status, beacons, drift, approve, reject, history, rollback, connect, performance, serve")
}

func runInit(args []string) error {
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
}

func runValidate(eng *engine.Engine, args []string) error {
	path := beaconPathArg(args)
	if err := eng.Validate(path); err != nil {
		return err
	}
	fmt.Println("valid", path)
	return nil
}

func runPlan(eng *engine.Engine, args []string) error {
	path := beaconPathArg(args)
	res, err := eng.Plan(path)
	if err != nil {
		return err
	}
	fmt.Printf("domain: %s\n", res.Graph.Domain.Name)
	fmt.Printf("nodes: %d edges: %d\n", len(res.Graph.Nodes), len(res.Graph.Edges))
	fmt.Print(resolver.FormatPlan(res.Plan))
	return nil
}

func runApply(eng *engine.Engine, args []string) error {
	path := beaconPathArg(args)
	res, err := eng.Apply(path)
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
}

func runStatus(eng *engine.Engine) error {
	st, err := eng.Status()
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
}

func runBeacons(eng *engine.Engine) error {
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
}

func runDrift(eng *engine.Engine, args []string) error {
	path := beaconPathArg(args)
	drifted, err := eng.Drift(path)
	if err != nil {
		return err
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
}

func runApprove(eng *engine.Engine, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: beecon approve <request-id> [approver]")
	}
	approver := "cli-user"
	if len(args) > 1 {
		approver = args[1]
	}
	res, err := eng.Approve(args[0], approver)
	if err != nil {
		return err
	}
	fmt.Printf("run: %s\n", res.RunID)
	fmt.Printf("executed_after_approval: %d\n", res.Executed)
	return nil
}

func runReject(eng *engine.Engine, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: beecon reject <request-id> [approver] [reason]")
	}
	approver := "cli-user"
	if len(args) > 1 {
		approver = args[1]
	}
	reason := "rejected by user"
	if len(args) > 2 {
		reason = args[2]
	}
	if err := eng.Reject(args[0], approver, reason); err != nil {
		return err
	}
	fmt.Printf("rejected: %s by %s\n", args[0], approver)
	return nil
}

func runHistory(eng *engine.Engine, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: beecon history <resource-id>")
	}
	events, err := eng.History(args[0])
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
}

func runRollback(eng *engine.Engine, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: beecon rollback <run-id>")
	}
	id, err := eng.Rollback(args[0])
	if err != nil {
		return err
	}
	fmt.Println("rollback_run:", id)
	return nil
}

func runConnect(eng *engine.Engine, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: beecon connect <provider> [region]")
	}
	region := ""
	if len(args) > 1 {
		region = args[1]
	}
	if err := eng.Connect(args[0], region); err != nil {
		return err
	}
	fmt.Printf("connected %s %s\n", args[0], region)
	return nil
}

func runPerformance(eng *engine.Engine, args []string) error {
	if len(args) < 4 {
		return fmt.Errorf("usage: beecon performance <resource-id> <metric> <observed> <threshold> [duration]")
	}
	dur := "5m"
	if len(args) > 4 {
		dur = args[4]
	}
	candidates := witness.EvaluateBreach(args[1], args[2], args[3])
	for _, c := range candidates {
		fmt.Printf("candidate: %s (%s)\n", c.Action, c.Reason)
	}
	id, err := eng.IngestPerformanceBreach(args[0], args[1], args[2], args[3], dur)
	if err != nil {
		return err
	}
	fmt.Println("event_id:", id)
	return nil
}

func runServe(eng *engine.Engine, args []string) error {
	addr := "127.0.0.1:8080"
	if len(args) > 0 {
		addr = args[0]
	}
	apiHandler := api.New(eng).Handler()
	uiHandler := ui.Handler()

	mux := http.NewServeMux()
	mux.Handle("/api/", apiHandler)
	mux.Handle("/", uiHandler)

	if !strings.HasPrefix(addr, ":") {
		fmt.Println("serving Mission Control + API on", addr)
	} else {
		fmt.Println("serving Mission Control on http://localhost" + addr)
		fmt.Println("API base: http://localhost" + addr + "/api")
	}
	return http.ListenAndServe(addr, mux)
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
