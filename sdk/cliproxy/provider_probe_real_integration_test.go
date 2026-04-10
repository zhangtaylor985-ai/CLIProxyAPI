package cliproxy

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/joho/godotenv"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/registry"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v6/sdk/config"
)

const (
	realProbeTestEnv              = "RUN_REAL_PROVIDER_PROBE_TEST"
	realProbeTargetBaseURL        = "https://capi.quan2go.com/openai"
	realProbeAuthWaitTimeout      = 2 * time.Minute
	realProbeFirstProbeTimeout    = 6 * time.Minute
	realProbeSecondProbeTimeout   = 5 * time.Minute
	realProbeSecondCanaryTimeout  = 10 * time.Minute
	realProbePollInterval         = 2 * time.Second
	realProbeExpectedProbeMinGap  = 90 * time.Second
	realProbeExpectedProbeMaxGap  = 4 * time.Minute
	realProbeExpectedCanaryMinGap = 5 * time.Minute
	realProbeExpectedCanaryMaxGap = 8 * time.Minute
)

type realProbeSnapshot struct {
	AuthID               string
	Model                string
	LastProbeAt          time.Time
	LastProbeLatencyMs   int64
	LastProbeSlow        bool
	LastProbeError       string
	LastCanaryAt         time.Time
	LastCanaryLatencyMs  int64
	LastCanarySlow       bool
	LastCanaryError      string
	NextRetryAfter       time.Time
	ConsecutiveSlowProbe int
	ConsecutiveSlowCan   int
}

func TestRealProviderProbeTiming(t *testing.T) {
	if os.Getenv(realProbeTestEnv) != "1" {
		t.Skipf("set %s=1 to run real provider probe timing validation", realProbeTestEnv)
	}

	repoRoot := realProbeRepoRoot(t)
	_ = godotenv.Load(filepath.Join(repoRoot, ".env"))

	configPath := filepath.Join(repoRoot, "config.yaml")
	cfg, err := sdkconfig.LoadConfig(configPath)
	if err != nil {
		t.Fatalf("LoadConfig(%q): %v", configPath, err)
	}

	service, err := NewBuilder().
		WithConfig(cfg).
		WithConfigPath(configPath).
		Build()
	if err != nil {
		t.Fatalf("Build service: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	errCh := make(chan error, 1)
	go func() {
		errCh <- service.Run(ctx)
	}()

	authID, err := waitForRealProbeAuthID(t, service, realProbeAuthWaitTimeout)
	if err != nil {
		cancel()
		realProbeWaitForShutdown(t, errCh)
		t.Fatalf("wait for target auth: %v", err)
	}
	t.Logf("observing auth_id=%s base_url=%s", authID, realProbeTargetBaseURL)

	probeModel, err := primeRealProbeModelState(service, authID)
	if err != nil {
		cancel()
		realProbeWaitForShutdown(t, errCh)
		t.Fatalf("prime probe model state: %v", err)
	}
	t.Logf("primed probe model=%s for auth_id=%s", probeModel, authID)

	first, err := waitForRealProbeSnapshot(t, service, authID, func(s realProbeSnapshot) bool {
		return !s.LastProbeAt.IsZero() && !s.LastCanaryAt.IsZero()
	}, realProbeFirstProbeTimeout)
	if err != nil {
		cancel()
		realProbeWaitForShutdown(t, errCh)
		t.Fatalf("wait for initial probe/canary: %v", err)
	}
	t.Logf("initial snapshot: probe_at=%s probe_latency_ms=%d probe_slow=%t probe_error=%q canary_at=%s canary_latency_ms=%d canary_slow=%t canary_error=%q",
		first.LastProbeAt.Format(time.RFC3339), first.LastProbeLatencyMs, first.LastProbeSlow, first.LastProbeError,
		first.LastCanaryAt.Format(time.RFC3339), first.LastCanaryLatencyMs, first.LastCanarySlow, first.LastCanaryError)

	secondProbe, err := waitForRealProbeSnapshot(t, service, authID, func(s realProbeSnapshot) bool {
		return s.LastProbeAt.After(first.LastProbeAt)
	}, realProbeSecondProbeTimeout)
	if err != nil {
		cancel()
		realProbeWaitForShutdown(t, errCh)
		t.Fatalf("wait for second probe tick: %v", err)
	}
	probeGap := secondProbe.LastProbeAt.Sub(first.LastProbeAt)
	t.Logf("second probe snapshot: probe_at=%s gap=%s probe_latency_ms=%d probe_slow=%t probe_error=%q",
		secondProbe.LastProbeAt.Format(time.RFC3339), probeGap, secondProbe.LastProbeLatencyMs, secondProbe.LastProbeSlow, secondProbe.LastProbeError)
	if probeGap < realProbeExpectedProbeMinGap || probeGap > realProbeExpectedProbeMaxGap {
		cancel()
		realProbeWaitForShutdown(t, errCh)
		t.Fatalf("probe gap = %s, want within [%s, %s]", probeGap, realProbeExpectedProbeMinGap, realProbeExpectedProbeMaxGap)
	}

	secondCanary, err := waitForRealProbeSnapshot(t, service, authID, func(s realProbeSnapshot) bool {
		return s.LastCanaryAt.After(first.LastCanaryAt)
	}, realProbeSecondCanaryTimeout)
	if err != nil {
		cancel()
		realProbeWaitForShutdown(t, errCh)
		t.Fatalf("wait for second canary tick: %v", err)
	}
	canaryGap := secondCanary.LastCanaryAt.Sub(first.LastCanaryAt)
	t.Logf("second canary snapshot: canary_at=%s gap=%s canary_latency_ms=%d canary_slow=%t canary_error=%q next_retry_after=%s",
		secondCanary.LastCanaryAt.Format(time.RFC3339), canaryGap, secondCanary.LastCanaryLatencyMs, secondCanary.LastCanarySlow, secondCanary.LastCanaryError,
		secondCanary.NextRetryAfter.Format(time.RFC3339))
	if canaryGap < realProbeExpectedCanaryMinGap || canaryGap > realProbeExpectedCanaryMaxGap {
		cancel()
		realProbeWaitForShutdown(t, errCh)
		t.Fatalf("canary gap = %s, want within [%s, %s]", canaryGap, realProbeExpectedCanaryMinGap, realProbeExpectedCanaryMaxGap)
	}

	cancel()
	realProbeWaitForShutdown(t, errCh)
}

func realProbeRepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func waitForRealProbeAuthID(t *testing.T, service *Service, timeout time.Duration) (string, error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		select {
		case <-time.After(realProbePollInterval):
		}
		authIDs := make([]string, 0)
		for _, auth := range service.coreManager.List() {
			if auth == nil {
				continue
			}
			if !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") {
				continue
			}
			if strings.TrimSpace(auth.Attributes["base_url"]) != realProbeTargetBaseURL {
				continue
			}
			authIDs = append(authIDs, strings.TrimSpace(auth.ID))
		}
		if len(authIDs) > 0 {
			sort.Strings(authIDs)
			return authIDs[0], nil
		}
	}
	return "", errors.New("target codex auth not registered before timeout")
}

func waitForRealProbeSnapshot(t *testing.T, service *Service, authID string, predicate func(realProbeSnapshot) bool, timeout time.Duration) (realProbeSnapshot, error) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	var last realProbeSnapshot
	for time.Now().Before(deadline) {
		snapshot, ok := currentRealProbeSnapshot(service, authID)
		if ok {
			last = snapshot
			if predicate == nil || predicate(snapshot) {
				return snapshot, nil
			}
		}
		time.Sleep(realProbePollInterval)
	}
	if last.AuthID != "" {
		return last, errors.New("predicate not satisfied before timeout")
	}
	return realProbeSnapshot{}, errors.New("auth snapshot unavailable before timeout")
}

func currentRealProbeSnapshot(service *Service, authID string) (realProbeSnapshot, bool) {
	if service == nil || service.coreManager == nil {
		return realProbeSnapshot{}, false
	}
	auth, ok := service.coreManager.GetByID(authID)
	if !ok || auth == nil {
		return realProbeSnapshot{}, false
	}
	model := realProbeSnapshotModel(auth)
	if model == "" {
		return realProbeSnapshot{}, false
	}
	state := auth.ModelStates[strings.TrimSpace(model)]
	if state == nil {
		return realProbeSnapshot{AuthID: auth.ID, Model: model}, true
	}
	health := state.Health
	return realProbeSnapshot{
		AuthID:               auth.ID,
		Model:                model,
		LastProbeAt:          health.LastProbeAt,
		LastProbeLatencyMs:   health.LastProbeLatencyMs,
		LastProbeSlow:        health.LastProbeSlow,
		LastProbeError:       health.LastProbeError,
		LastCanaryAt:         health.LastCanaryAt,
		LastCanaryLatencyMs:  health.LastCanaryLatencyMs,
		LastCanarySlow:       health.LastCanarySlow,
		LastCanaryError:      health.LastCanaryError,
		NextRetryAfter:       state.NextRetryAfter,
		ConsecutiveSlowProbe: health.ConsecutiveSlowProbes,
		ConsecutiveSlowCan:   health.ConsecutiveSlowCanaries,
	}, true
}

func realProbeWaitForShutdown(t *testing.T, errCh <-chan error) {
	t.Helper()
	select {
	case err := <-errCh:
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("service shutdown returned error: %v", err)
		}
	case <-time.After(45 * time.Second):
		t.Fatal("timed out waiting for service shutdown")
	}
}

func primeRealProbeModelState(service *Service, authID string) (string, error) {
	if service == nil || service.coreManager == nil {
		return "", errors.New("service core manager unavailable")
	}
	auth, ok := service.coreManager.GetByID(authID)
	if !ok || auth == nil {
		return "", errors.New("auth not found")
	}

	service.ensureExecutorsForAuth(auth)
	service.registerModelsForAuth(auth)

	model := firstRegistryModelID(auth.ID)
	if model == "" {
		model = firstCodexRegistryModelID()
	}
	if model == "" {
		return "", errors.New("no codex model available for probe priming")
	}

	auth = auth.Clone()
	if auth.ModelStates == nil {
		auth.ModelStates = make(map[string]*coreauth.ModelState)
	}
	state := auth.ModelStates[model]
	if state == nil {
		state = &coreauth.ModelState{}
		auth.ModelStates[model] = state
	}
	state.Status = coreauth.StatusActive
	state.UpdatedAt = time.Now().UTC()
	if _, err := service.coreManager.Update(context.Background(), auth); err != nil {
		return "", err
	}
	service.coreManager.RefreshSchedulerEntry(auth.ID)
	return model, nil
}

func realProbeSnapshotModel(auth *coreauth.Auth) string {
	if auth == nil || len(auth.ModelStates) == 0 {
		return ""
	}
	models := make([]string, 0, len(auth.ModelStates))
	for model := range auth.ModelStates {
		if trimmed := strings.TrimSpace(model); trimmed != "" {
			models = append(models, trimmed)
		}
	}
	if len(models) == 0 {
		return ""
	}
	sort.Strings(models)
	return models[0]
}

func firstRegistryModelID(authID string) string {
	models := registry.GetGlobalRegistry().GetModelsForClient(strings.TrimSpace(authID))
	for _, model := range models {
		if model == nil {
			continue
		}
		if trimmed := strings.TrimSpace(model.ID); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstCodexRegistryModelID() string {
	for _, model := range registry.GetCodexProModels() {
		if model == nil {
			continue
		}
		if trimmed := strings.TrimSpace(model.ID); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

var _ = coreauth.StatusActive
