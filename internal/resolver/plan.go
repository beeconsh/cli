package resolver

import (
	"fmt"
	"sort"
	"strings"

	"github.com/terracotta-ai/beecon/internal/ir"
	"github.com/terracotta-ai/beecon/internal/security"
	"github.com/terracotta-ai/beecon/internal/state"
)

// Plan is an ordered, executable set of actions.
type Plan struct {
	Actions []*state.PlanAction `json:"actions"`
}

// BuildPlan builds a topologically sorted execution plan from graph and existing state.
func BuildPlan(g *ir.Graph, st *state.State) (*Plan, error) {
	orderedIDs, dependsOn, err := orderByDependencies(g)
	if err != nil {
		return nil, err
	}
	nodeByID := g.NodesByID()
	managed := managedByNodeID(st)
	intentIDs := map[string]bool{}
	actions := make([]*state.PlanAction, 0, len(orderedIDs)+len(managed))

	for _, id := range orderedIDs {
		intentIDs[id] = true
		n := nodeByID[id]
		nodeIntent := n.Snapshot()
		rec, ok := managed[id]
		if !ok {
			actions = append(actions, &state.PlanAction{
				ID:        state.NewID("act"),
				NodeID:    id,
				NodeType:  string(n.Type),
				NodeName:  n.Name,
				Operation: "CREATE",
				DependsOn: dependsOn[id],
				Reasoning: "intent exists with no live state",
			})
			continue
		}
		newHash := state.HashMap(nodeIntent)
		if newHash != rec.IntentHash || rec.Status == state.StatusDrifted {
			changes := diffIntent(rec.IntentSnapshot, nodeIntent)
			if len(changes) == 0 && rec.Status == state.StatusDrifted {
				// Performance breach set DRIFTED without changing intent — skip phantom UPDATE
				continue
			}
			actions = append(actions, &state.PlanAction{
				ID:        state.NewID("act"),
				NodeID:    id,
				NodeType:  string(n.Type),
				NodeName:  n.Name,
				Operation: "UPDATE",
				DependsOn: dependsOn[id],
				Reasoning: "intent differs from live state",
				Changes:   changes,
			})
		}
	}

	// Managed resources removed from intent become deletes.
	for nodeID, rec := range managed {
		if intentIDs[nodeID] {
			continue
		}
		actions = append(actions, &state.PlanAction{
			ID:        state.NewID("act"),
			NodeID:    nodeID,
			NodeType:  rec.NodeType,
			NodeName:  rec.NodeName,
			Operation: "DELETE",
			Reasoning: "live managed resource has no intent",
		})
	}

	// Preserve dependency order for create/update actions exactly as emitted by the
	// topological walk. DELETE actions are pushed to the end and ordered by reverse
	// type precedence so dependents (services) are removed before dependencies (stores, networks).
	pos := map[string]int{}
	for i, id := range orderedIDs {
		pos[id] = i
	}
	sort.SliceStable(actions, func(i, j int) bool {
		di := actions[i].Operation == "DELETE"
		dj := actions[j].Operation == "DELETE"
		if di != dj {
			return !di
		}
		if !di {
			return pos[actions[i].NodeID] < pos[actions[j].NodeID]
		}
		// For deletes: reverse type precedence (services before stores before networks)
		pi := deletePrecedence(actions[i].NodeType)
		pj := deletePrecedence(actions[j].NodeType)
		if pi != pj {
			return pi < pj
		}
		return actions[i].NodeID < actions[j].NodeID
	})

	return &Plan{Actions: actions}, nil
}

func FormatPlan(p *Plan) string {
	var b strings.Builder
	for i, a := range p.Actions {
		line := fmt.Sprintf("%d. %s %s", i+1, a.Operation, a.NodeID)
		if len(a.DependsOn) > 0 {
			line += fmt.Sprintf(" (depends_on: %s)", strings.Join(a.DependsOn, ", "))
		}
		if a.RequiresApproval {
			line += fmt.Sprintf(" [approval:%s]", a.BoundaryTag)
		}
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

func orderByDependencies(g *ir.Graph) ([]string, map[string][]string, error) {
	nodeByID := g.NodesByID()
	incoming := map[string]int{}
	adj := map[string][]string{}
	depends := map[string][]string{}
	for _, n := range g.Nodes {
		incoming[n.ID] = 0
	}
	for _, e := range g.Edges {
		adj[e.From] = append(adj[e.From], e.To)
		incoming[e.To]++
		depends[e.To] = append(depends[e.To], e.From)
	}
	for id := range incoming {
		sort.Strings(depends[id])
	}
	queue := make([]string, 0)
	for id, c := range incoming {
		if c == 0 {
			queue = append(queue, id)
		}
	}
	sort.Slice(queue, func(i, j int) bool {
		pi := precedence(nodeByID[queue[i]].Type)
		pj := precedence(nodeByID[queue[j]].Type)
		if pi != pj {
			return pi < pj
		}
		return queue[i] < queue[j]
	})

	out := make([]string, 0, len(incoming))
	for len(queue) > 0 {
		id := queue[0]
		queue = queue[1:]
		out = append(out, id)
		for _, nxt := range adj[id] {
			incoming[nxt]--
			if incoming[nxt] == 0 {
				queue = append(queue, nxt)
				sort.Slice(queue, func(i, j int) bool {
					pi := precedence(nodeByID[queue[i]].Type)
					pj := precedence(nodeByID[queue[j]].Type)
					if pi != pj {
						return pi < pj
					}
					return queue[i] < queue[j]
				})
			}
		}
	}
	if len(out) != len(incoming) {
		return nil, nil, fmt.Errorf("dependency cycle detected")
	}
	return out, depends, nil
}

func deletePrecedence(nodeType string) int {
	switch ir.NodeType(nodeType) {
	case ir.NodeService:
		return 1
	case ir.NodeCompute:
		return 2
	case ir.NodeStore:
		return 3
	case ir.NodeNetwork:
		return 4
	default:
		return 0
	}
}

func precedence(t ir.NodeType) int {
	switch t {
	case ir.NodeNetwork:
		return 1
	case ir.NodeStore:
		return 2
	case ir.NodeCompute:
		return 3
	case ir.NodeService:
		return 4
	default:
		return 9
	}
}

func managedByNodeID(st *state.State) map[string]*state.ResourceRecord {
	out := map[string]*state.ResourceRecord{}
	for _, r := range st.Resources {
		if r.Managed {
			out[r.ResourceID] = r
		}
	}
	return out
}

func diffIntent(old, new map[string]interface{}) map[string]string {
	changes := map[string]string{}
	for k, n := range new {
		if o, ok := old[k]; !ok || fmt.Sprint(o) != fmt.Sprint(n) {
			if security.IsSensitiveKey(k) {
				changes[k] = "**REDACTED** -> **REDACTED**"
			} else {
				changes[k] = fmt.Sprintf("%v -> %v", o, n)
			}
		}
	}
	for k, o := range old {
		if _, ok := new[k]; !ok {
			if security.IsSensitiveKey(k) {
				changes[k] = "**REDACTED** -> <deleted>"
			} else {
				changes[k] = fmt.Sprintf("%v -> <deleted>", o)
			}
		}
	}
	return changes
}
