package apikeyconfig

import (
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

type ReconcileOptions struct {
	AssignBudgetGroups bool
}

type ReconcileStats struct {
	ExistingAPIKeys            int
	ExistingAPIKeyPolicies     int
	YAMLAPIKeys                int
	YAMLAPIKeyPolicies         int
	MergedAPIKeys              int
	MergedAPIKeyPolicies       int
	GroupAssignedPolicies      int
	TokenPackagePolicyCount    int
	DoubleGroupPolicyCount     int
	TripleGroupPolicyCount     int
	QuadGroupPolicyCount       int
	PreservedPGOnlyPolicyCount int
}

type budgetGroupTarget struct {
	GroupID         string
	DailyBudgetUSD  float64
	WeeklyBudgetUSD float64
	DailyTolerance  float64
	WeeklyTolerance float64
}

var defaultBudgetGroupTargets = []budgetGroupTarget{
	{GroupID: "double", DailyBudgetUSD: 150, WeeklyBudgetUSD: 500, DailyTolerance: 20, WeeklyTolerance: 50},
	{GroupID: "triple", DailyBudgetUSD: 100, WeeklyBudgetUSD: 300, DailyTolerance: 20, WeeklyTolerance: 50},
	{GroupID: "quad", DailyBudgetUSD: 60, WeeklyBudgetUSD: 250, DailyTolerance: 20, WeeklyTolerance: 50},
}

// ReconcileStates merges YAML-derived state into an existing Postgres state.
// YAML entries win on conflicts, while PG-only entries are preserved.
func ReconcileStates(existing State, fromYAML State, opts ReconcileOptions) (State, ReconcileStats) {
	existing = existing.Normalized()
	fromYAML = fromYAML.Normalized()
	stats := ReconcileStats{
		ExistingAPIKeys:        len(existing.Records),
		ExistingAPIKeyPolicies: len(existing.Records),
		YAMLAPIKeys:            len(fromYAML.Records),
		YAMLAPIKeyPolicies:     len(fromYAML.Records),
	}

	merged := State{Records: mergeRecords(fromYAML.Records, existing.Records, &stats)}
	if opts.AssignBudgetGroups {
		merged.Records = assignBudgetGroups(merged.Records, &stats)
	}
	merged = sanitizeState(merged)

	stats.MergedAPIKeys = len(merged.Records)
	stats.MergedAPIKeyPolicies = len(merged.Records)
	for _, entry := range merged.Records {
		if entry.Policy.TokenPackageUSD > 0 {
			stats.TokenPackagePolicyCount++
		}
	}
	return merged, stats
}

func mergeRecords(primary, secondary []Record, stats *ReconcileStats) []Record {
	seen := make(map[string]struct{}, len(primary)+len(secondary))
	out := make([]Record, 0, len(primary)+len(secondary))
	appendRecords := func(records []Record, preservedOnly bool) {
		for _, raw := range records {
			record := raw
			key := strings.TrimSpace(record.APIKey)
			if key == "" {
				continue
			}
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			out = append(out, record)
			if preservedOnly && stats != nil {
				stats.PreservedPGOnlyPolicyCount++
			}
		}
	}
	appendRecords(primary, false)
	appendRecords(secondary, true)
	return normalizeRecords(out)
}

func assignBudgetGroups(records []Record, stats *ReconcileStats) []Record {
	if len(records) == 0 {
		return nil
	}
	out := normalizeRecords(records)
	for i := range out {
		entry := &out[i].Policy
		if keepCustomGroup(entry.GroupID) {
			continue
		}
		target, ok := matchBudgetGroup(*entry)
		if !ok {
			continue
		}
		entry.GroupID = target.GroupID
		entry.DailyBudgetUSD = target.DailyBudgetUSD
		entry.WeeklyBudgetUSD = target.WeeklyBudgetUSD
		if stats != nil {
			stats.GroupAssignedPolicies++
			switch target.GroupID {
			case "double":
				stats.DoubleGroupPolicyCount++
			case "triple":
				stats.TripleGroupPolicyCount++
			case "quad":
				stats.QuadGroupPolicyCount++
			}
		}
	}
	return out
}

func keepCustomGroup(groupID string) bool {
	groupID = strings.TrimSpace(groupID)
	if groupID == "" {
		return false
	}
	for _, target := range defaultBudgetGroupTargets {
		if groupID == target.GroupID {
			return false
		}
	}
	return true
}

func matchBudgetGroup(entry config.APIKeyPolicy) (budgetGroupTarget, bool) {
	daily := entry.DailyBudgetUSD
	weekly := entry.WeeklyBudgetUSD
	if daily <= 0 || weekly <= 0 {
		return budgetGroupTarget{}, false
	}

	var (
		best      budgetGroupTarget
		bestScore float64
		found     bool
	)
	for _, target := range defaultBudgetGroupTargets {
		dailyDiff := absFloat(daily - target.DailyBudgetUSD)
		weeklyDiff := absFloat(weekly - target.WeeklyBudgetUSD)
		if dailyDiff > target.DailyTolerance || weeklyDiff > target.WeeklyTolerance {
			continue
		}
		score := dailyDiff + weeklyDiff
		if !found || score < bestScore {
			best = target
			bestScore = score
			found = true
		}
	}
	return best, found
}

func sanitizeState(state State) State {
	cfg := &config.Config{
		SDKConfig: config.SDKConfig{APIKeys: stateAPIKeys(state.Normalized().Records)},
		APIKeyPolicies: statePolicies(state.Normalized().Records),
	}
	cfg.SanitizeAPIKeyPolicies()
	return StateFromConfig(cfg)
}

func absFloat(value float64) float64 {
	if value < 0 {
		return -value
	}
	return value
}
