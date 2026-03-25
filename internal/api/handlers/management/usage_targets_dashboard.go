package management

import (
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/usagetargets"
)

func (h *Handler) GetUsageTargetsDashboard(c *gin.Context) {
	if h == nil || h.billingStore == nil {
		c.JSON(http.StatusOK, usagetargets.Dashboard{})
		return
	}

	now := time.Now()
	dailyRows, err := h.billingStore.ListDailyUsageRows(c.Request.Context(), "", "")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	aggregateRows, err := h.billingStore.ListUsageEventAggregateRows(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	recentRows, err := h.billingStore.ListUsageEvents(c.Request.Context(), now.Add(-200*time.Minute), now)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, usagetargets.Build(h.cfg, dailyRows, aggregateRows, recentRows, now))
}
