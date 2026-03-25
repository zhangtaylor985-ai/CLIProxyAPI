package policy

import (
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/thinking"
)

const (
	claudeModelPrefix              = "claude-"
	claudeOpusPrefix               = "claude-opus-"
	claudeOpus46Prefix             = "claude-opus-4-6"
	claudeOpus45FallbackPrefix     = "claude-opus-4-5-20251101"
	claudeThinkingSuffixLiteral    = "-thinking"
	ClaudeGPTTargetFamilyGPT52     = "gpt-5.2"
	ClaudeGPTTargetFamilyGPT54     = "gpt-5.4"
	ClaudeGPTTargetModelGPT53Codex = "gpt-5.3-codex"
	defaultClaudeGPTHighTarget     = "gpt-5.4(high)"
	defaultClaudeGPTMediumTarget   = "gpt-5.4(medium)"
)

// NormaliseModelKey returns a lowercased model name without thinking budget suffix "(...)".
func NormaliseModelKey(model string) string {
	parsed := thinking.ParseSuffix(strings.TrimSpace(model))
	return strings.ToLower(strings.TrimSpace(parsed.ModelName))
}

// DowngradeClaudeOpus46 rewrites claude-opus-4-6* to claude-opus-4-5-20251101* while preserving
// any suffix segments (e.g., "-thinking") and thinking budget suffix "(...)".
func DowngradeClaudeOpus46(model string) (string, bool) {
	trimmed := strings.TrimSpace(model)
	if trimmed == "" {
		return model, false
	}
	parsed := thinking.ParseSuffix(trimmed)
	base := parsed.ModelName
	baseLower := strings.ToLower(strings.TrimSpace(base))
	if !strings.HasPrefix(baseLower, claudeOpus46Prefix) {
		return model, false
	}

	// Preserve the remainder after the opus-4-6 prefix (e.g., "-thinking").
	remainder := ""
	if len(base) >= len(claudeOpus46Prefix) {
		remainder = base[len(claudeOpus46Prefix):]
	}

	rewritten := claudeOpus45FallbackPrefix + remainder
	if parsed.HasSuffix {
		rewritten = rewritten + "(" + parsed.RawSuffix + ")"
	}
	return rewritten, true
}

// IsClaudeOpus46 returns true when the model name (after stripping "(...)") starts with claude-opus-4-6.
func IsClaudeOpus46(model string) bool {
	return strings.HasPrefix(NormaliseModelKey(model), claudeOpus46Prefix)
}

// IsClaudeModel returns true when the model name (after stripping "(...)") starts with claude-.
func IsClaudeModel(model string) bool {
	return strings.HasPrefix(NormaliseModelKey(model), claudeModelPrefix)
}

// DefaultClaudeGPTTarget maps Claude requests to the default GPT target used by
// the global Claude -> GPT routing feature.
func DefaultClaudeGPTTarget(model string) (string, bool) {
	return DefaultClaudeGPTTargetForFamily(model, "")
}

// NormalizeClaudeGPTTargetFamily returns a canonical GPT family ID for Claude -> GPT routing.
// Unsupported values are normalized to "" so callers can fall back to the default family.
func NormalizeClaudeGPTTargetFamily(value string) string {
	switch NormaliseModelKey(value) {
	case ClaudeGPTTargetFamilyGPT52:
		return ClaudeGPTTargetFamilyGPT52
	case ClaudeGPTTargetFamilyGPT54:
		return ClaudeGPTTargetFamilyGPT54
	case ClaudeGPTTargetModelGPT53Codex:
		return ClaudeGPTTargetModelGPT53Codex
	default:
		return ""
	}
}

// NormalizeClaudeGPTTargetBase returns a canonical target base model for per-key
// Claude -> GPT routing overrides. Unsupported values are normalized to "".
func NormalizeClaudeGPTTargetBase(value string) string {
	switch NormaliseModelKey(value) {
	case ClaudeGPTTargetFamilyGPT52:
		return ClaudeGPTTargetFamilyGPT52
	case ClaudeGPTTargetFamilyGPT54:
		return ClaudeGPTTargetFamilyGPT54
	case ClaudeGPTTargetModelGPT53Codex:
		return ClaudeGPTTargetModelGPT53Codex
	default:
		return ""
	}
}

// EffectiveClaudeGPTTargetFamily resolves the configured Claude -> GPT family,
// defaulting to gpt-5.4 when unset or invalid.
func EffectiveClaudeGPTTargetFamily(value string) string {
	if family := NormalizeClaudeGPTTargetFamily(value); family != "" {
		return family
	}
	return ClaudeGPTTargetFamilyGPT54
}

// EffectiveClaudeGPTTargetBase resolves the configured per-key Claude -> GPT target base model,
// defaulting to gpt-5.4 when unset or invalid.
func EffectiveClaudeGPTTargetBase(value string) string {
	if base := NormalizeClaudeGPTTargetBase(value); base != "" {
		return base
	}
	return ClaudeGPTTargetFamilyGPT54
}

// DefaultClaudeGPTTargetForFamily maps Claude requests to the default GPT target
// used by Claude -> GPT routing for the selected target base model.
func DefaultClaudeGPTTargetForFamily(model, family string) (string, bool) {
	key := NormaliseModelKey(model)
	if !strings.HasPrefix(key, claudeModelPrefix) {
		return "", false
	}
	family = EffectiveClaudeGPTTargetBase(family)
	if strings.HasPrefix(key, claudeOpusPrefix) {
		return family + "(high)", true
	}
	return family + "(medium)", true
}

// MatchWildcard performs case-insensitive matching where '*' matches any substring.
// Pattern and value are expected to be lowercased by callers; the function lowercases defensively.
func MatchWildcard(pattern, value string) bool {
	pattern = strings.ToLower(strings.TrimSpace(pattern))
	value = strings.ToLower(strings.TrimSpace(value))
	if pattern == "" || value == "" {
		return false
	}
	if !strings.Contains(pattern, "*") {
		return pattern == value
	}

	parts := strings.Split(pattern, "*")
	if prefix := parts[0]; prefix != "" {
		if !strings.HasPrefix(value, prefix) {
			return false
		}
		value = value[len(prefix):]
	}
	if suffix := parts[len(parts)-1]; suffix != "" {
		if !strings.HasSuffix(value, suffix) {
			return false
		}
		value = value[:len(value)-len(suffix)]
	}
	for i := 1; i < len(parts)-1; i++ {
		segment := parts[i]
		if segment == "" {
			continue
		}
		idx := strings.Index(value, segment)
		if idx < 0 {
			return false
		}
		value = value[idx+len(segment):]
	}
	return true
}

// StripThinkingVariant maps "-thinking" models to their non-thinking base.
// This helps apply shared limits across thinking and non-thinking variants when configured.
func StripThinkingVariant(modelKey string) string {
	trimmed := strings.ToLower(strings.TrimSpace(modelKey))
	return strings.TrimSuffix(trimmed, claudeThinkingSuffixLiteral)
}
