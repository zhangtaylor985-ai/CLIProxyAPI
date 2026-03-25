package usageidentity

import (
	"encoding/hex"
	"hash/fnv"
	"regexp"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
)

const (
	sourcePrefixKey    = "k:"
	sourcePrefixMasked = "m:"
	sourcePrefixText   = "t:"
)

var (
	keyLikeTokenPattern = regexp.MustCompile(`(sk-[A-Za-z0-9-_]{6,}|sk-ant-[A-Za-z0-9-_]{6,}|AIza[0-9A-Za-z-_]{8,}|AI[a-zA-Z0-9_-]{6,}|hf_[A-Za-z0-9]{6,}|pk_[A-Za-z0-9]{6,}|rk_[A-Za-z0-9]{6,})`)
	maskedTokenHint     = regexp.MustCompile(`^[^\s]{1,24}(\*{2,}|\.{3}|…)[^\s]{1,24}$`)
	queryTokenPattern   = regexp.MustCompile(`(?i)(?:[?&])(api[-_]?key|key|token|access_token|authorization)=([^&#\s]+)`)
	headerTokenPattern  = regexp.MustCompile(`(?i)(api[-_]?key|key|token|access[-_]?token|authorization)\s*[:=]\s*([A-Za-z0-9._=-]+)`)
	bearerTokenPattern  = regexp.MustCompile(`(?i)\bBearer\s+([A-Za-z0-9._=-]{6,})`)
)

func NormalizeSourceID(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}

	if extracted := extractRawSecretFromText(trimmed); extracted != "" {
		return sourcePrefixKey + fnv1a64Hex(extracted)
	}

	if maskedTokenHint.MatchString(trimmed) {
		return sourcePrefixMasked + util.HideAPIKey(trimmed)
	}

	return sourcePrefixText + trimmed
}

func BuildCandidateSourceIDs(apiKey, prefix string) []string {
	seen := make(map[string]struct{}, 3)
	result := make([]string, 0, 3)

	if trimmedPrefix := strings.TrimSpace(prefix); trimmedPrefix != "" {
		key := sourcePrefixText + trimmedPrefix
		seen[key] = struct{}{}
		result = append(result, key)
	}

	if trimmedKey := strings.TrimSpace(apiKey); trimmedKey != "" {
		keyID := sourcePrefixKey + fnv1a64Hex(trimmedKey)
		if _, ok := seen[keyID]; !ok {
			seen[keyID] = struct{}{}
			result = append(result, keyID)
		}

		maskedID := sourcePrefixMasked + util.HideAPIKey(trimmedKey)
		if _, ok := seen[maskedID]; !ok {
			seen[maskedID] = struct{}{}
			result = append(result, maskedID)
		}
	}

	return result
}

func extractRawSecretFromText(text string) string {
	if text == "" {
		return ""
	}
	if looksLikeRawSecret(text) {
		return text
	}

	if match := keyLikeTokenPattern.FindString(text); match != "" {
		return match
	}

	if matches := queryTokenPattern.FindStringSubmatch(text); len(matches) >= 3 && looksLikeRawSecret(matches[2]) {
		return matches[2]
	}

	if matches := headerTokenPattern.FindStringSubmatch(text); len(matches) >= 3 && looksLikeRawSecret(matches[2]) {
		return matches[2]
	}

	if matches := bearerTokenPattern.FindStringSubmatch(text); len(matches) >= 2 && looksLikeRawSecret(matches[1]) {
		return matches[1]
	}

	return ""
}

func looksLikeRawSecret(text string) bool {
	if text == "" || strings.ContainsAny(text, " \t\r\n") {
		return false
	}

	lower := strings.ToLower(text)
	if strings.HasSuffix(lower, ".json") {
		return false
	}
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return false
	}
	if strings.ContainsAny(text, `/\`) {
		return false
	}

	if keyLikeTokenPattern.MatchString(text) {
		return true
	}

	if len(text) >= 32 && len(text) <= 512 {
		return true
	}

	if len(text) >= 16 && len(text) < 32 {
		alphaNumish := true
		hasLetter := false
		hasDigit := false
		for _, r := range text {
			switch {
			case r >= 'a' && r <= 'z':
				hasLetter = true
			case r >= 'A' && r <= 'Z':
				hasLetter = true
			case r >= '0' && r <= '9':
				hasDigit = true
			case r == '.' || r == '_' || r == '=' || r == '-':
			default:
				alphaNumish = false
			}
		}
		if alphaNumish && hasLetter && hasDigit {
			return true
		}
	}

	return false
}

func fnv1a64Hex(value string) string {
	hasher := fnv.New64a()
	_, _ = hasher.Write([]byte(value))
	return hex.EncodeToString(hasher.Sum(nil))
}
