package sessiontrajectory

import (
	"context"
	"sync"
	"testing"
)

type recorderStub struct {
	mu      sync.Mutex
	records []*CompletedRequest
}

func (s *recorderStub) Record(_ context.Context, record *CompletedRequest) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, record)
	return nil
}

func (s *recorderStub) Close() error { return nil }

func (s *recorderStub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

func TestAsyncRecorderSetEnabledSkipsNewRecords(t *testing.T) {
	store := &recorderStub{}
	recorder := NewAsyncRecorder(store, 4, 1)
	if recorder == nil {
		t.Fatal("expected recorder")
	}
	defer func() {
		if err := recorder.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
	}()

	recorder.SetEnabled(false)
	if recorder.IsEnabled() {
		t.Fatal("IsEnabled() = true, want false after SetEnabled(false)")
	}

	if err := recorder.Record(context.Background(), &CompletedRequest{RequestID: "req-disabled"}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	if got := store.count(); got != 0 {
		t.Fatalf("records = %d, want 0 while disabled", got)
	}

	recorder.SetEnabled(true)
	if !recorder.IsEnabled() {
		t.Fatal("IsEnabled() = false, want true after SetEnabled(true)")
	}

	if err := recorder.Record(context.Background(), &CompletedRequest{RequestID: "req-enabled"}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}

	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	if got := store.count(); got != 1 {
		t.Fatalf("records = %d, want 1 after re-enabling", got)
	}
}
