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
}

// ComputeBudgetReplayState replays usage events to derive current base-budget and token-package state.
func ComputeBudgetReplayState(ctx context.Context, store UsageEventReader, apiKey string, asOf time.Time, p *config.APIKeyPolicy) (BudgetReplayState, error) {
	state := BudgetReplayState{}
	if store == nil || p == nil || asOf.IsZero() {
		return state, nil
	}

	dayStart, dayEnd := dayBoundsAt(asOf)
	weekStart, weekEnd := p.WeeklyBudgetBounds(asOf)
	packageStart, packageEnabled := p.TokenPackageStartTime()
	weeklyAnchor, anchoredWeeklyBudget := policy.ParseHourlyAnchorRFC3339(p.WeeklyBudgetAnchorAt)
	packageBudgetMicro := int64(p.TokenPackageUSD*1_000_000 + 0.5)
	dailyBudgetMicro := int64(p.DailyBudgetUSD*1_000_000 + 0.5)
	weeklyBudgetMicro := int64(p.WeeklyBudgetUSD*1_000_000 + 0.5)

	startAt := weekStart
	if dayStart.Before(startAt) {
		startAt = dayStart
	}
	if packageEnabled && packageStart.Before(startAt) {
		startAt = packageStart
	}

	events, err := store.ListUsageEventsByAPIKey(ctx, apiKey, startAt, asOf.Add(time.Second), 0, false)
	if err != nil {
		return state, err
	}

	dayUsed := make(map[string]int64)
	weekUsed := make(map[int64]int64)
	for _, event := range events {
		ts := time.Unix(event.RequestedAt, 0)
		dayKey := policy.DayKeyChina(ts)
		evtWeekStart, _ := p.WeeklyBudgetBounds(ts)
		if anchoredWeeklyBudget {
			evtWeekStart, _ = policy.AnchoredWindowBoundsFloor(weeklyAnchor, ts, 7*24*time.Hour)
		}
		weekKey := evtWeekStart.Unix()

		baseCovered := event.CostMicroUSD
		if packageEnabled && !ts.Before(packageStart) {
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
			packageCovered := event.CostMicroUSD - baseCovered
			if packageCovered > 0 {
				state.PackageUsedMicro += packageCovered
			}
		}

		if baseCovered < 0 {
			baseCovered = 0
		}
		dayUsed[dayKey] += baseCovered
		weekUsed[weekKey] += baseCovered
	}

	state.DailyUsedMicro = dayUsed[policy.DayKeyChina(asOf)]
	state.WeeklyUsedMicro = weekUsed[weekStart.Unix()]
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
	if packageEnabled && packageBudgetMicro > 0 {
		state.PackageRemainingMicro = packageBudgetMicro - state.PackageUsedMicro
		if state.PackageRemainingMicro < 0 {
			state.PackageRemainingMicro = 0
		}
	}

	_ = dayEnd
	_ = weekEnd
	return state, nil
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
