package management

import (
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/policy"
)

func (h *Handler) GetAPIKeyDailyUsage(c *gin.Context) {
	if h == nil || h.billingStore == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "billing store unavailable"})
		return
	}

	apiKey := strings.TrimSpace(c.Query("api-key"))
	if apiKey == "" {
		apiKey = strings.TrimSpace(c.Query("apiKey"))
	}
	if apiKey == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "api-key is required"})
		return
	}

	day := strings.TrimSpace(c.Query("day"))
	if day == "" {
		day = policy.DayKeyChina(time.Now())
	}

	report, err := h.billingStore.GetDailyUsageReport(c.Request.Context(), apiKey, day)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"usage": report})
}
