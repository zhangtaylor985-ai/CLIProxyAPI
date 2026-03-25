package requesttrace

import (
	"context"

	"github.com/gin-gonic/gin"
)

const apiKeyPolicyTraceContextKey = "apiKeyPolicyTrace"

// APIKeyPolicyTrace captures per-request API key policy decisions for logging.
type APIKeyPolicyTrace struct {
	APIKey          string
	FastModeEnabled bool
	FastModeApplied bool
	ServiceTier     string
	Model           string
	Source          string
}

func APIKeyPolicyTraceFromGin(c *gin.Context) *APIKeyPolicyTrace {
	if c == nil {
		return nil
	}
	value, exists := c.Get(apiKeyPolicyTraceContextKey)
	if !exists || value == nil {
		return nil
	}
	trace, ok := value.(*APIKeyPolicyTrace)
	if !ok || trace == nil {
		return nil
	}
	cloned := *trace
	return &cloned
}

func UpsertAPIKeyPolicyTraceOnGin(c *gin.Context, update func(*APIKeyPolicyTrace)) {
	if c == nil || update == nil {
		return
	}

	trace := APIKeyPolicyTraceFromGin(c)
	if trace == nil {
		trace = &APIKeyPolicyTrace{}
	}
	update(trace)
	c.Set(apiKeyPolicyTraceContextKey, trace)
}

func UpsertAPIKeyPolicyTraceOnContext(ctx context.Context, update func(*APIKeyPolicyTrace)) {
	if ctx == nil {
		return
	}
	ginCtx, _ := ctx.Value("gin").(*gin.Context)
	UpsertAPIKeyPolicyTraceOnGin(ginCtx, update)
}
