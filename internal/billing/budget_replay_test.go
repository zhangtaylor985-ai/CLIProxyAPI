package billing

import (
	"context"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

type budgetReplayEventStore struct {
	events []UsageEventRow
}

func (s budgetReplayEventStore) ListUsageEventsByAPIKey(_ context.Context, apiKey string, startAt, endAt time.Time, _ int, _ bool) ([]UsageEventRow, error) {
	result := make([]UsageEventRow, 0, len(s.events))
	for _, event := range s.events {
		if event.APIKey != apiKey {
			continue
		}
		ts := time.Unix(event.RequestedAt, 0)
		if !startAt.IsZero() && ts.Before(startAt) {
			continue
		}
		if !endAt.IsZero() && !ts.Before(endAt) {
			continue
		}
		result = append(result, event)
	}
	return result, nil
}

func TestComputeBudgetReplayState_MultipleTokenPackagesFIFO(t *testing.T) {
	start := time.Date(2026, 4, 21, 0, 0, 0, 0, time.UTC)
	second := time.Date(2026, 4, 28, 0, 0, 0, 0, time.UTC)
	asOf := second.Add(12 * time.Hour)
	p := &config.APIKeyPolicy{
		APIKey:          "k",
		DailyBudgetUSD:  10,
		WeeklyBudgetUSD: 0,
		TokenPackages: []config.TokenPackageEntry{
			{ID: "p1", StartedAt: start.Format(time.RFC3339), USD: 100},
			{ID: "p2", StartedAt: second.Format(time.RFC3339), USD: 100},
		},
	}
	store := budgetReplayEventStore{events: []UsageEventRow{
		{ID: 1, APIKey: "k", RequestedAt: start.Add(time.Hour).Unix(), CostMicroUSD: 60_000_000},
		{ID: 2, APIKey: "k", RequestedAt: second.Add(time.Hour).Unix(), CostMicroUSD: 80_000_000},
	}}

	state, allocations, err := ComputeBudgetReplayStateWithAllocations(context.Background(), store, "k", asOf, p)
	if err != nil {
		t.Fatalf("ComputeBudgetReplayStateWithAllocations: %v", err)
	}
	if len(state.Packages) != 2 {
		t.Fatalf("packages len=%d, want 2", len(state.Packages))
	}
	if got := state.Packages[0].UsedMicro; got != 100_000_000 {
		t.Fatalf("p1 used=%d, want 100000000", got)
	}
	if got := state.Packages[0].RemainingMicro; got != 0 {
		t.Fatalf("p1 remaining=%d, want 0", got)
	}
	if got := state.Packages[1].UsedMicro; got != 20_000_000 {
		t.Fatalf("p2 used=%d, want 20000000", got)
	}
	if got := state.Packages[1].RemainingMicro; got != 80_000_000 {
		t.Fatalf("p2 remaining=%d, want 80000000", got)
	}
	if got := state.PackageRemainingMicro; got != 80_000_000 {
		t.Fatalf("package remaining=%d, want 80000000", got)
	}
	if len(allocations) != 3 {
		t.Fatalf("allocations len=%d, want 3", len(allocations))
	}
	if allocations[1].PackageID != "p1" || allocations[1].CostMicroUSD != 50_000_000 {
		t.Fatalf("second allocation=%+v, want p1/50000000", allocations[1])
	}
	if allocations[2].PackageID != "p2" || allocations[2].CostMicroUSD != 20_000_000 {
		t.Fatalf("third allocation=%+v, want p2/20000000", allocations[2])
	}
}

func TestAPIKeyPolicy_TokenPackageEntriesMapsLegacySinglePackage(t *testing.T) {
	startedAt := "2026-04-21T00:00:00Z"
	p := &config.APIKeyPolicy{
		TokenPackageUSD:       100,
		TokenPackageStartedAt: startedAt,
	}
	entries := p.TokenPackageEntries()
	if len(entries) != 1 {
		t.Fatalf("entries len=%d, want 1", len(entries))
	}
	if entries[0].USD != 100 || entries[0].StartedAt != startedAt || entries[0].ID == "" {
		t.Fatalf("legacy entry=%+v", entries[0])
	}
}
