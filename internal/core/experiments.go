package core

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

type ExplorationPolicy struct {
	ID       string `json:"id"`
	Order    string `json:"order"`
	MaxCalls int    `json:"max_calls"`
	MaxDepth int    `json:"max_depth"`
}
type DiscoveryNode struct {
	ID       string  `json:"id"`
	ParentID string  `json:"parent_id"`
	Score    float64 `json:"score"`
	Cost     float64 `json:"cost"`
	Text     string  `json:"text"`
	Reviewed bool    `json:"reviewed"`
}
type DiscoveryCase struct {
	ID    string          `json:"id"`
	Split string          `json:"split"`
	Nodes []DiscoveryNode `json:"nodes"`
}
type PolicyExperiment struct {
	ID          string              `json:"id"`
	Title       string              `json:"title"`
	CreatedAt   string              `json:"created_at"`
	Policies    []ExplorationPolicy `json:"policies"`
	Cases       []DiscoveryCase     `json:"cases"`
	CostPenalty float64             `json:"cost_penalty"`
	MinGain     float64             `json:"min_gain"`
}
type PolicyScore struct {
	PolicyID string  `json:"policy_id"`
	Split    string  `json:"split"`
	Cases    int     `json:"cases"`
	Quality  float64 `json:"quality"`
	Cost     float64 `json:"cost"`
	Calls    int     `json:"calls"`
	Reward   float64 `json:"reward"`
}
type PolicyEvaluation struct {
	Scores     []PolicyScore `json:"scores"`
	Candidate  string        `json:"candidate"`
	Promotable bool          `json:"promotable"`
	Reason     string        `json:"reason"`
}

// validateExperiment requires frozen finite outcomes and disjoint training and held-out cases.
func validateExperiment(experiment PolicyExperiment) error {
	if len(experiment.Policies) < 2 || len(experiment.Policies) > 12 || len(experiment.Cases) > 200 || experiment.CostPenalty < 0 || experiment.MinGain < 0 || math.IsNaN(experiment.MinGain) {
		return errors.New("Provide 2–12 policies and up to 200 frozen cases with nonnegative evaluation settings")
	}
	policies := map[string]bool{}
	for _, p := range experiment.Policies {
		if p.ID == "" || policies[p.ID] || !contains([]string{"breadth", "depth", "best_parent"}, p.Order) || p.MaxCalls < 1 || p.MaxCalls > 100 || p.MaxDepth < 1 || p.MaxDepth > 100 {
			return errors.New("Invalid exploration policy")
		}
		policies[p.ID] = true
	}
	cases := map[string]bool{}
	for _, c := range experiment.Cases {
		if c.ID == "" || cases[c.ID] || !contains([]string{"train", "holdout"}, c.Split) || len(c.Nodes) == 0 || len(c.Nodes) > 1000 {
			return errors.New("Case IDs must be unique and split into train or holdout")
		}
		cases[c.ID] = true
		nodes := map[string]DiscoveryNode{}
		for _, n := range c.Nodes {
			if !n.Reviewed || n.ID == "" || n.Score < 0 || n.Score > 1 || n.Cost < 0 || math.IsNaN(n.Score) || math.IsNaN(n.Cost) {
				return errors.New("Node quality must be an author-reviewed score between 0 and 1")
			}
			if _, ok := nodes[n.ID]; ok {
				return errors.New("Duplicate discovery node")
			}
			nodes[n.ID] = n
		}
		for _, n := range c.Nodes {
			seen := map[string]bool{n.ID: true}
			parent := n.ParentID
			for parent != "" {
				p, ok := nodes[parent]
				if !ok || seen[parent] {
					return errors.New("Discovery tree has a missing parent or cycle")
				}
				seen[parent] = true
				parent = p.ParentID
			}
		}
	}
	return nil
}

// replayPolicy reveals recorded outcomes only after their parent is revealed, without using hidden node scores.
func replayPolicy(policy ExplorationPolicy, c DiscoveryCase) (float64, float64, int) {
	seen := map[string]DiscoveryNode{}
	depth := map[string]int{}
	best, cost := 0.0, 0.0
	calls := 0
	for calls < policy.MaxCalls {
		legal := []DiscoveryNode{}
		for _, n := range c.Nodes {
			if _, ok := seen[n.ID]; ok {
				continue
			}
			if n.ParentID != "" {
				if _, ok := seen[n.ParentID]; !ok {
					continue
				}
			}
			if depth[n.ParentID]+1 <= policy.MaxDepth {
				legal = append(legal, n)
			}
		}
		if len(legal) == 0 {
			break
		}
		sort.SliceStable(legal, func(i, j int) bool {
			a, b := legal[i], legal[j]
			switch policy.Order {
			case "depth":
				if depth[a.ParentID] != depth[b.ParentID] {
					return depth[a.ParentID] > depth[b.ParentID]
				}
			case "best_parent":
				if seen[a.ParentID].Score != seen[b.ParentID].Score {
					return seen[a.ParentID].Score > seen[b.ParentID].Score
				}
			default:
				if depth[a.ParentID] != depth[b.ParentID] {
					return depth[a.ParentID] < depth[b.ParentID]
				}
			}
			return a.ID < b.ID
		})
		chosen := legal[0]
		seen[chosen.ID] = chosen
		depth[chosen.ID] = depth[chosen.ParentID] + 1
		best = math.Max(best, chosen.Score)
		cost += chosen.Cost
		calls++
	}
	return best, cost, calls
}

// evaluatePolicies selects with training cases, then independently checks gain and costs on held-out cases.
func evaluatePolicies(experiment PolicyExperiment) (PolicyEvaluation, error) {
	result := PolicyEvaluation{Scores: []PolicyScore{}}
	if err := validateExperiment(experiment); err != nil {
		return result, err
	}
	for _, p := range experiment.Policies {
		for _, split := range []string{"train", "holdout"} {
			score := PolicyScore{PolicyID: p.ID, Split: split}
			for _, c := range experiment.Cases {
				if c.Split != split {
					continue
				}
				q, cost, calls := replayPolicy(p, c)
				score.Cases++
				score.Quality += q
				score.Cost += cost
				score.Calls += calls
			}
			if score.Cases > 0 {
				score.Quality /= float64(score.Cases)
				score.Cost /= float64(score.Cases)
				score.Reward = score.Quality - experiment.CostPenalty*score.Cost
			}
			result.Scores = append(result.Scores, score)
		}
	}
	baseline := experiment.Policies[0].ID
	candidate := baseline
	best := -math.MaxFloat64
	for _, s := range result.Scores {
		if s.Split == "train" && s.Cases > 0 && s.Reward > best {
			best = s.Reward
			candidate = s.PolicyID
		}
	}
	result.Candidate = candidate
	scores := map[string]PolicyScore{}
	for _, s := range result.Scores {
		scores[s.PolicyID+":"+s.Split] = s
	}
	training, held := scores[candidate+":train"], scores[candidate+":holdout"]
	baseTrain, baseHold := scores[baseline+":train"], scores[baseline+":holdout"]
	result.Reason = "Research result only; no production prompt or model is changed."
	if training.Cases < 5 || held.Cases < 5 {
		result.Reason = "At least 5 training and 5 held-out cases are required; no promotion evidence."
		return result, nil
	}
	gainTrain, gainHold := training.Reward-baseTrain.Reward, held.Reward-baseHold.Reward
	result.Promotable = candidate != baseline && gainTrain > experiment.MinGain && gainHold > experiment.MinGain && held.Quality >= baseHold.Quality
	result.Reason = fmt.Sprintf("Candidate selected on training only. Train gain %.4f; held-out gain %.4f. Threshold %.4f. Author review remains required; replay cannot evaluate unobserved branches.", gainTrain, gainHold, experiment.MinGain)
	return result, nil
}

// experimentCommand stores and evaluates bounded per-story research artifacts without calling a model.
func (engine *Engine) experimentCommand(command string, args *arguments) (any, error) {
	projectID := args.optionalString("project_id")
	if _, err := engine.store.Get(projectID); err != nil {
		return nil, err
	}
	if command == "list_experiments" {
		rows, err := engine.store.db.Query("SELECT document FROM experiments WHERE project_id=? ORDER BY rowid DESC LIMIT 50", projectID)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		result := []PolicyExperiment{}
		for rows.Next() {
			var raw string
			if err := rows.Scan(&raw); err != nil {
				return nil, err
			}
			var v PolicyExperiment
			if err := decodeJSON([]byte(raw), &v); err != nil {
				return nil, err
			}
			result = append(result, v)
		}
		return result, rows.Err()
	}
	var experiment PolicyExperiment
	if err := decodeJSON(args.fields["experiment"], &experiment); err != nil {
		return nil, err
	}
	evaluation, err := evaluatePolicies(experiment)
	if err != nil {
		return nil, err
	}
	if command == "record_experiment" {
		experiment.ID = newID()
		experiment.CreatedAt = now()
		if _, err := engine.store.db.Exec("INSERT INTO experiments(id,project_id,document) VALUES(?,?,?)", experiment.ID, projectID, jsonText(experiment)); err != nil {
			return nil, err
		}
	}
	return map[string]any{"experiment": experiment, "evaluation": evaluation}, nil
}
