package billing

import (
	"context"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/policy"
)

type BudgetReplayState struct {
	DailyUsedMicro        int64
	WeeklyUsedMicro       int64
	DailyRemainingMicro   int64
	WeeklyRemainingMicro  int64
	BaseAvailableMicro    int64
	PackageUsedMicro      int64
	PackageRemainingMicro int64
	Packages              []TokenPackageReplayState
}

type TokenPackageReplayState struct {
	ID             string
	StartedAt      time.Time
	TotalMicro     int64
	UsedMicro      int64
	RemainingMicro int64
	Active         bool
	Note           string
}

type TokenPackageUsageAllocation struct {
	UsageEventID     int64
	RequestedAt      int64
	PackageID        string
	PackageStartedAt time.Time
	CostMicroUSD     int64
}

// ComputeBudgetReplayState replays usage events to derive current base-budget and token-package state.
func ComputeBudgetReplayState(ctx context.Context, store UsageEventReader, apiKey string, asOf time.Time, p *config.APIKeyPolicy) (BudgetReplayState, error) {
	state, _, err := computeBudgetReplayState(ctx, store, apiKey, asOf, p)
	return state, err
}

// ComputeBudgetReplayStateWithAllocations returns the replay state plus recent
// package allocation details, useful for management views.
func ComputeBudgetReplayStateWithAllocations(ctx context.Context, store UsageEventReader, apiKey string, asOf time.Time, p *config.APIKeyPolicy) (BudgetReplayState, []TokenPackageUsageAllocation, error) {
	return computeBudgetReplayState(ctx, store, apiKey, asOf, p)
}

func computeBudgetReplayState(ctx context.Context, store UsageEventReader, apiKey string, asOf time.Time, p *config.APIKeyPolicy) (BudgetReplayState, []TokenPackageUsageAllocation, error) {
	state := BudgetReplayState{}
	if p == nil || asOf.IsZero() {
		return state, nil, nil
	}

	dayStart, dayEnd := dayBoundsAt(asOf)
	packages := buildReplayPackages(p.TokenPackageEntries(), asOf)
	packageEnabled := len(packages) > 0
	dailyBudgetMicro := int64(p.DailyBudgetUSD*1_000_000 + 0.5)
	weeklyBudgetMicro := int64(p.WeeklyBudgetUSD*1_000_000 + 0.5)
	if store == nil {
		if dailyBudgetMicro > 0 {
			state.DailyRemainingMicro = dailyBudgetMicro
			state.BaseAvailableMicro = dailyBudgetMicro
		}
		if weeklyBudgetMicro > 0 {
			state.WeeklyRemainingMicro = weeklyBudgetMicro
			if state.BaseAvailableMicro == 0 || weeklyBudgetMicro < state.BaseAvailableMicro {
				state.BaseAvailableMicro = weeklyBudgetMicro
			}
		}
		state.Packages = packages
		for _, pkg := range packages {
			if !pkg.StartedAt.After(asOf) {
				state.PackageRemainingMicro += pkg.RemainingMicro
			}
		}
		return state, nil, nil
	}

	weekStart := time.Time{}
	weekEnd := time.Time{}
	weeklyAnchor := time.Time{}
	if weeklyBudgetMicro > 0 {
		var err error
		weekStart, weekEnd, err = p.WeeklyBudgetBounds(asOf)
		if err != nil {
			return state, nil, err
		}
		weeklyAnchor, err = p.WeeklyBudgetAnchorTime()
		if err != nil {
			return state, nil, err
		}
	}

	startAt := dayStart
	if !weekStart.IsZero() && weekStart.Before(startAt) {
		startAt = weekStart
	}
	if packageEnabled && packages[0].StartedAt.Before(startAt) {
		startAt = packages[0].StartedAt
	}

	events, err := store.ListUsageEventsByAPIKey(ctx, apiKey, startAt, asOf.Add(time.Second), 0, false)
	if err != nil {
		return state, nil, err
	}

	dayUsed := make(map[string]int64)
	weekUsed := make(map[int64]int64)
	allocations := make([]TokenPackageUsageAllocation, 0)
	for _, event := range events {
		ts := time.Unix(event.RequestedAt, 0)
		dayKey := policy.DayKeyChina(ts)
		weekKey := int64(0)
		if weeklyBudgetMicro > 0 {
			evtWeekStart, _ := policy.AnchoredWindowBoundsFloor(weeklyAnchor, ts, 7*24*time.Hour)
			weekKey = evtWeekStart.Unix()
		}

		baseCovered := event.CostMicroUSD
		if packageEnabled {
			baseCovered = event.CostMicroUSD
			hasBaseLimit := false
			if dailyBudgetMicro > 0 {
				hasBaseLimit = true
				dayRemaining := dailyBudgetMicro - dayUsed[dayKey]
				if dayRemaining < 0 {
					dayRemaining = 0
				}
				if baseCovered > dayRemaining {
					baseCovered = dayRemaining
				}
			}
			if weeklyBudgetMicro > 0 {
				hasBaseLimit = true
				weekRemaining := weeklyBudgetMicro - weekUsed[weekKey]
				if weekRemaining < 0 {
					weekRemaining = 0
				}
				if baseCovered > weekRemaining {
					baseCovered = weekRemaining
				}
			}
			if !hasBaseLimit {
				baseCovered = 0
			}
			packageNeeded := event.CostMicroUSD - baseCovered
			allocations = append(allocations, allocatePackageUsage(packages, event, ts, packageNeeded)...)
		}

		if baseCovered < 0 {
			baseCovered = 0
		}
		dayUsed[dayKey] += baseCovered
		weekUsed[weekKey] += baseCovered
	}

	state.DailyUsedMicro = dayUsed[policy.DayKeyChina(asOf)]
	if weeklyBudgetMicro > 0 {
		state.WeeklyUsedMicro = weekUsed[weekStart.Unix()]
	}
	if dailyBudgetMicro > 0 {
		state.DailyRemainingMicro = dailyBudgetMicro - state.DailyUsedMicro
		if state.DailyRemainingMicro < 0 {
			state.DailyRemainingMicro = 0
		}
	}
	if weeklyBudgetMicro > 0 {
		state.WeeklyRemainingMicro = weeklyBudgetMicro - state.WeeklyUsedMicro
		if state.WeeklyRemainingMicro < 0 {
			state.WeeklyRemainingMicro = 0
		}
	}
	switch {
	case dailyBudgetMicro > 0 && weeklyBudgetMicro > 0:
		state.BaseAvailableMicro = min64(state.DailyRemainingMicro, state.WeeklyRemainingMicro)
	case dailyBudgetMicro > 0:
		state.BaseAvailableMicro = state.DailyRemainingMicro
	case weeklyBudgetMicro > 0:
		state.BaseAvailableMicro = state.WeeklyRemainingMicro
	}
	if packageEnabled {
		for _, pkg := range packages {
			state.PackageUsedMicro += pkg.UsedMicro
			if !pkg.StartedAt.After(asOf) {
				state.PackageRemainingMicro += pkg.RemainingMicro
			}
		}
	}
	state.Packages = packages

	_ = dayEnd
	_ = weekEnd
	return state, allocations, nil
}

func buildReplayPackages(entries []config.TokenPackageEntry, asOf time.Time) []TokenPackageReplayState {
	packages := make([]TokenPackageReplayState, 0, len(entries))
	for _, entry := range entries {
		startedAt, err := time.Parse(time.RFC3339, entry.StartedAt)
		if err != nil || entry.USD <= 0 {
			continue
		}
		totalMicro := int64(entry.USD*1_000_000 + 0.5)
		if totalMicro <= 0 {
			continue
		}
		packages = append(packages, TokenPackageReplayState{
			ID:             entry.ID,
			StartedAt:      startedAt,
			TotalMicro:     totalMicro,
			RemainingMicro: totalMicro,
			Active:         !startedAt.After(asOf),
			Note:           entry.Note,
		})
	}
	return packages
}

func allocatePackageUsage(packages []TokenPackageReplayState, event UsageEventRow, requestedAt time.Time, neededMicro int64) []TokenPackageUsageAllocation {
	if neededMicro <= 0 {
		return nil
	}
	allocations := make([]TokenPackageUsageAllocation, 0, 1)
	for i := range packages {
		if neededMicro <= 0 {
			break
		}
		if packages[i].StartedAt.After(requestedAt) || packages[i].RemainingMicro <= 0 {
			continue
		}
		covered := min64(neededMicro, packages[i].RemainingMicro)
		packages[i].UsedMicro += covered
		packages[i].RemainingMicro -= covered
		packages[i].Active = packages[i].RemainingMicro > 0
		neededMicro -= covered
		allocations = append(allocations, TokenPackageUsageAllocation{
			UsageEventID:     event.ID,
			RequestedAt:      event.RequestedAt,
			PackageID:        packages[i].ID,
			PackageStartedAt: packages[i].StartedAt,
			CostMicroUSD:     covered,
		})
	}
	return allocations
}

func dayBoundsAt(now time.Time) (time.Time, time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	local := now.In(policy.ChinaLocation())
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, policy.ChinaLocation())
	return start, start.Add(24 * time.Hour)
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
