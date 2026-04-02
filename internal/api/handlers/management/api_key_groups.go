package management

import (
	"database/sql"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/apikeygroup"
)

type apiKeyGroupView struct {
	ID              string    `json:"id"`
	Name            string    `json:"name"`
	DailyBudgetUSD  float64   `json:"daily_budget_usd"`
	WeeklyBudgetUSD float64   `json:"weekly_budget_usd"`
	IsSystem        bool      `json:"is_system"`
	MemberCount     int       `json:"member_count"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`
}

func (h *Handler) ListAPIKeyGroups(c *gin.Context) {
	if h == nil || h.groupStore == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "api key group store unavailable"})
		return
	}
	items, err := h.groupStore.ListGroups(c.Request.Context())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	result := make([]apiKeyGroupView, 0, len(items))
	for _, item := range items {
		result = append(result, h.groupToView(item))
	}
	c.JSON(http.StatusOK, gin.H{"items": result})
}

func (h *Handler) CreateAPIKeyGroup(c *gin.Context) {
	if h == nil || h.groupStore == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "api key group store unavailable"})
		return
	}
	var body struct {
		ID              string  `json:"id"`
		Name            string  `json:"name"`
		DailyBudgetUSD  float64 `json:"daily_budget_usd"`
		WeeklyBudgetUSD float64 `json:"weekly_budget_usd"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	group := apikeygroup.Group{
		ID:                   normalizeGroupID(body.ID, body.Name),
		Name:                 strings.TrimSpace(body.Name),
		DailyBudgetMicroUSD:  billingUSDToMicro(body.DailyBudgetUSD),
		WeeklyBudgetMicroUSD: billingUSDToMicro(body.WeeklyBudgetUSD),
	}
	saved, err := h.groupStore.UpsertGroup(c.Request.Context(), group)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, h.groupToView(saved))
}

func (h *Handler) UpdateAPIKeyGroup(c *gin.Context) {
	if h == nil || h.groupStore == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "api key group store unavailable"})
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id is required"})
		return
	}
	current, ok, err := h.groupStore.GetGroup(c.Request.Context(), id)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "group not found"})
		return
	}
	var body struct {
		Name            *string  `json:"name"`
		DailyBudgetUSD  *float64 `json:"daily_budget_usd"`
		WeeklyBudgetUSD *float64 `json:"weekly_budget_usd"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	if body.Name != nil {
		current.Name = strings.TrimSpace(*body.Name)
	}
	if body.DailyBudgetUSD != nil {
		current.DailyBudgetMicroUSD = billingUSDToMicro(*body.DailyBudgetUSD)
	}
	if body.WeeklyBudgetUSD != nil {
		current.WeeklyBudgetMicroUSD = billingUSDToMicro(*body.WeeklyBudgetUSD)
	}
	saved, err := h.groupStore.UpsertGroup(c.Request.Context(), current)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, h.groupToView(saved))
}

func (h *Handler) DeleteAPIKeyGroup(c *gin.Context) {
	if h == nil || h.groupStore == nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "api key group store unavailable"})
		return
	}
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "id is required"})
		return
	}
	if count := h.countGroupMembers(id); count > 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "group is still used by api keys"})
		return
	}
	if err := h.groupStore.DeleteGroup(c.Request.Context(), id); err != nil {
		status := http.StatusBadRequest
		if err == sql.ErrNoRows {
			status = http.StatusNotFound
		}
		c.JSON(status, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) groupToView(group apikeygroup.Group) apiKeyGroupView {
	return apiKeyGroupView{
		ID:              group.ID,
		Name:            group.Name,
		DailyBudgetUSD:  microToBillingUSD(group.DailyBudgetMicroUSD),
		WeeklyBudgetUSD: microToBillingUSD(group.WeeklyBudgetMicroUSD),
		IsSystem:        group.IsSystem,
		MemberCount:     h.countGroupMembers(group.ID),
		CreatedAt:       group.CreatedAt,
		UpdatedAt:       group.UpdatedAt,
	}
}

func (h *Handler) countGroupMembers(groupID string) int {
	if h == nil || h.cfg == nil || strings.TrimSpace(groupID) == "" {
		return 0
	}
	count := 0
	for _, item := range h.cfg.APIKeyPolicies {
		if strings.TrimSpace(item.GroupID) == strings.TrimSpace(groupID) {
			count++
		}
	}
	return count
}

func microToBillingUSD(value int64) float64 {
	return float64(value) / 1_000_000
}

func billingUSDToMicro(value float64) int64 {
	if value <= 0 {
		return 0
	}
	return int64(value*1_000_000 + 0.5)
}

func normalizeGroupID(rawID, fallbackName string) string {
	id := strings.TrimSpace(strings.ToLower(rawID))
	if id == "" {
		id = strings.TrimSpace(strings.ToLower(fallbackName))
	}
	if id == "" {
		return time.Now().UTC().Format("group-20060102150405")
	}
	builder := strings.Builder{}
	prevDash := false
	for _, ch := range id {
		valid := (ch >= 'a' && ch <= 'z') || (ch >= '0' && ch <= '9')
		if valid {
			builder.WriteRune(ch)
			prevDash = false
			continue
		}
		if !prevDash {
			builder.WriteByte('-')
			prevDash = true
		}
	}
	normalized := strings.Trim(builder.String(), "-")
	if normalized == "" {
		return time.Now().UTC().Format("group-20060102150405")
	}
	return normalized
}
