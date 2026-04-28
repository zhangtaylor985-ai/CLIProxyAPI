package middleware

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/apikeygroup"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/billing"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/clientidentity"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/policy"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/requesttrace"
	"github.com/router-for-me/CLIProxyAPI/v6/sdk/api/handlers"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
	"github.com/tiktoken-go/tokenizer"
)

const (
	apiKeyPolicyContextKey = "apiKeyPolicy"
	claudeOpus1MHeaderName = "X-CPA-CLAUDE-1M"
	claudeOpus1MBetaName   = "context-1m-2025-08-07"

	// Claude Code/Codex expose a product-side effective prompt window below the
	// raw registry context window (for example 272k -> about 258k). Mirror that
	// policy for non-1M Opus client keys so service-side preflight matches the
	// client's practical session budget instead of API-level million-token caps.
	claudeOrdinaryOpusContextTokens     = 200000
	claudeCodexEffectiveContextPercent  = 95
	claudePromptTooLongEstimateDivisor  = 3
	claudeImagePixelsPerToken           = 750
	claudeImageMaxBillablePixels        = 1152000
	claudeImageFallbackTokens           = 1600
	claudeGPTImageLowDetailTokens       = 70
	claudeGPTImageBaseTokens            = 70
	claudeGPTImageTileTokens            = 140
	claudeGPTImageMaxSidePixels         = 2048
	claudeGPTImageShortSidePixels       = 768
	claudeGPTImageTileSizePixels        = 512
	claudeOneMillionContextTokens       = 1050000
	claudeDocumentFallbackTokens        = 8000
	claudeDocumentBytesPerToken         = 512
	claudeDocumentMaxEstimateTokens     = 64000
	claudeTokenEstimateMessageOverhead  = 4
	claudeTokenEstimateToolOverhead     = 8
	claudeTokenEstimateBlockOverhead    = 1
	claudeTokenizerRefineThresholdPct   = 80
	claudePromptTooLongErrorContentType = "application/json"
	claudePromptTooLongMessage          = "Prompt is too long. Please run /compact and try again."
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
				body := buildPolicyErrorResponseBody(c, http.StatusForbidden, "api key disabled or expired")
				c.Abort()
				c.Data(http.StatusForbidden, "application/json", body)
				return
			}
			resolved, _, errResolve := apikeygroup.ApplyGroupBudget(c.Request.Context(), groupStore, policyEntry)
			if errResolve != nil {
				body := buildPolicyErrorResponseBody(c, http.StatusInternalServerError, errResolve.Error())
				c.Abort()
				c.Data(http.StatusInternalServerError, "application/json", body)
				return
			}
			policyEntry = resolved
		}
		if policyEntry != nil {
			c.Set(apiKeyPolicyContextKey, policyEntry)
		}
		if policyEntry != nil && policyEntry.ClaudeCodeOnlyEnabled() && !clientidentity.IsClaudeCodeRequest(c.Request) {
			body := buildPolicyErrorResponseBody(c, http.StatusForbidden, "api key is restricted to Claude Code clients")
			c.Abort()
			c.Data(http.StatusForbidden, "application/json", body)
			return
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
		replaceRequestBody(c, bodyBytes)

		if !allowClaudeOpus1M {
			filteredBody := stripClaudeOpus1MBetaFromBody(bodyBytes)
			if !bytes.Equal(filteredBody, bodyBytes) {
				bodyBytes = filteredBody
				replaceRequestBody(c, bodyBytes)
			}
			rewrittenBody := rewriteClaudeOpus1MModelInBody(bodyBytes)
			if !bytes.Equal(rewrittenBody, bodyBytes) {
				bodyBytes = rewrittenBody
				replaceRequestBody(c, bodyBytes)
			}
		}

		model := strings.TrimSpace(gjson.GetBytes(bodyBytes, "model").String())
		if model == "" {
			c.Next()
			return
		}
		requestNow := time.Now()
		// Access controls are evaluated against the client-requested model namespace.
		// Downstream routing/fallback targets remain unaffected by excluded-models.
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
				body := buildPolicyErrorResponseBody(c, http.StatusInternalServerError, errExceeded.Error())
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
						body := buildPolicyErrorResponseBody(c, http.StatusInternalServerError, errExceeded.Error())
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
		if shouldEnforceClaudeBaseContextLimit(c.Request, effectiveModel) {
			contextLimit := claudePromptContextLimitTokensForPolicy(budgetModel, allowClaudeOpus1M)
			estimatedTokens := estimateClaudeRequestTokensWithinLimitForModel(bodyBytes, contextLimit, budgetModel)
			if estimatedTokens > contextLimit {
				if claudeContextLimitAlertEnabled(cfg) {
					alertClaudePromptTooLong(c, apiKey, effectiveModel, estimatedTokens, contextLimit)
				}
				body := buildClaudePolicyErrorResponseBody(
					c,
					"invalid_request_error",
					claudePromptTooLongMessage,
				)
				c.Abort()
				c.Data(http.StatusBadRequest, claudePromptTooLongErrorContentType, body)
				return
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
				replaceRequestBody(c, bodyBytes)
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
				body := buildPolicyErrorResponseBody(c, http.StatusForbidden, "model access denied by api key policy")
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
				body := buildPolicyErrorResponseBody(c, http.StatusInternalServerError, "billing store unavailable")
				c.Abort()
				c.Data(http.StatusInternalServerError, "application/json", body)
				return
			}
			resolver, ok := costReader.(priceResolver)
			if !ok {
				body := buildPolicyErrorResponseBody(c, http.StatusInternalServerError, "billing price resolver unavailable")
				c.Abort()
				c.Data(http.StatusInternalServerError, "application/json", body)
				return
			}
			if _, source, _, errPrice := resolver.ResolvePriceMicro(c.Request.Context(), budgetModel); errPrice != nil {
				body := buildPolicyErrorResponseBody(c, http.StatusInternalServerError, errPrice.Error())
				c.Abort()
				c.Data(http.StatusInternalServerError, "application/json", body)
				return
			} else if source == "missing" {
				body := buildPolicyErrorResponseBody(c, http.StatusServiceUnavailable, "budgeted model price unavailable")
				c.Abort()
				c.Data(http.StatusServiceUnavailable, "application/json", body)
				return
			}
			if hasTokenPackage {
				store, ok := costReader.(billing.UsageEventReader)
				if !ok {
					body := buildPolicyErrorResponseBody(c, http.StatusInternalServerError, "billing store unavailable")
					c.Abort()
					c.Data(http.StatusInternalServerError, "application/json", body)
					return
				}
				budgetState, err = billing.ComputeBudgetReplayState(c.Request.Context(), store, apiKey, requestNow, policyEntry)
				if err != nil {
					body := buildPolicyErrorResponseBody(c, http.StatusInternalServerError, err.Error())
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
			body := buildPolicyErrorResponseBody(c, http.StatusTooManyRequests, message)
			c.Abort()
			c.Data(http.StatusTooManyRequests, "application/json", body)
			return
		}
		if hasBaseBudgets && !hasTokenPackage && policyEntry.DailyBudgetUSD > 0 {
			spentMicro, errSpent := costReader.GetDailyCostMicroUSD(c.Request.Context(), apiKey, policy.DayKeyChina(requestNow))
			if errSpent != nil {
				body := buildPolicyErrorResponseBody(c, http.StatusInternalServerError, errSpent.Error())
				c.Abort()
				c.Data(http.StatusInternalServerError, "application/json", body)
				return
			}
			budgetMicro := int64(math.Round(policyEntry.DailyBudgetUSD * 1_000_000))
			if budgetMicro > 0 && spentMicro >= budgetMicro {
				body := buildPolicyErrorResponseBody(c, http.StatusTooManyRequests, "daily budget exceeded")
				c.Abort()
				c.Data(http.StatusTooManyRequests, "application/json", body)
				return
			}
		}
		if hasBaseBudgets && !hasTokenPackage && policyEntry.WeeklyBudgetUSD > 0 {
			start, end, errBounds := policyEntry.WeeklyBudgetBounds(requestNow)
			if errBounds != nil {
				body := buildPolicyErrorResponseBody(c, http.StatusInternalServerError, errBounds.Error())
				c.Abort()
				c.Data(http.StatusInternalServerError, "application/json", body)
				return
			}
			spentMicro, errSpent := costReader.GetCostMicroUSDByTimeRange(c.Request.Context(), apiKey, start, end)
			if errSpent != nil {
				body := buildPolicyErrorResponseBody(c, http.StatusInternalServerError, errSpent.Error())
				c.Abort()
				c.Data(http.StatusInternalServerError, "application/json", body)
				return
			}
			budgetMicro := int64(math.Round(policyEntry.WeeklyBudgetUSD * 1_000_000))
			if budgetMicro > 0 && spentMicro >= budgetMicro {
				body := buildPolicyErrorResponseBody(c, http.StatusTooManyRequests, "weekly budget exceeded")
				c.Abort()
				c.Data(http.StatusTooManyRequests, "application/json", body)
				return
			}
		}
		if !hasBaseBudgets && hasTokenPackage && budgetState.PackageRemainingMicro <= 0 {
			body := buildPolicyErrorResponseBody(c, http.StatusTooManyRequests, "token package exhausted")
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
					body := buildPolicyErrorResponseBody(c, http.StatusInternalServerError, "daily limiter unavailable")
					c.Abort()
					c.Data(http.StatusInternalServerError, "application/json", body)
					return
				}
				dayKey := policy.DayKeyChina(requestNow)
				_, allowed, errConsume := limiter.Consume(c.Request.Context(), apiKey, limitKey, dayKey, limit)
				if errConsume != nil {
					body := buildPolicyErrorResponseBody(c, http.StatusInternalServerError, errConsume.Error())
					c.Abort()
					c.Data(http.StatusInternalServerError, "application/json", body)
					return
				}
				if !allowed {
					body := buildPolicyErrorResponseBody(c, http.StatusTooManyRequests, "daily model limit exceeded")
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
				replaceRequestBody(c, modified)
			}
		}

		c.Next()
	}
}

func replaceRequestBody(c *gin.Context, body []byte) {
	if c == nil || c.Request == nil {
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewBuffer(body))
	c.Request.ContentLength = int64(len(body))
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

func rewriteClaudeOpus1MModelInBody(body []byte) []byte {
	model := strings.TrimSpace(gjson.GetBytes(body, "model").String())
	if model == "" {
		return body
	}
	rewritten, changed := policy.RewriteClaudeOpus1MToBase(model)
	if !changed {
		return body
	}
	next, err := sjson.SetBytes(body, "model", rewritten)
	if err != nil {
		return body
	}
	return next
}

func shouldEnforceClaudeBaseContextLimit(req *http.Request, model string) bool {
	if req == nil || req.Method != http.MethodPost {
		return false
	}
	if !isClaudeOpusModel(model) {
		return false
	}
	path := strings.TrimRight(req.URL.Path, "/")
	return strings.HasSuffix(path, "/v1/messages")
}

func isClaudeOpusModel(model string) bool {
	return strings.HasPrefix(policy.NormaliseModelKey(model), "claude-opus-")
}

func estimateClaudeRequestTokens(body []byte) int {
	return estimateClaudeRequestTokensWithinLimit(body, 0)
}

func estimateClaudeRequestTokensWithinLimit(body []byte, limit int) int {
	return estimateClaudeRequestTokensWithinLimitForModel(body, limit, "")
}

func estimateClaudeRequestTokensWithinLimitForModel(body []byte, limit int, routedModel string) int {
	if len(body) == 0 {
		return 0
	}
	rawEstimate := (len(body) + claudePromptTooLongEstimateDivisor - 1) / claudePromptTooLongEstimateDivisor
	if limit > 0 && rawEstimate <= limit && !shouldRefineClaudePromptEstimate(rawEstimate, limit, routedModel) {
		return rawEstimate
	}
	if semanticTokens, ok := estimateClaudeSemanticPromptTokens(body, routedModel); ok {
		return semanticTokens
	}
	if semanticBytes, ok := estimateClaudeSemanticPromptBytes(body); ok {
		return (semanticBytes + claudePromptTooLongEstimateDivisor - 1) / claudePromptTooLongEstimateDivisor
	}
	return rawEstimate
}

func shouldRefineClaudePromptEstimate(rawEstimate, limit int, routedModel string) bool {
	if limit <= 0 || rawEstimate <= 0 {
		return false
	}
	if !isTokenizableClaudePromptModel(routedModel) {
		return false
	}
	return rawEstimate*100 >= limit*claudeTokenizerRefineThresholdPct
}

func claudePromptContextLimitTokens(routedModel string) int {
	contextWindow := claudeModelContextWindowTokens(routedModel)
	if contextWindow <= 0 {
		contextWindow = claudeOrdinaryOpusContextTokens
	}
	return claudeEffectivePromptContextLimitTokens(contextWindow)
}

func claudePromptContextLimitTokensForPolicy(routedModel string, allowClaudeOpus1M bool) int {
	if allowClaudeOpus1M {
		return claudeEffectivePromptContextLimitTokens(claudeOneMillionContextTokens)
	}
	return claudePromptContextLimitTokens(routedModel)
}

func claudeContextLimitAlertEnabled(cfg *config.Config) bool {
	if cfg == nil || cfg.Notifications.Telegram.ErrorLog.ClaudeContextLimitAlertEnabled == nil {
		return true
	}
	return *cfg.Notifications.Telegram.ErrorLog.ClaudeContextLimitAlertEnabled
}

func alertClaudePromptTooLong(c *gin.Context, apiKey, model string, estimatedTokens, contextLimit int) {
	fields := log.Fields{
		"component":            "claude_prompt_context_preflight",
		"estimated_tokens":     estimatedTokens,
		"context_limit_tokens": contextLimit,
		"model":                strings.TrimSpace(model),
	}
	if requestID := handlers.GinRequestID(c); requestID != "" {
		fields["request_id"] = requestID
	}
	if rawAPIKey := strings.TrimSpace(apiKey); rawAPIKey != "" {
		fields["client_api_key"] = rawAPIKey
	}
	if c != nil && c.Request != nil && c.Request.URL != nil {
		fields["path"] = c.Request.URL.Path
	}
	log.WithFields(fields).Errorf(
		"Claude prompt context preflight exceeded: estimated_tokens=%d limit_tokens=%d",
		estimatedTokens,
		contextLimit,
	)
}

func claudeEffectivePromptContextLimitTokens(contextWindow int) int {
	if contextWindow <= 0 {
		return 0
	}
	return contextWindow * claudeCodexEffectiveContextPercent / 100
}

func claudeModelContextWindowTokens(model string) int {
	key := policy.NormaliseModelKey(model)
	if key == "" {
		return 0
	}
	if isClaudeOpusModel(key) {
		return claudeOrdinaryOpusContextTokens
	}
	info := registry.LookupModelInfo(key)
	if info == nil {
		return 0
	}
	if info.ContextLength > 0 {
		return info.ContextLength
	}
	if info.InputTokenLimit > 0 && info.OutputTokenLimit > 0 {
		return info.InputTokenLimit + info.OutputTokenLimit
	}
	return info.InputTokenLimit
}

func estimateClaudeSemanticPromptBytes(body []byte) (int, bool) {
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return 0, false
	}
	obj, ok := root.(map[string]any)
	if !ok {
		return 0, false
	}
	total := 0
	total += estimateClaudeContentBytes(obj["system"])
	total += estimateClaudeMessagesBytes(obj["messages"])
	total += estimateClaudeToolsBytes(obj["tools"])
	return total, true
}

func estimateClaudeSemanticPromptTokens(body []byte, routedModel string) (int, bool) {
	enc, ok := tokenizerForClaudePromptModel(routedModel)
	if !ok || enc == nil {
		return 0, false
	}
	var root any
	if err := json.Unmarshal(body, &root); err != nil {
		return 0, false
	}
	obj, ok := root.(map[string]any)
	if !ok {
		return 0, false
	}
	collector := claudePromptTokenCollector{
		segments:            make([]string, 0, 64),
		imageTokenEstimator: estimateClaudeGPTImageBlockTokens,
	}
	collector.collectContent(obj["system"])
	collector.collectMessages(obj["messages"])
	collector.collectTools(obj["tools"])
	text := strings.TrimSpace(strings.Join(collector.segments, "\n"))
	if text == "" {
		return collector.extraTokens, true
	}
	count, err := enc.Count(text)
	if err != nil {
		return 0, false
	}
	return count + collector.extraTokens, true
}

func tokenizerForClaudePromptModel(model string) (tokenizer.Codec, bool) {
	key := policy.NormaliseModelKey(model)
	switch {
	case strings.HasPrefix(key, "gpt-5"):
		enc, err := tokenizer.ForModel(tokenizer.GPT5)
		return enc, err == nil
	case strings.HasPrefix(key, "gpt-4.1"):
		enc, err := tokenizer.ForModel(tokenizer.GPT41)
		return enc, err == nil
	case strings.HasPrefix(key, "gpt-4o"):
		enc, err := tokenizer.ForModel(tokenizer.GPT4o)
		return enc, err == nil
	case strings.HasPrefix(key, "gpt-4"):
		enc, err := tokenizer.ForModel(tokenizer.GPT4)
		return enc, err == nil
	case strings.HasPrefix(key, "gpt-3.5"), strings.HasPrefix(key, "gpt-3"):
		enc, err := tokenizer.ForModel(tokenizer.GPT35Turbo)
		return enc, err == nil
	case strings.HasPrefix(key, "o1"):
		enc, err := tokenizer.ForModel(tokenizer.O1)
		return enc, err == nil
	case strings.HasPrefix(key, "o3"):
		enc, err := tokenizer.ForModel(tokenizer.O3)
		return enc, err == nil
	case strings.HasPrefix(key, "o4"):
		enc, err := tokenizer.ForModel(tokenizer.O4Mini)
		return enc, err == nil
	case strings.HasPrefix(key, "chatgpt-"):
		enc, err := tokenizer.Get(tokenizer.O200kBase)
		return enc, err == nil
	default:
		return nil, false
	}
}

func isTokenizableClaudePromptModel(model string) bool {
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

type claudePromptTokenCollector struct {
	segments            []string
	extraTokens         int
	imageTokenEstimator func(map[string]any) int
}

func (c *claudePromptTokenCollector) collectMessages(value any) {
	messages, ok := value.([]any)
	if !ok {
		c.collectContent(value)
		return
	}
	for _, message := range messages {
		msg, ok := message.(map[string]any)
		if !ok {
			c.collectContent(message)
			continue
		}
		c.extraTokens += claudeTokenEstimateMessageOverhead
		c.addString(stringFromMap(msg, "role"))
		c.collectContent(msg["content"])
	}
}

func (c *claudePromptTokenCollector) collectTools(value any) {
	tools, ok := value.([]any)
	if !ok {
		c.addCompactJSON(value)
		return
	}
	for _, tool := range tools {
		t, ok := tool.(map[string]any)
		if !ok {
			c.addCompactJSON(tool)
			continue
		}
		c.extraTokens += claudeTokenEstimateToolOverhead
		c.addString(stringFromMap(t, "name"))
		c.addString(stringFromMap(t, "description"))
		c.addCompactJSON(t["input_schema"])
	}
}

func (c *claudePromptTokenCollector) collectContent(value any) {
	switch v := value.(type) {
	case nil:
		return
	case string:
		c.addString(v)
	case []any:
		for _, item := range v {
			c.collectContent(item)
		}
	case map[string]any:
		c.collectContentBlock(v)
	default:
		c.addCompactJSON(v)
	}
}

func (c *claudePromptTokenCollector) collectContentBlock(block map[string]any) {
	c.extraTokens += claudeTokenEstimateBlockOverhead
	switch blockType, _ := block["type"].(string); blockType {
	case "text":
		c.addString(stringFromMap(block, "text"))
	case "thinking":
		c.addString(stringFromMap(block, "thinking"))
	case "redacted_thinking":
		c.addString(stringFromMap(block, "data"))
	case "tool_use":
		c.addString(stringFromMap(block, "name"))
		c.addCompactJSON(block["input"])
	case "tool_result":
		c.collectContent(block["content"])
	case "image":
		tokens := 0
		if c.imageTokenEstimator != nil {
			tokens = c.imageTokenEstimator(block)
		}
		if tokens <= 0 {
			tokens = estimateClaudeImageBlockTokens(block)
		}
		if tokens <= 0 {
			tokens = claudeImageFallbackTokens
		}
		c.extraTokens += tokens
	case "document":
		tokens := estimateClaudeDocumentBlockTokens(block)
		if tokens <= 0 {
			tokens = claudeDocumentFallbackTokens
		}
		c.extraTokens += tokens
		c.addString(stringFromMap(block, "title"))
		c.addString(stringFromMap(block, "context"))
	default:
		c.addCompactJSON(block)
	}
}

func (c *claudePromptTokenCollector) addString(value string) {
	if value = strings.TrimSpace(value); value != "" {
		c.segments = append(c.segments, value)
	}
}

func (c *claudePromptTokenCollector) addCompactJSON(value any) {
	if value == nil {
		return
	}
	data, err := json.Marshal(value)
	if err != nil {
		return
	}
	c.addString(string(data))
}

func estimateClaudeMessagesBytes(value any) int {
	messages, ok := value.([]any)
	if !ok {
		return estimateClaudeContentBytes(value)
	}
	total := 0
	for _, message := range messages {
		msg, ok := message.(map[string]any)
		if !ok {
			total += estimateClaudeContentBytes(message)
			continue
		}
		if role, ok := msg["role"].(string); ok {
			total += len(role)
		}
		total += estimateClaudeContentBytes(msg["content"])
	}
	return total
}

func estimateClaudeToolsBytes(value any) int {
	tools, ok := value.([]any)
	if !ok {
		return estimateClaudeCompactJSONBytes(value)
	}
	total := 0
	for _, tool := range tools {
		t, ok := tool.(map[string]any)
		if !ok {
			total += estimateClaudeCompactJSONBytes(tool)
			continue
		}
		total += estimateClaudeStringFieldBytes(t, "name")
		total += estimateClaudeStringFieldBytes(t, "description")
		total += estimateClaudeCompactJSONBytes(t["input_schema"])
	}
	return total
}

func estimateClaudeContentBytes(value any) int {
	switch v := value.(type) {
	case nil:
		return 0
	case string:
		return len(v)
	case []any:
		total := 0
		for _, item := range v {
			total += estimateClaudeContentBytes(item)
		}
		return total
	case map[string]any:
		return estimateClaudeContentBlockBytes(v)
	default:
		return estimateClaudeCompactJSONBytes(v)
	}
}

func estimateClaudeContentBlockBytes(block map[string]any) int {
	switch blockType, _ := block["type"].(string); blockType {
	case "text":
		return estimateClaudeStringFieldBytes(block, "text")
	case "thinking":
		return estimateClaudeStringFieldBytes(block, "thinking")
	case "redacted_thinking":
		return estimateClaudeStringFieldBytes(block, "data")
	case "tool_use":
		return estimateClaudeStringFieldBytes(block, "name") + estimateClaudeCompactJSONBytes(block["input"])
	case "tool_result":
		return estimateClaudeContentBytes(block["content"])
	case "image":
		return estimateClaudeImageBlockBytes(block)
	case "document":
		return estimateClaudeDocumentBlockBytes(block)
	default:
		return estimateClaudeCompactJSONBytes(block)
	}
}

func estimateClaudeDocumentBlockBytes(block map[string]any) int {
	tokens := estimateClaudeDocumentBlockTokens(block)
	if tokens <= 0 {
		tokens = claudeDocumentFallbackTokens
	}
	return tokens * claudePromptTooLongEstimateDivisor
}

func estimateClaudeImageBlockBytes(block map[string]any) int {
	tokens := estimateClaudeImageBlockTokens(block)
	if tokens <= 0 {
		tokens = claudeImageFallbackTokens
	}
	return tokens * claudePromptTooLongEstimateDivisor
}

func estimateClaudeImageBlockTokens(block map[string]any) int {
	cfg, ok := decodeClaudeBase64ImageConfig(block)
	if !ok {
		return claudeImageFallbackTokens
	}
	pixels := int64(cfg.Width) * int64(cfg.Height)
	maxPixels := int64(claudeImageMaxBillablePixels)
	if pixels > maxPixels {
		pixels = maxPixels
	}
	return int((pixels + int64(claudeImagePixelsPerToken) - 1) / int64(claudeImagePixelsPerToken))
}

func estimateClaudeGPTImageBlockTokens(block map[string]any) int {
	detail := strings.ToLower(strings.TrimSpace(stringFromMap(block, "detail")))
	if detail == "low" {
		return claudeGPTImageLowDetailTokens
	}
	cfg, ok := decodeClaudeBase64ImageConfig(block)
	if !ok {
		return claudeImageFallbackTokens
	}
	return estimateGPTVisionHighDetailImageTokens(cfg.Width, cfg.Height)
}

func decodeClaudeBase64ImageConfig(block map[string]any) (image.Config, bool) {
	source, ok := block["source"].(map[string]any)
	if !ok {
		return image.Config{}, false
	}
	sourceType := strings.ToLower(strings.TrimSpace(stringFromMap(source, "type")))
	if sourceType != "base64" {
		return image.Config{}, false
	}
	data := strings.TrimSpace(stringFromMap(source, "data"))
	if data == "" {
		return image.Config{}, false
	}
	if comma := strings.IndexByte(data, ','); comma >= 0 && strings.Contains(data[:comma], "base64") {
		data = data[comma+1:]
	}
	cfg, _, err := image.DecodeConfig(base64.NewDecoder(base64.StdEncoding, strings.NewReader(data)))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return image.Config{}, false
	}
	return cfg, true
}

func estimateGPTVisionHighDetailImageTokens(width, height int) int {
	if width <= 0 || height <= 0 {
		return claudeImageFallbackTokens
	}
	w := float64(width)
	h := float64(height)
	maxSide := math.Max(w, h)
	if maxSide > claudeGPTImageMaxSidePixels {
		scale := float64(claudeGPTImageMaxSidePixels) / maxSide
		w *= scale
		h *= scale
	}
	shortSide := math.Min(w, h)
	if shortSide > claudeGPTImageShortSidePixels {
		scale := float64(claudeGPTImageShortSidePixels) / shortSide
		w *= scale
		h *= scale
	}
	tilesWide := int(math.Ceil(w / claudeGPTImageTileSizePixels))
	tilesHigh := int(math.Ceil(h / claudeGPTImageTileSizePixels))
	if tilesWide < 1 {
		tilesWide = 1
	}
	if tilesHigh < 1 {
		tilesHigh = 1
	}
	return claudeGPTImageBaseTokens + claudeGPTImageTileTokens*tilesWide*tilesHigh
}

func estimateClaudeDocumentBlockTokens(block map[string]any) int {
	source, ok := block["source"].(map[string]any)
	if !ok {
		return claudeDocumentFallbackTokens
	}
	sourceType := strings.ToLower(strings.TrimSpace(stringFromMap(source, "type")))
	if sourceType != "base64" {
		return claudeDocumentFallbackTokens
	}
	data := strings.TrimSpace(stringFromMap(source, "data"))
	if data == "" {
		return claudeDocumentFallbackTokens
	}
	if comma := strings.IndexByte(data, ','); comma >= 0 && strings.Contains(data[:comma], "base64") {
		data = data[comma+1:]
	}
	decodedBytes := estimatedBase64DecodedBytes(data)
	if decodedBytes <= 0 {
		return claudeDocumentFallbackTokens
	}
	tokens := (decodedBytes + claudeDocumentBytesPerToken - 1) / claudeDocumentBytesPerToken
	if tokens < claudeDocumentFallbackTokens {
		return claudeDocumentFallbackTokens
	}
	if tokens > claudeDocumentMaxEstimateTokens {
		return claudeDocumentMaxEstimateTokens
	}
	return tokens
}

func estimatedBase64DecodedBytes(data string) int {
	trimmed := strings.TrimRight(strings.TrimSpace(data), "=")
	if trimmed == "" {
		return 0
	}
	decoded := len(trimmed) * 3 / 4
	if decoded <= 0 {
		return 0
	}
	return decoded
}

func estimateClaudeStringFieldBytes(obj map[string]any, field string) int {
	return len(stringFromMap(obj, field))
}

func stringFromMap(obj map[string]any, field string) string {
	value, ok := obj[field].(string)
	if !ok {
		return ""
	}
	return value
}

func estimateClaudeCompactJSONBytes(value any) int {
	if value == nil {
		return 0
	}
	data, err := json.Marshal(value)
	if err != nil {
		return 0
	}
	return len(data)
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

func buildPolicyErrorResponseBody(c *gin.Context, status int, message string) []byte {
	return handlers.BuildErrorResponseBodyWithRequestID(status, message, handlers.GinRequestID(c))
}

func buildClaudePolicyErrorResponseBody(c *gin.Context, errType string, message string) []byte {
	errType = strings.TrimSpace(errType)
	if errType == "" {
		errType = "api_error"
	}
	message = strings.TrimSpace(message)
	if message == "" {
		message = http.StatusText(http.StatusBadRequest)
	}
	body := []byte(fmt.Sprintf(`{"type":"error","error":{"type":%q,"message":%q}}`, errType, message))
	return handlers.AttachRequestIDToErrorBody(body, handlers.GinRequestID(c))
}
