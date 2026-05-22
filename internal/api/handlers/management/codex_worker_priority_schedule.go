package management

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	log "github.com/sirupsen/logrus"
)

const (
	defaultCodexWorkerScheduleTimezone            = "Asia/Shanghai"
	defaultCodexWorkerScheduleStart               = "15:00"
	defaultCodexWorkerScheduleEnd                 = "17:30"
	defaultCodexWorkerScheduleAPIProviderBase     = "https://apibridge012.online"
	defaultCodexWorkerScheduleWorkerPriority      = 20
	defaultCodexWorkerScheduleAPIProviderPriority = 20
	defaultCodexWorkerScheduleSessionAffinityTTL  = "3h"
)

type codexWorkerPriorityScheduleView struct {
	config.CodexWorkerPriorityScheduleConfig
	Active            bool      `json:"active"`
	NextTransitionAt  string    `json:"next_transition_at,omitempty"`
	WorkerProviderIDs []string  `json:"worker_provider_ids,omitempty"`
	APIProviderNames  []string  `json:"api_provider_names,omitempty"`
	Now               time.Time `json:"now"`
}

func (h *Handler) startCodexWorkerPriorityScheduleLoop() {
	go func() {
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		for {
			<-timer.C
			h.applyCodexWorkerPriorityScheduleBackground()
			timer.Reset(time.Minute)
		}
	}()
}

func (h *Handler) applyCodexWorkerPriorityScheduleBackground() {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	changed, err := h.applyCodexWorkerPrioritySchedule(ctx, time.Now())
	if err != nil {
		log.WithError(err).Warn("failed to apply codex worker priority schedule")
		return
	}
	if changed {
		log.Info("codex worker priority schedule applied")
	}
}

func (h *Handler) GetCodexWorkerPrioritySchedule(c *gin.Context) {
	view, err := h.buildCodexWorkerPriorityScheduleView(c.Request.Context(), time.Now())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, view)
}

func (h *Handler) PutCodexWorkerPrioritySchedule(c *gin.Context) {
	var req config.CodexWorkerPriorityScheduleConfig
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid body"})
		return
	}
	req = normalizeCodexWorkerPrioritySchedule(req)
	if _, err := codexWorkerScheduleLocation(req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if _, _, err := codexWorkerScheduleTimes(req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	if _, err := time.ParseDuration(req.SessionAffinityTTL); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session_affinity_ttl"})
		return
	}
	h.mu.Lock()
	if h.cfg == nil {
		h.mu.Unlock()
		c.JSON(http.StatusInternalServerError, gin.H{"error": "config is unavailable"})
		return
	}
	h.cfg.CodexWorkerPrioritySchedule = req
	currentCfg := h.cfg
	if err := config.SaveConfigPreserveComments(h.configFilePath, currentCfg); err != nil {
		h.mu.Unlock()
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to save config: %v", err)})
		return
	}
	h.mu.Unlock()
	h.notifyConfigUpdated(currentCfg)

	if _, err := h.applyCodexWorkerPrioritySchedule(c.Request.Context(), time.Now()); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"status": "ok"})
}

func (h *Handler) buildCodexWorkerPriorityScheduleView(ctx context.Context, now time.Time) (codexWorkerPriorityScheduleView, error) {
	workers, err := h.loadCodexWorkerManagementConfigs()
	if err != nil {
		return codexWorkerPriorityScheduleView{}, err
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cfg == nil {
		return codexWorkerPriorityScheduleView{}, nil
	}
	schedule := normalizeCodexWorkerPrioritySchedule(h.cfg.CodexWorkerPrioritySchedule)
	view := codexWorkerPriorityScheduleView{
		CodexWorkerPriorityScheduleConfig: schedule,
		Now:                               now.UTC(),
	}
	active, next, err := codexWorkerScheduleState(now, schedule)
	if err != nil {
		return codexWorkerPriorityScheduleView{}, err
	}
	view.Active = active && schedule.Enabled
	if !next.IsZero() {
		view.NextTransitionAt = next.UTC().Format(time.RFC3339)
	}
	workerIndices := codexWorkerRouteProviderIndices(h.cfg.OpenAICompatibility, workers)
	for idx, workerID := range workerIndices {
		if idx >= 0 && idx < len(h.cfg.OpenAICompatibility) {
			view.WorkerProviderIDs = append(view.WorkerProviderIDs, workerID)
		}
	}
	apiIndices := codexWorkerScheduleAPIProviderIndices(h.cfg.OpenAICompatibility, schedule)
	for _, idx := range apiIndices {
		if idx >= 0 && idx < len(h.cfg.OpenAICompatibility) {
			view.APIProviderNames = append(view.APIProviderNames, h.cfg.OpenAICompatibility[idx].Name)
		}
	}
	for _, idx := range codexWorkerScheduleCodexProviderIndices(h.cfg.CodexKey, schedule) {
		if idx >= 0 && idx < len(h.cfg.CodexKey) {
			view.APIProviderNames = append(view.APIProviderNames, "codex-api-key")
		}
	}
	_ = ctx
	return view, nil
}

func (h *Handler) applyCodexWorkerPrioritySchedule(ctx context.Context, now time.Time) (bool, error) {
	workers, err := h.loadCodexWorkerManagementConfigs()
	if err != nil {
		return false, err
	}

	h.mu.Lock()
	if h.cfg == nil {
		h.mu.Unlock()
		return false, nil
	}
	schedule := normalizeCodexWorkerPrioritySchedule(h.cfg.CodexWorkerPrioritySchedule)
	if !schedule.Enabled {
		h.mu.Unlock()
		return false, nil
	}
	active, _, err := codexWorkerScheduleState(now, schedule)
	if err != nil {
		h.mu.Unlock()
		return false, err
	}

	changed := false
	workerPriority := schedule.OutsideWorkerPriority
	apiPriority := schedule.OutsideAPIProviderPriority
	if active {
		workerPriority = schedule.WindowWorkerPriority
		apiPriority = schedule.WindowAPIProviderPriority
	}

	workerIndices := codexWorkerRouteProviderIndices(h.cfg.OpenAICompatibility, workers)
	for idx := range workerIndices {
		if idx < 0 || idx >= len(h.cfg.OpenAICompatibility) {
			continue
		}
		if h.cfg.OpenAICompatibility[idx].Priority != workerPriority {
			h.cfg.OpenAICompatibility[idx].Priority = workerPriority
			changed = true
		}
	}
	for _, idx := range codexWorkerScheduleAPIProviderIndices(h.cfg.OpenAICompatibility, schedule) {
		if idx < 0 || idx >= len(h.cfg.OpenAICompatibility) {
			continue
		}
		if h.cfg.OpenAICompatibility[idx].Priority != apiPriority {
			h.cfg.OpenAICompatibility[idx].Priority = apiPriority
			changed = true
		}
	}
	for _, idx := range codexWorkerScheduleCodexProviderIndices(h.cfg.CodexKey, schedule) {
		if idx < 0 || idx >= len(h.cfg.CodexKey) {
			continue
		}
		if h.cfg.CodexKey[idx].Priority != apiPriority {
			h.cfg.CodexKey[idx].Priority = apiPriority
			changed = true
		}
	}
	if active {
		if !h.cfg.Routing.SessionAffinity || h.cfg.Routing.ClaudeCodeSessionAffinity {
			h.cfg.Routing.SessionAffinity = true
			h.cfg.Routing.ClaudeCodeSessionAffinity = false
			changed = true
		}
		if strings.TrimSpace(h.cfg.Routing.SessionAffinityTTL) != schedule.SessionAffinityTTL {
			h.cfg.Routing.SessionAffinityTTL = schedule.SessionAffinityTTL
			changed = true
		}
	} else if h.cfg.Routing.SessionAffinity || h.cfg.Routing.ClaudeCodeSessionAffinity {
		h.cfg.Routing.SessionAffinity = false
		h.cfg.Routing.ClaudeCodeSessionAffinity = false
		changed = true
	}
	if changed {
		h.cfg.CodexWorkerPrioritySchedule = schedule
		h.cfg.SanitizeOpenAICompatibility()
		h.cfg.SanitizeCodexKeys()
		currentCfg := h.cfg
		if err := config.SaveConfigPreserveComments(h.configFilePath, currentCfg); err != nil {
			h.mu.Unlock()
			return false, fmt.Errorf("failed to save config: %w", err)
		}
		h.mu.Unlock()
		h.notifyConfigUpdated(currentCfg)
		_ = ctx
		return true, nil
	}
	h.mu.Unlock()
	_ = ctx
	return false, nil
}

func normalizeCodexWorkerPrioritySchedule(in config.CodexWorkerPriorityScheduleConfig) config.CodexWorkerPriorityScheduleConfig {
	in.Timezone = strings.TrimSpace(in.Timezone)
	if in.Timezone == "" {
		in.Timezone = defaultCodexWorkerScheduleTimezone
	}
	in.StartTime = strings.TrimSpace(in.StartTime)
	if in.StartTime == "" {
		in.StartTime = defaultCodexWorkerScheduleStart
	}
	in.EndTime = strings.TrimSpace(in.EndTime)
	if in.EndTime == "" {
		in.EndTime = defaultCodexWorkerScheduleEnd
	}
	in.APIProviderBaseURL = sanitizeWorkerBaseURL(in.APIProviderBaseURL)
	if in.APIProviderBaseURL == "" {
		in.APIProviderBaseURL = defaultCodexWorkerScheduleAPIProviderBase
	}
	if in.WindowWorkerPriority == 0 {
		in.WindowWorkerPriority = defaultCodexWorkerScheduleWorkerPriority
	}
	if in.OutsideAPIProviderPriority == 0 {
		in.OutsideAPIProviderPriority = defaultCodexWorkerScheduleAPIProviderPriority
	}
	in.SessionAffinityTTL = strings.TrimSpace(in.SessionAffinityTTL)
	if in.SessionAffinityTTL == "" {
		in.SessionAffinityTTL = defaultCodexWorkerScheduleSessionAffinityTTL
	}
	return in
}

func codexWorkerScheduleLocation(schedule config.CodexWorkerPriorityScheduleConfig) (*time.Location, error) {
	loc, err := time.LoadLocation(strings.TrimSpace(schedule.Timezone))
	if err != nil {
		return nil, fmt.Errorf("invalid timezone")
	}
	return loc, nil
}

func codexWorkerScheduleTimes(schedule config.CodexWorkerPriorityScheduleConfig) (int, int, error) {
	start, err := parseCodexWorkerScheduleClock(schedule.StartTime)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid start_time")
	}
	end, err := parseCodexWorkerScheduleClock(schedule.EndTime)
	if err != nil {
		return 0, 0, fmt.Errorf("invalid end_time")
	}
	if start == end {
		return 0, 0, fmt.Errorf("start_time and end_time must differ")
	}
	return start, end, nil
}

func codexWorkerScheduleState(now time.Time, schedule config.CodexWorkerPriorityScheduleConfig) (bool, time.Time, error) {
	loc, err := codexWorkerScheduleLocation(schedule)
	if err != nil {
		return false, time.Time{}, err
	}
	startMin, endMin, err := codexWorkerScheduleTimes(schedule)
	if err != nil {
		return false, time.Time{}, err
	}
	localNow := now.In(loc)
	currentMin := localNow.Hour()*60 + localNow.Minute()
	active := currentMin >= startMin && currentMin < endMin
	if startMin > endMin {
		active = currentMin >= startMin || currentMin < endMin
	}
	nextMin := startMin
	if active {
		nextMin = endMin
	}
	next := time.Date(localNow.Year(), localNow.Month(), localNow.Day(), nextMin/60, nextMin%60, 0, 0, loc)
	if !next.After(localNow) {
		next = next.Add(24 * time.Hour)
	}
	return active, next, nil
}

func parseCodexWorkerScheduleClock(value string) (int, error) {
	parts := strings.Split(strings.TrimSpace(value), ":")
	if len(parts) != 2 {
		return 0, fmt.Errorf("invalid clock")
	}
	hour, err := parseSmallInt(parts[0])
	if err != nil || hour < 0 || hour > 23 {
		return 0, fmt.Errorf("invalid hour")
	}
	minute, err := parseSmallInt(parts[1])
	if err != nil || minute < 0 || minute > 59 {
		return 0, fmt.Errorf("invalid minute")
	}
	return hour*60 + minute, nil
}

func parseSmallInt(value string) (int, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, fmt.Errorf("empty")
	}
	out := 0
	for _, ch := range value {
		if ch < '0' || ch > '9' {
			return 0, fmt.Errorf("invalid")
		}
		out = out*10 + int(ch-'0')
	}
	return out, nil
}

func codexWorkerRouteProviderIndices(entries []config.OpenAICompatibility, workers []codexWorkerManagementConfig) map[int]string {
	out := make(map[int]string)
	for _, worker := range workers {
		idx, _ := findCodexWorkerRouteProviderIndex(entries, worker)
		if idx >= 0 {
			out[idx] = worker.ID
		}
	}
	return out
}

func codexWorkerScheduleAPIProviderIndices(entries []config.OpenAICompatibility, schedule config.CodexWorkerPriorityScheduleConfig) []int {
	targetBase := sanitizeWorkerBaseURL(schedule.APIProviderBaseURL)
	if targetBase == "" {
		return nil
	}
	out := make([]int, 0, 1)
	for i := range entries {
		if sanitizeWorkerBaseURL(entries[i].BaseURL) == targetBase {
			out = append(out, i)
		}
	}
	return out
}

func codexWorkerScheduleCodexProviderIndices(entries []config.CodexKey, schedule config.CodexWorkerPriorityScheduleConfig) []int {
	targetBase := sanitizeWorkerBaseURL(schedule.APIProviderBaseURL)
	if targetBase == "" {
		return nil
	}
	out := make([]int, 0, 1)
	for i := range entries {
		if sanitizeWorkerBaseURL(entries[i].BaseURL) == targetBase {
			out = append(out, i)
		}
	}
	return out
}
