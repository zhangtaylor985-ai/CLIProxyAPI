package sessiontrajectory

import (
	"context"
	"sync"
	"sync/atomic"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/interfaces"
	log "github.com/sirupsen/logrus"
)

// AsyncRecorder decouples HTTP response completion from PG persistence by using
// a bounded in-process queue.
type AsyncRecorder struct {
	store   Recorder
	queue   chan *CompletedRequest
	wg      sync.WaitGroup
	once    sync.Once
	enabled atomic.Bool
}

func NewAsyncRecorder(store Recorder, queueSize int, workers int) *AsyncRecorder {
	if store == nil {
		return nil
	}
	if queueSize <= 0 {
		queueSize = defaultAsyncQueueSize
	}
	if workers <= 0 {
		workers = 1
	}
	recorder := &AsyncRecorder{
		store: store,
		queue: make(chan *CompletedRequest, queueSize),
	}
	recorder.enabled.Store(true)
	for worker := 0; worker < workers; worker++ {
		recorder.wg.Add(1)
		go recorder.runWorker()
	}
	return recorder
}

func (r *AsyncRecorder) Record(_ context.Context, record *CompletedRequest) error {
	if r == nil || record == nil || !r.IsEnabled() {
		return nil
	}
	cloned := cloneCompletedRequest(record)
	select {
	case r.queue <- cloned:
	default:
		log.WithFields(log.Fields{
			"request_id": record.RequestID,
			"url":        record.RequestURL,
		}).Warn("session trajectory queue full, dropping capture")
	}
	return nil
}

func (r *AsyncRecorder) SetEnabled(enabled bool) {
	if r == nil {
		return
	}
	r.enabled.Store(enabled)
}

func (r *AsyncRecorder) IsEnabled() bool {
	if r == nil {
		return false
	}
	return r.enabled.Load()
}

func (r *AsyncRecorder) Close() error {
	if r == nil {
		return nil
	}
	var err error
	r.once.Do(func() {
		close(r.queue)
		r.wg.Wait()
		err = r.store.Close()
	})
	return err
}

func (r *AsyncRecorder) runWorker() {
	defer r.wg.Done()
	for record := range r.queue {
		if err := r.store.Record(context.Background(), record); err != nil {
			log.WithError(err).WithField("request_id", record.RequestID).Warn("failed to persist session trajectory")
		}
	}
}

func cloneCompletedRequest(record *CompletedRequest) *CompletedRequest {
	if record == nil {
		return nil
	}
	cloned := *record
	cloned.RequestHeaders = cloneHeaders(record.RequestHeaders)
	cloned.ResponseHeaders = cloneHeaders(record.ResponseHeaders)
	cloned.RequestBody = append([]byte(nil), record.RequestBody...)
	cloned.ResponseBody = append([]byte(nil), record.ResponseBody...)
	cloned.APIRequestBody = append([]byte(nil), record.APIRequestBody...)
	cloned.APIResponseBody = append([]byte(nil), record.APIResponseBody...)
	if len(record.APIResponseErrors) > 0 {
		cloned.APIResponseErrors = append([]*interfaces.ErrorMessage(nil), record.APIResponseErrors...)
	}
	return &cloned
}

func cloneHeaders(headers map[string][]string) map[string][]string {
	if len(headers) == 0 {
		return nil
	}
	cloned := make(map[string][]string, len(headers))
	for key, values := range headers {
		copied := make([]string, len(values))
		copy(copied, values)
		cloned[key] = copied
	}
	return cloned
}
