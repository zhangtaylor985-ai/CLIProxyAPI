package middleware

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/apikeygroup"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/billing"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/policy"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/requesttrace"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

const (
	apiKeyPolicyContextKey = "apiKeyPolicy"
	claudeOpus1MHeaderName = "X-CPA-CLAUDE-1M"
	claudeOpus1MBetaName   = "context-1m-2025-08-07"
)

func modelSupportsPriorityServiceTier(model string) bool {
	key := policy.NormaliseModelKey(model)
	switch {
	case strings.HasPrefix(key, "gpt-"):
		return true
	case strings.HasPrefix(key, "chatgpt-"):
		return true
	case strings.HasPrefix(key, "o1"):
		return true
	case strings.HasPrefix(key, "o3"):
		return true
	case strings.HasPrefix(key, "o4"):
		return true
	default:
		return false
	}
}

type priceResolver interface {
	ResolvePriceMicro(ctx context.Context, model string) (billing.PriceMicroUSDPer1M, string, int64, error)
}

type tokenPackageBudgetState struct {
	bypassBudgets         bool
	postPackageBudgetFrom time.Time
}

// APIKeyPolicyMiddleware enforces per-client API key restrictions and quotas.
// It assumes AuthMiddleware already stored the authenticated key as gin context value "apiKey".
func APIKeyPolicyMiddleware(getConfig func() *config.Config, limiter policy.DailyLimiter, costReader billing.DailyCostReader, groupStore apikeygroup.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c == nil || c.Request == nil {
			return
		}
		cfg := (*config.Config)(nil)
		if getConfig != nil {
			cfg = getConfig()
		}
		if cfg == nil {
			c.Next()
			return
		}

		apiKey := strings.TrimSpace(c.GetString("apiKey"))
		if apiKey == "" {
			c.Next()
			return
		}
		allowClaudeOpus1M := cfg.AllowsClaudeOpus1M(apiKey)
		policyEntry := cfg.EffectiveAPIKeyPolicy(apiKey)
		if policyEntry != nil {
			if policyEntry.IsDisabledAt(time.Now()) {
				body := handlers.BuildErrorResponseBody(http.StatusForbidden, "api key disabled or expired")
				c.Abort()
				c.Data(http.StatusForbidden, "application/json", body)
				return
			}
			resolved, _, errResolve := apikeygroup.ApplyGroupBudget(c.Request.Context(), groupStore, policyEntry)
			if errResolve != nil {
				body := handlers.BuildErrorResponseBody(http.StatusInternalServerError, errResolve.Error())
				c.Abort()
				c.Data(http.StatusInternalServerError, "application/json", body)
				return
			}
			policyEntry = resolved
		}
		if policyEntry != nil {
			c.Set(apiKeyPolicyContextKey, policyEntry)
		}

		if !allowClaudeOpus1M {
			stripClaudeOpus1MHeaders(c.Request.Header)
		}

		// Only enforce request-body model rules for JSON body endpoints.
		// GET /v1/models is handled by response filtering.
		if c.Request.Method == http.MethodGet || c.Request.Method == http.MethodHead || c.Request.Method == http.MethodOptions {
			c.Next()
			return
		}

		bodyBytes, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.Next()
			return
		}
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		if !allowClaudeOpus1M {
			filteredBody := stripClaudeOpus1MBetaFromBody(bodyBytes)
			if !bytes.Equal(filteredBody, bodyBytes) {
				bodyBytes = filteredBody
				c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
				c.Request.ContentLength = int64(len(bodyBytes))
			}
		}

		model := strings.TrimSpace(gjson.GetBytes(bodyBytes, "model").String())
		if model == "" {
			c.Next()
			return
		}

		requestNow := time.Now()
		// Access controls are evaluated against the client-requested model namespace.
		// Downstream routing/failover targets remain unaffected by excluded-models.
		effectiveModel := model

		// 1) Transparent model downgrade rules.
		if policyEntry != nil && !policyEntry.AllowsClaudeOpus46() {
			if rewritten, changed := policy.DowngradeClaudeOpus46(effectiveModel); changed {
				effectiveModel = rewritten
			}
		}
		if cfg.ClaudeToGPTRoutingEnabled && policyEntry != nil && policyEntry.ClaudeModelsEnabled() && policyEntry.ClaudeUsageLimitEnabled() && policy.IsClaudeModel(effectiveModel) {
			exceeded, errExceeded := claudeUsageLimitExceeded(c.Request.Context(), costReader, apiKey, policyEntry)
			if errExceeded != nil {
				body := handlers.BuildErrorResponseBody(http.StatusInternalServerError, errExceeded.Error())
				c.Abort()
				c.Data(http.StatusInternalServerError, "application/json", body)
				return
			}
			if exceeded {
				policyEntry = cfg.EffectiveAPIKeyPolicyWithOptions(apiKey, config.APIKeyPolicyEffectiveOptions{
					ForceGlobalClaudeRouting: true,
				})
				if policyEntry != nil {
					policyEntry, _, errExceeded = apikeygroup.ApplyGroupBudget(c.Request.Context(), groupStore, policyEntry)
					if errExceeded != nil {
						body := handlers.BuildErrorResponseBody(http.StatusInternalServerError, errExceeded.Error())
						c.Abort()
						c.Data(http.StatusInternalServerError, "application/json", body)
						return
					}
				}
				if policyEntry != nil {
					c.Set(apiKeyPolicyContextKey, policyEntry)
				}
			}
		}
		budgetModel := effectiveModel
		if policyEntry != nil {
			if routed, decision := policyEntry.RoutedModelFor(apiKey, effectiveModel, requestNow); decision != nil && strings.TrimSpace(routed) != "" {
				budgetModel = routed
			}
		}
		if policyEntry != nil && policyEntry.FastModeEnabled() {
			requesttrace.UpsertAPIKeyPolicyTraceOnGin(c, func(trace *requesttrace.APIKeyPolicyTrace) {
				trace.APIKey = apiKey
				trace.FastModeEnabled = true
				if strings.TrimSpace(budgetModel) != "" {
					trace.Model = budgetModel
				}
				if strings.TrimSpace(trace.Source) == "" {
					trace.Source = "api_key_policy"
				}
			})
		}
		if policyEntry != nil && policyEntry.FastModeEnabled() && modelSupportsPriorityServiceTier(budgetModel) {
			if updated, errSet := sjson.SetBytes(bodyBytes, "service_tier", "priority"); errSet == nil {
				bodyBytes = updated
				c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))
				c.Request.ContentLength = int64(len(bodyBytes))
				requesttrace.UpsertAPIKeyPolicyTraceOnGin(c, func(trace *requesttrace.APIKeyPolicyTrace) {
					trace.APIKey = apiKey
					trace.FastModeEnabled = true
					trace.FastModeApplied = true
					trace.ServiceTier = "priority"
					trace.Model = budgetModel
					trace.Source = "api_key_policy_middleware"
				})
			}
		}

		// 2) Model allow/deny checks.
		if policyEntry != nil && len(policyEntry.ExcludedModels) > 0 {
			modelKey := policy.NormaliseModelKey(effectiveModel)
			denied := false
			for _, pattern := range policyEntry.ExcludedModels {
				if policy.MatchWildcard(pattern, modelKey) {
					denied = true
					break
				}
			}
			if denied {
				body := handlers.BuildErrorResponseBody(http.StatusForbidden, "model access denied by api key policy")
				c.Abort()
				c.Data(http.StatusForbidden, "application/json", body)
				return
			}
		}

		hasBaseBudgets := policyEntry != nil && (policyEntry.DailyBudgetUSD > 0 || policyEntry.WeeklyBudgetUSD > 0)
		hasTokenPackage := policyEntry != nil && policyEntry.TokenPackageEnabled()
		spendConstrained := hasBaseBudgets || hasTokenPackage
		budgetState := billing.BudgetReplayState{}
		if spendConstrained {
			if costReader == nil {
				body := handlers.BuildErrorResponseBody(http.StatusInternalServerError, "billing store unavailable")
				c.Abort()
				c.Data(http.StatusInternalServerError, "application/json", body)
				return
			}
			resolver, ok := costReader.(priceResolver)
			if !ok {
				body := handlers.BuildErrorResponseBody(http.StatusInternalServerError, "billing price resolver unavailable")
				c.Abort()
				c.Data(http.StatusInternalServerError, "application/json", body)
				return
			}
			if _, source, _, errPrice := resolver.ResolvePriceMicro(c.Request.Context(), budgetModel); errPrice != nil {
				body := handlers.BuildErrorResponseBody(http.StatusInternalServerError, errPrice.Error())
				c.Abort()
				c.Data(http.StatusInternalServerError, "application/json", body)
				return
			} else if source == "missing" {
				body := handlers.BuildErrorResponseBody(http.StatusServiceUnavailable, "budgeted model price unavailable")
				c.Abort()
				c.Data(http.StatusServiceUnavailable, "application/json", body)
				return
			}
			if hasTokenPackage {
				store, ok := costReader.(billing.UsageEventReader)
				if !ok {
					body := handlers.BuildErrorResponseBody(http.StatusInternalServerError, "billing store unavailable")
					c.Abort()
					c.Data(http.StatusInternalServerError, "application/json", body)
					return
				}
				budgetState, err = billing.ComputeBudgetReplayState(c.Request.Context(), store, apiKey, requestNow, policyEntry)
				if err != nil {
					body := handlers.BuildErrorResponseBody(http.StatusInternalServerError, err.Error())
					c.Abort()
					c.Data(http.StatusInternalServerError, "application/json", body)
					return
				}
			}
		}

		if hasBaseBudgets && hasTokenPackage && budgetState.BaseAvailableMicro <= 0 && budgetState.PackageRemainingMicro <= 0 {
			message := "budget exceeded"
			switch {
			case policyEntry.DailyBudgetUSD > 0 && budgetState.DailyRemainingMicro <= 0:
				message = "daily budget exceeded"
			case policyEntry.WeeklyBudgetUSD > 0 && budgetState.WeeklyRemainingMicro <= 0:
				message = "weekly budget exceeded"
			}
			body := handlers.BuildErrorResponseBody(http.StatusTooManyRequests, message)
			c.Abort()
			c.Data(http.StatusTooManyRequests, "application/json", body)
			return
		}
		if hasBaseBudgets && !hasTokenPackage && policyEntry.DailyBudgetUSD > 0 {
			spentMicro, errSpent := costReader.GetDailyCostMicroUSD(c.Request.Context(), apiKey, policy.DayKeyChina(requestNow))
			if errSpent != nil {
				body := handlers.BuildErrorResponseBody(http.StatusInternalServerError, errSpent.Error())
				c.Abort()
				c.Data(http.StatusInternalServerError, "application/json", body)
				return
			}
			budgetMicro := int64(math.Round(policyEntry.DailyBudgetUSD * 1_000_000))
			if budgetMicro > 0 && spentMicro >= budgetMicro {
				body := handlers.BuildErrorResponseBody(http.StatusTooManyRequests, "daily budget exceeded")
				c.Abort()
				c.Data(http.StatusTooManyRequests, "application/json", body)
				return
			}
		}
		if hasBaseBudgets && !hasTokenPackage && policyEntry.WeeklyBudgetUSD > 0 {
			start, end := policyEntry.WeeklyBudgetBounds(requestNow)
			var spentMicro int64
			var errSpent error
			if strings.TrimSpace(policyEntry.WeeklyBudgetAnchorAt) != "" {
				spentMicro, errSpent = costReader.GetCostMicroUSDByTimeRange(c.Request.Context(), apiKey, start, end)
			} else {
				spentMicro, errSpent = costReader.GetCostMicroUSDByDayRange(
					c.Request.Context(),
					apiKey,
					policy.DayKeyChina(start),
					policy.DayKeyChina(end),
				)
			}
			if errSpent != nil {
				body := handlers.BuildErrorResponseBody(http.StatusInternalServerError, errSpent.Error())
				c.Abort()
				c.Data(http.StatusInternalServerError, "application/json", body)
				return
			}
			budgetMicro := int64(math.Round(policyEntry.WeeklyBudgetUSD * 1_000_000))
			if budgetMicro > 0 && spentMicro >= budgetMicro {
				body := handlers.BuildErrorResponseBody(http.StatusTooManyRequests, "weekly budget exceeded")
				c.Abort()
				c.Data(http.StatusTooManyRequests, "application/json", body)
				return
			}
		}
		if !hasBaseBudgets && hasTokenPackage && budgetState.PackageRemainingMicro <= 0 {
			body := handlers.BuildErrorResponseBody(http.StatusTooManyRequests, "token package exhausted")
			c.Abort()
			c.Data(http.StatusTooManyRequests, "application/json", body)
			return
		}

		// 3) Daily usage limits.
		if policyEntry != nil && len(policyEntry.DailyLimits) > 0 {
			modelKey := policy.NormaliseModelKey(effectiveModel)
			limit, limitKey := resolveDailyLimit(policyEntry, modelKey)
			if limit > 0 {
				if limiter == nil {
					body := handlers.BuildErrorResponseBody(http.StatusInternalServerError, "daily limiter unavailable")
					c.Abort()
					c.Data(http.StatusInternalServerError, "application/json", body)
					return
				}
				dayKey := policy.DayKeyChina(requestNow)
				_, allowed, errConsume := limiter.Consume(c.Request.Context(), apiKey, limitKey, dayKey, limit)
				if errConsume != nil {
					body := handlers.BuildErrorResponseBody(http.StatusInternalServerError, errConsume.Error())
					c.Abort()
					c.Data(http.StatusInternalServerError, "application/json", body)
					return
				}
				if !allowed {
					body := handlers.BuildErrorResponseBody(http.StatusTooManyRequests, "daily model limit exceeded")
					c.Abort()
					c.Data(http.StatusTooManyRequests, "application/json", body)
					return
				}
			}
		}

		// If we rewrote the model, patch the request body for downstream handlers.
		if effectiveModel != model {
			modified, errSet := sjson.SetBytes(bodyBytes, "model", effectiveModel)
			if errSet == nil {
				c.Request.Body = io.NopCloser(bytes.NewBuffer(modified))
				c.Request.ContentLength = int64(len(modified))
			}
		}

		c.Next()
	}
}

func stripClaudeOpus1MHeaders(header http.Header) {
	if header == nil {
		return
	}
	header.Del(claudeOpus1MHeaderName)
	if betaHeader := header.Get("Anthropic-Beta"); betaHeader != "" {
		if filtered := filterBetaFeatures(betaHeader, claudeOpus1MBetaName); filtered != "" {
			header.Set("Anthropic-Beta", filtered)
		} else {
			header.Del("Anthropic-Beta")
		}
	}
}

func stripClaudeOpus1MBetaFromBody(body []byte) []byte {
	betasResult := gjson.GetBytes(body, "betas")
	if !betasResult.Exists() {
		return body
	}

	filtered := make([]string, 0, len(betasResult.Array()))
	appendBeta := func(raw string) {
		for _, beta := range strings.Split(raw, ",") {
			trimmed := strings.TrimSpace(beta)
			if trimmed == "" || trimmed == claudeOpus1MBetaName {
				continue
			}
			filtered = append(filtered, trimmed)
		}
	}

	if betasResult.IsArray() {
		for _, item := range betasResult.Array() {
			appendBeta(item.String())
		}
	} else {
		appendBeta(betasResult.String())
	}

	var next []byte
	var err error
	if len(filtered) == 0 {
		next, err = sjson.DeleteBytes(body, "betas")
	} else {
		next, err = sjson.SetBytes(body, "betas", filtered)
	}
	if err != nil {
		return body
	}
	return next
}

func filterBetaFeatures(header, featureToRemove string) string {
	features := strings.Split(header, ",")
	filtered := make([]string, 0, len(features))

	for _, feature := range features {
		trimmed := strings.TrimSpace(feature)
		if trimmed != "" && trimmed != featureToRemove {
			filtered = append(filtered, trimmed)
		}
	}

	return strings.Join(filtered, ",")
}

func resolveDailyLimit(p *config.APIKeyPolicy, modelKey string) (limit int, limitKey string) {
	if p == nil || len(p.DailyLimits) == 0 {
		return 0, ""
	}
	key := strings.ToLower(strings.TrimSpace(modelKey))
	if key == "" {
		return 0, ""
	}
	if v, ok := p.DailyLimits[key]; ok && v > 0 {
		return v, key
	}
	if strings.HasSuffix(key, "-thinking") {
		base := strings.TrimSuffix(key, "-thinking")
		if v, ok := p.DailyLimits[base]; ok && v > 0 {
			return v, base
		}
	}
	return 0, ""
}

func claudeUsageLimitExceeded(
	ctx context.Context,
	costReader billing.DailyCostReader,
	apiKey string,
	p *config.APIKeyPolicy,
) (bool, error) {
	if p == nil || !p.ClaudeModelsEnabled() || !p.ClaudeUsageLimitEnabled() {
		return false, nil
	}
	if costReader == nil {
		return false, fmt.Errorf("billing store unavailable")
	}
	limitMicro := int64(math.Round(p.ClaudeUsageLimitUSD * 1_000_000))
	if limitMicro <= 0 {
		return false, nil
	}
	spentMicro, err := costReader.GetCostMicroUSDByModelPrefix(ctx, apiKey, "claude-")
	if err != nil {
		return false, err
	}
	return spentMicro >= limitMicro, nil
}

func resolveTokenPackageBudgetState(
	ctx context.Context,
	costReader billing.DailyCostReader,
	apiKey string,
	requestNow time.Time,
	requestEndExclusive time.Time,
	p *config.APIKeyPolicy,
) (tokenPackageBudgetState, error) {
	if p == nil || !p.TokenPackageEnabled() {
		return tokenPackageBudgetState{}, nil
	}
	startedAt, ok := p.TokenPackageStartTime()
	if !ok || startedAt.After(requestNow) {
		return tokenPackageBudgetState{}, nil
	}

	packageBudgetMicro := int64(math.Round(p.TokenPackageUSD * 1_000_000))
	if packageBudgetMicro <= 0 {
		return tokenPackageBudgetState{}, nil
	}
	if !requestEndExclusive.After(startedAt) {
		requestEndExclusive = time.Unix(startedAt.Unix()+1, 0)
	}

	spentMicro, err := costReader.GetCostMicroUSDByTimeRange(ctx, apiKey, startedAt, requestEndExclusive)
	if err != nil {
		return tokenPackageBudgetState{}, err
	}
	if spentMicro < packageBudgetMicro {
		return tokenPackageBudgetState{bypassBudgets: true}, nil
	}

	postPackageBudgetFrom, err := findTokenPackagePostBudgetStart(
		ctx,
		costReader,
		apiKey,
		startedAt,
		requestEndExclusive,
		packageBudgetMicro,
	)
	if err != nil {
		return tokenPackageBudgetState{}, err
	}
	return tokenPackageBudgetState{postPackageBudgetFrom: postPackageBudgetFrom}, nil
}

func findTokenPackagePostBudgetStart(
	ctx context.Context,
	costReader billing.DailyCostReader,
	apiKey string,
	startInclusive time.Time,
	endExclusive time.Time,
	budgetMicro int64,
) (time.Time, error) {
	low := startInclusive.Unix() + 1
	high := endExclusive.Unix()
	if high < low {
		return endExclusive, nil
	}
	for low < high {
		mid := low + (high-low)/2
		spentMicro, err := costReader.GetCostMicroUSDByTimeRange(ctx, apiKey, startInclusive, time.Unix(mid, 0))
		if err != nil {
			return time.Time{}, err
		}
		if spentMicro >= budgetMicro {
			high = mid
		} else {
			low = mid + 1
		}
	}
	return time.Unix(low, 0), nil
}

func readBudgetWindowCost(
	ctx context.Context,
	costReader billing.DailyCostReader,
	apiKey string,
	windowStart time.Time,
	windowEnd time.Time,
	requestEndExclusive time.Time,
	postPackageBudgetFrom time.Time,
	readWholeWindow func() (int64, error),
) (int64, error) {
	if costReader == nil {
		return 0, nil
	}
	if postPackageBudgetFrom.IsZero() || !postPackageBudgetFrom.After(windowStart) {
		return readWholeWindow()
	}

	effectiveStart := postPackageBudgetFrom
	if effectiveStart.Before(windowStart) {
		effectiveStart = windowStart
	}
	effectiveEnd := requestEndExclusive
	if !windowEnd.IsZero() && effectiveEnd.After(windowEnd) {
		effectiveEnd = windowEnd
	}
	if !effectiveEnd.After(effectiveStart) {
		return 0, nil
	}
	return costReader.GetCostMicroUSDByTimeRange(ctx, apiKey, effectiveStart, effectiveEnd)
}

func dayBoundsChina(now time.Time) (time.Time, time.Time) {
	if now.IsZero() {
		now = time.Now()
	}
	local := now.In(policy.ChinaLocation())
	start := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, policy.ChinaLocation())
	return start, start.AddDate(0, 0, 1)
}
