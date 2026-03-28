package management

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	log "github.com/sirupsen/logrus"
)

func (h *Handler) GetAPIKeyPolicies(c *gin.Context) {
	policies := []config.APIKeyPolicy(nil)
	if h != nil && h.cfg != nil {
		policies = append([]config.APIKeyPolicy(nil), h.cfg.APIKeyPolicies...)
	}
	c.JSON(http.StatusOK, gin.H{"api-key-policies": policies})
}

func (h *Handler) PutAPIKeyPolicies(c *gin.Context) {
	if h == nil || h.cfg == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "config unavailable"})
		return
	}

	data, err := c.GetRawData()
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "failed to read body"})
		return
	}

	var arr []config.APIKeyPolicy
	if err = json.Unmarshal(data, &arr); err != nil {
		var obj struct {
			Items []config.APIKeyPolicy `json:"items"`
		}
		if err2 := json.Unmarshal(data, &obj); err2 != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
			return
		}
		arr = obj.Items
	}

	h.cfg.APIKeyPolicies = append([]config.APIKeyPolicy(nil), arr...)
	h.cfg.SanitizeAPIKeyPolicies()
	h.persist(c)
}

func (h *Handler) PatchAPIKeyPolicies(c *gin.Context) {
	if h == nil || h.cfg == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "config unavailable"})
		return
	}

	type modelRoutingPatch struct {
		Rules *[]config.ModelRoutingRule `json:"rules"`
	}
	type providerFailoverPatch struct {
		Enabled     *bool                       `json:"enabled"`
		TargetModel *string                     `json:"target-model"`
		Rules       *[]config.ModelFailoverRule `json:"rules"`
	}
	type failoverPatch struct {
		Claude *providerFailoverPatch `json:"claude"`
	}
	type policyPatch struct {
		FastMode              *bool              `json:"fast-mode"`
		EnableClaudeModels    *bool              `json:"enable-claude-models"`
		ClaudeUsageLimitUSD   *float64           `json:"claude-usage-limit-usd"`
		ClaudeGPTTargetFamily *string            `json:"claude-gpt-target-family"`
		EnableClaudeOpus1M    *bool              `json:"enable-claude-opus-1m"`
		ModelRouting          *modelRoutingPatch `json:"model-routing"`
		UpstreamBaseURL       *string            `json:"upstream-base-url"`
		ExcludedModels        *[]string          `json:"excluded-models"`
		AllowClaudeOpus46     *bool              `json:"allow-claude-opus-4-6"`
		DailyLimits           *map[string]int    `json:"daily-limits"`
		DailyBudgetUSD        *float64           `json:"daily-budget-usd"`
		WeeklyBudgetUSD       *float64           `json:"weekly-budget-usd"`
		WeeklyBudgetAnchorAt  *string            `json:"weekly-budget-anchor-at"`
		TokenPackageUSD       *float64           `json:"token-package-usd"`
		TokenPackageStartedAt *string            `json:"token-package-started-at"`
		Failover              *failoverPatch     `json:"failover"`
		APIKey                *string            `json:"api-key"`
	}
	var body struct {
		APIKey string       `json:"api-key"`
		Value  *policyPatch `json:"value"`
	}
	if err := c.ShouldBindJSON(&body); err != nil || body.Value == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}

	apiKey := strings.TrimSpace(body.APIKey)
	if apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "api-key is required"})
		return
	}

	targetIndex := -1
	for i := range h.cfg.APIKeyPolicies {
		if strings.TrimSpace(h.cfg.APIKeyPolicies[i].APIKey) == apiKey {
			targetIndex = i
			break
		}
	}

	entry := config.APIKeyPolicy{APIKey: apiKey}
	if targetIndex >= 0 {
		entry = h.cfg.APIKeyPolicies[targetIndex]
	}

	if body.Value.APIKey != nil {
		trimmed := strings.TrimSpace(*body.Value.APIKey)
		if trimmed == "" {
			if targetIndex >= 0 {
				h.cfg.APIKeyPolicies = append(h.cfg.APIKeyPolicies[:targetIndex], h.cfg.APIKeyPolicies[targetIndex+1:]...)
				h.cfg.SanitizeAPIKeyPolicies()
				h.persist(c)
				return
			}
			c.JSON(http.StatusBadRequest, gin.H{"error": "api-key cannot be empty"})
			return
		}
		entry.APIKey = trimmed
	}

	if body.Value.EnableClaudeModels != nil {
		v := *body.Value.EnableClaudeModels
		entry.EnableClaudeModels = &v
	}
	if body.Value.ClaudeUsageLimitUSD != nil {
		entry.ClaudeUsageLimitUSD = *body.Value.ClaudeUsageLimitUSD
	}
	if body.Value.ClaudeGPTTargetFamily != nil {
		entry.ClaudeGPTTargetFamily = strings.TrimSpace(*body.Value.ClaudeGPTTargetFamily)
	}
	if body.Value.FastMode != nil {
		entry.FastMode = *body.Value.FastMode
	}
	if body.Value.EnableClaudeOpus1M != nil {
		v := *body.Value.EnableClaudeOpus1M
		entry.EnableClaudeOpus1M = &v
	}
	if body.Value.ExcludedModels != nil {
		entry.ExcludedModels = config.NormalizeExcludedModels(*body.Value.ExcludedModels)
	}
	if body.Value.UpstreamBaseURL != nil {
		entry.UpstreamBaseURL = strings.TrimSpace(*body.Value.UpstreamBaseURL)
	}
	if body.Value.ModelRouting != nil && body.Value.ModelRouting.Rules != nil {
		entry.ModelRouting.Rules = append([]config.ModelRoutingRule(nil), (*body.Value.ModelRouting.Rules)...)
	}
	if body.Value.AllowClaudeOpus46 != nil {
		v := *body.Value.AllowClaudeOpus46
		entry.AllowClaudeOpus46 = &v
	}
	if body.Value.DailyLimits != nil {
		entry.DailyLimits = *body.Value.DailyLimits
	}
	if body.Value.DailyBudgetUSD != nil {
		entry.DailyBudgetUSD = *body.Value.DailyBudgetUSD
	}
	if body.Value.WeeklyBudgetUSD != nil {
		entry.WeeklyBudgetUSD = *body.Value.WeeklyBudgetUSD
	}
	if body.Value.WeeklyBudgetAnchorAt != nil {
		entry.WeeklyBudgetAnchorAt = strings.TrimSpace(*body.Value.WeeklyBudgetAnchorAt)
	}
	if body.Value.TokenPackageUSD != nil {
		entry.TokenPackageUSD = *body.Value.TokenPackageUSD
	}
	if body.Value.TokenPackageStartedAt != nil {
		entry.TokenPackageStartedAt = strings.TrimSpace(*body.Value.TokenPackageStartedAt)
	}
	if body.Value.Failover != nil && body.Value.Failover.Claude != nil {
		ruleCount := -1
		if body.Value.Failover.Claude.Rules != nil {
			ruleCount = len(*body.Value.Failover.Claude.Rules)
		}
		log.WithFields(log.Fields{
			"api_key":           apiKey,
			"enabled_present":   body.Value.Failover.Claude.Enabled != nil,
			"enabled_value":     body.Value.Failover.Claude.Enabled,
			"target_present":    body.Value.Failover.Claude.TargetModel != nil,
			"rules_present":     body.Value.Failover.Claude.Rules != nil,
			"rules_count":       ruleCount,
			"existing_enabled":  entry.Failover.Claude.Enabled,
			"existing_target":   entry.Failover.Claude.TargetModel,
			"existing_rule_len": len(entry.Failover.Claude.Rules),
		}).Info("management api-key-policies patch: received claude failover block")

		// Treat a provided provider block as replacement instead of field-merge,
		// so omitted nested fields like enabled=false do not preserve stale values.
		entry.Failover.Claude = config.ProviderFailoverPolicy{}
		if body.Value.Failover.Claude.Enabled != nil {
			entry.Failover.Claude.Enabled = *body.Value.Failover.Claude.Enabled
		}
		if body.Value.Failover.Claude.TargetModel != nil {
			entry.Failover.Claude.TargetModel = strings.TrimSpace(*body.Value.Failover.Claude.TargetModel)
		}
		if body.Value.Failover.Claude.Rules != nil {
			entry.Failover.Claude.Rules = append([]config.ModelFailoverRule(nil), (*body.Value.Failover.Claude.Rules)...)
		}
		log.WithFields(log.Fields{
			"api_key":        apiKey,
			"final_enabled":  entry.Failover.Claude.Enabled,
			"final_target":   entry.Failover.Claude.TargetModel,
			"final_rule_len": len(entry.Failover.Claude.Rules),
		}).Info("management api-key-policies patch: applied claude failover block")
	}

	if targetIndex >= 0 {
		h.cfg.APIKeyPolicies[targetIndex] = entry
	} else {
		h.cfg.APIKeyPolicies = append(h.cfg.APIKeyPolicies, entry)
	}
	h.cfg.SanitizeAPIKeyPolicies()
	h.persist(c)
}

func (h *Handler) DeleteAPIKeyPolicies(c *gin.Context) {
	if h == nil || h.cfg == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "config unavailable"})
		return
	}

	apiKey := strings.TrimSpace(c.Query("api-key"))
	if apiKey == "" {
		apiKey = strings.TrimSpace(c.Query("apiKey"))
	}
	if apiKey != "" {
		out := make([]config.APIKeyPolicy, 0, len(h.cfg.APIKeyPolicies))
		for _, v := range h.cfg.APIKeyPolicies {
			if strings.TrimSpace(v.APIKey) == apiKey {
				continue
			}
			out = append(out, v)
		}
		if len(out) == len(h.cfg.APIKeyPolicies) {
			c.JSON(http.StatusNotFound, gin.H{"error": "item not found"})
			return
		}
		h.cfg.APIKeyPolicies = out
		h.cfg.SanitizeAPIKeyPolicies()
		h.persist(c)
		return
	}

	c.JSON(http.StatusBadRequest, gin.H{"error": "missing api-key"})
}
