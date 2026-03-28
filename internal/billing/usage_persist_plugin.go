package billing

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/policy"
	internalusage "github.com/router-for-me/CLIProxyAPI/v6/internal/usage"
	coreusage "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/usage"
	log "github.com/sirupsen/logrus"
)

const (
	usagePersistTimeout       = 5 * time.Second
	usagePersistFlushInterval = 5 * time.Second
	usagePersistMaxBatchSize  = 256
	usagePersistStopTimeout   = 10 * time.Second
)

type UsagePersistPlugin struct {
	store *SQLiteStore

	flushInterval time.Duration
	maxBatchSize  int

	signalCh chan struct{}
	stopCh   chan struct{}
	doneCh   chan struct{}

	stopOnce sync.Once

	mu               sync.RWMutex
	stopped          bool
	pending          []usagePersistRecord
	pendingDailyCost map[string]map[string]int64
}

func NewUsagePersistPlugin(store *SQLiteStore) *UsagePersistPlugin {
	plugin := &UsagePersistPlugin{
		store:            store,
		flushInterval:    usagePersistFlushInterval,
		maxBatchSize:     usagePersistMaxBatchSize,
		signalCh:         make(chan struct{}, 1),
		stopCh:           make(chan struct{}),
		doneCh:           make(chan struct{}),
		pendingDailyCost: make(map[string]map[string]int64),
	}
	go plugin.run()
	return plugin
}

func (p *UsagePersistPlugin) HandleUsage(ctx context.Context, record coreusage.Record) {
	if p == nil || p.store == nil {
		return
	}
	item, err := p.buildPersistRecord(record)
	if err != nil {
		if err != errUsageRecordSkipped {
			log.WithError(err).Warn("billing usage persist: failed to normalize usage record")
		}
		return
	}

	queueLen := p.enqueue(item)
	if queueLen >= p.maxBatchSize {
		p.signalFlush()
	}
}

func (p *UsagePersistPlugin) Stop(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.stopOnce.Do(func() {
		close(p.stopCh)
	})
	if ctx == nil {
		<-p.doneCh
		return nil
	}
	select {
	case <-p.doneCh:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *UsagePersistPlugin) PendingDailyCostMicroUSD(apiKey, dayKey string) int64 {
	if p == nil {
		return 0
	}
	apiKey = strings.TrimSpace(apiKey)
	dayKey = strings.TrimSpace(dayKey)
	if apiKey == "" || dayKey == "" {
		return 0
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if daily, ok := p.pendingDailyCost[apiKey]; ok {
		return daily[dayKey]
	}
	return 0
}

func (p *UsagePersistPlugin) PendingDailyUsageRows(apiKey, dayKey string) []DailyUsageRow {
	if p == nil {
		return nil
	}
	apiKey = strings.TrimSpace(apiKey)
	dayKey = strings.TrimSpace(dayKey)
	if apiKey == "" || dayKey == "" {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()

	rowsByModel := make(map[string]DailyUsageRow)
	for _, item := range p.pending {
		if item.delta.APIKey != apiKey || item.dayKey != dayKey {
			continue
		}
		row := rowsByModel[item.delta.Model]
		row.APIKey = apiKey
		row.Model = item.delta.Model
		row.Day = dayKey
		row.Requests += item.delta.Requests
		row.FailedRequests += item.delta.FailedRequests
		row.InputTokens += item.delta.InputTokens
		row.OutputTokens += item.delta.OutputTokens
		row.ReasoningTokens += item.delta.ReasoningTokens
		row.CachedTokens += item.delta.CachedTokens
		row.TotalTokens += item.delta.TotalTokens
		row.CostMicroUSD += item.delta.CostMicroUSD
		row.UpdatedAt = nowUnixUTC()
		rowsByModel[item.delta.Model] = row
	}

	rows := make([]DailyUsageRow, 0, len(rowsByModel))
	for _, row := range rowsByModel {
		rows = append(rows, row)
	}
	return rows
}

func (p *UsagePersistPlugin) PendingCostMicroUSDByDayRange(apiKey, startDay, endDayExclusive string) int64 {
	if p == nil {
		return 0
	}
	apiKey = strings.TrimSpace(apiKey)
	startDay = strings.TrimSpace(startDay)
	endDayExclusive = strings.TrimSpace(endDayExclusive)
	if apiKey == "" || startDay == "" || endDayExclusive == "" {
		return 0
	}
	var total int64
	p.mu.RLock()
	defer p.mu.RUnlock()
	for dayKey, cost := range p.pendingDailyCost[apiKey] {
		if dayKey >= startDay && dayKey < endDayExclusive {
			total += cost
		}
	}
	return total
}

func (p *UsagePersistPlugin) PendingCostMicroUSDByTimeRange(apiKey string, startInclusive, endExclusive time.Time) int64 {
	if p == nil {
		return 0
	}
	apiKey = strings.TrimSpace(apiKey)
	if apiKey == "" || startInclusive.IsZero() || endExclusive.IsZero() {
		return 0
	}
	startUnix := startInclusive.Unix()
	endUnix := endExclusive.Unix()
	var total int64
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, item := range p.pending {
		if item.event.APIKey != apiKey {
			continue
		}
		if item.event.RequestedAt >= startUnix && item.event.RequestedAt < endUnix {
			total += item.event.CostMicroUSD
		}
	}
	return total
}

func (p *UsagePersistPlugin) PendingCostMicroUSDByModelPrefix(apiKey, modelPrefix string) int64 {
	if p == nil {
		return 0
	}
	apiKey = strings.TrimSpace(apiKey)
	modelPrefix = policy.NormaliseModelKey(modelPrefix)
	if apiKey == "" || modelPrefix == "" {
		return 0
	}
	var total int64
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, item := range p.pending {
		if item.delta.APIKey != apiKey {
			continue
		}
		if strings.HasPrefix(item.delta.Model, modelPrefix) {
			total += item.delta.CostMicroUSD
		}
	}
	return total
}

func (p *UsagePersistPlugin) MergePendingSnapshot(snapshot *internalusage.StatisticsSnapshot) {
	if p == nil || snapshot == nil {
		return
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	for _, item := range p.pending {
		addUsageEventToSnapshot(snapshot, item.event)
	}
}

func (p *UsagePersistPlugin) run() {
	defer close(p.doneCh)

	ticker := time.NewTicker(p.flushInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			p.flushWithTimeout(usagePersistTimeout)
		case <-p.signalCh:
			p.flushWithTimeout(usagePersistTimeout)
		case <-p.stopCh:
			p.markStopped()
			p.flushWithTimeout(usagePersistStopTimeout)
			return
		}
	}
}

func (p *UsagePersistPlugin) flushWithTimeout(timeout time.Duration) {
	if p == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := p.flushPending(ctx); err != nil {
		log.WithError(err).Warn("billing usage persist: failed to flush pending usage batch")
	}
}

func (p *UsagePersistPlugin) flushPending(ctx context.Context) error {
	if p == nil || p.store == nil {
		return nil
	}
	for {
		batch := p.peekBatch()
		if len(batch) == 0 {
			return nil
		}
		if err := p.store.AddUsageBatch(ctx, batch); err != nil {
			return err
		}
		p.markCommitted(batch)
	}
}

func (p *UsagePersistPlugin) peekBatch() []usagePersistRecord {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if len(p.pending) == 0 {
		return nil
	}
	limit := len(p.pending)
	if p.maxBatchSize > 0 && limit > p.maxBatchSize {
		limit = p.maxBatchSize
	}
	batch := make([]usagePersistRecord, limit)
	copy(batch, p.pending[:limit])
	return batch
}

func (p *UsagePersistPlugin) markCommitted(batch []usagePersistRecord) {
	if p == nil || len(batch) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(batch) > len(p.pending) {
		batch = batch[:len(p.pending)]
	}
	for _, item := range batch {
		p.subtractPendingCostLocked(item.delta.APIKey, item.dayKey, item.delta.CostMicroUSD)
	}
	p.pending = append([]usagePersistRecord(nil), p.pending[len(batch):]...)
}

func (p *UsagePersistPlugin) enqueue(item usagePersistRecord) int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.stopped {
		return len(p.pending)
	}
	p.pending = append(p.pending, item)
	p.addPendingCostLocked(item.delta.APIKey, item.dayKey, item.delta.CostMicroUSD)
	return len(p.pending)
}

func (p *UsagePersistPlugin) signalFlush() {
	if p == nil {
		return
	}
	select {
	case p.signalCh <- struct{}{}:
	default:
	}
}

func (p *UsagePersistPlugin) markStopped() {
	if p == nil {
		return
	}
	p.mu.Lock()
	p.stopped = true
	p.mu.Unlock()
}

func (p *UsagePersistPlugin) addPendingCostLocked(apiKey, dayKey string, cost int64) {
	if cost <= 0 {
		return
	}
	if p.pendingDailyCost[apiKey] == nil {
		p.pendingDailyCost[apiKey] = make(map[string]int64)
	}
	p.pendingDailyCost[apiKey][dayKey] += cost
}

func (p *UsagePersistPlugin) subtractPendingCostLocked(apiKey, dayKey string, cost int64) {
	if cost <= 0 {
		return
	}
	daily := p.pendingDailyCost[apiKey]
	if daily == nil {
		return
	}
	daily[dayKey] -= cost
	if daily[dayKey] <= 0 {
		delete(daily, dayKey)
	}
	if len(daily) == 0 {
		delete(p.pendingDailyCost, apiKey)
	}
}

var errUsageRecordSkipped = errors.New("billing usage persist: skip record")

func (p *UsagePersistPlugin) buildPersistRecord(record coreusage.Record) (usagePersistRecord, error) {
	if p == nil || p.store == nil {
		return usagePersistRecord{}, errUsageRecordSkipped
	}
	persistCtx, cancel := context.WithTimeout(context.Background(), usagePersistTimeout)
	defer cancel()

	apiKey := strings.TrimSpace(record.APIKey)
	if apiKey == "" {
		return usagePersistRecord{}, errUsageRecordSkipped
	}
	modelKey := policy.NormaliseModelKey(record.Model)
	if modelKey == "" {
		modelKey = "unknown"
	}

	ts := record.RequestedAt
	if ts.IsZero() {
		ts = time.Now()
	}
	dayKey := policy.DayKeyChina(ts)

	detail := record.Detail
	if detail.TotalTokens == 0 {
		detail.TotalTokens = detail.InputTokens + detail.OutputTokens + detail.ReasoningTokens + detail.CachedTokens
	}
	if detail.TotalTokens < 0 {
		detail.TotalTokens = 0
	}

	price, priceSource, _, err := p.store.ResolvePriceMicro(persistCtx, modelKey)
	if err != nil {
		return usagePersistRecord{}, err
	}
	if priceSource == "missing" {
		log.WithFields(log.Fields{
			"component": "billing",
			"api_key":   apiKey,
			"model":     modelKey,
		}).Warn("billing price missing for usage record; request will be tracked with zero cost")
	}
	cost := calculateUsageCostMicro(detail.InputTokens, detail.OutputTokens, detail.ReasoningTokens, detail.CachedTokens, price)

	return usagePersistRecord{
		dayKey: dayKey,
		delta: DailyUsageRow{
			APIKey:          apiKey,
			Model:           modelKey,
			Day:             dayKey,
			Requests:        1,
			FailedRequests:  boolToInt64(record.Failed),
			InputTokens:     max64(0, detail.InputTokens),
			OutputTokens:    max64(0, detail.OutputTokens),
			ReasoningTokens: max64(0, detail.ReasoningTokens),
			CachedTokens:    max64(0, detail.CachedTokens),
			TotalTokens:     max64(0, detail.TotalTokens),
			CostMicroUSD:    max64(0, cost),
		},
		event: UsageEventRow{
			RequestedAt:     ts.Unix(),
			APIKey:          apiKey,
			Source:          strings.TrimSpace(record.Source),
			AuthIndex:       strings.TrimSpace(record.AuthIndex),
			Model:           modelKey,
			Failed:          record.Failed,
			InputTokens:     max64(0, detail.InputTokens),
			OutputTokens:    max64(0, detail.OutputTokens),
			ReasoningTokens: max64(0, detail.ReasoningTokens),
			CachedTokens:    max64(0, detail.CachedTokens),
			TotalTokens:     max64(0, detail.TotalTokens),
			CostMicroUSD:    max64(0, cost),
		},
	}, nil
}

func boolToInt64(v bool) int64 {
	if v {
		return 1
	}
	return 0
}

func boolToSQLiteInt(v bool) int64 {
	if v {
		return 1
	}
	return 0
}
