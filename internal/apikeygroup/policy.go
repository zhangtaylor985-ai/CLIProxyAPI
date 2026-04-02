package apikeygroup

import (
	"context"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
)

// ApplyGroupBudget overlays daily/weekly budgets from the bound group onto the policy copy.
func ApplyGroupBudget(ctx context.Context, store Store, p *config.APIKeyPolicy) (*config.APIKeyPolicy, *Group, error) {
	if p == nil {
		return nil, nil, nil
	}
	cloned := *p
	groupID := strings.TrimSpace(cloned.GroupID)
	if groupID == "" || store == nil {
		return &cloned, nil, nil
	}
	group, ok, err := store.GetGroup(ctx, groupID)
	if err != nil {
		return nil, nil, err
	}
	if !ok {
		return &cloned, nil, nil
	}
	cloned.DailyBudgetUSD = float64(group.DailyBudgetMicroUSD) / 1_000_000
	cloned.WeeklyBudgetUSD = float64(group.WeeklyBudgetMicroUSD) / 1_000_000
	return &cloned, &group, nil
}
