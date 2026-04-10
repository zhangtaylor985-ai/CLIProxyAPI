package management

import (
	"context"
	"strings"
	"sync"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/managementauth"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
)

type memoryAuthStore struct {
	mu    sync.Mutex
	items map[string]*coreauth.Auth
}

func (s *memoryAuthStore) List(_ context.Context) ([]*coreauth.Auth, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	out := make([]*coreauth.Auth, 0, len(s.items))
	for _, item := range s.items {
		out = append(out, item)
	}
	return out, nil
}

func (s *memoryAuthStore) Save(_ context.Context, auth *coreauth.Auth) (string, error) {
	if auth == nil {
		return "", nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.items == nil {
		s.items = make(map[string]*coreauth.Auth)
	}
	s.items[auth.ID] = auth
	return auth.ID, nil
}

func (s *memoryAuthStore) Delete(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.items, id)
	return nil
}

func (s *memoryAuthStore) SetBaseDir(string) {}

type memoryManagementUserStore struct {
	mu    sync.Mutex
	items map[string]managementauth.User
}

func (s *memoryManagementUserStore) Close() error { return nil }

func (s *memoryManagementUserStore) EnsureSchema(context.Context) error { return nil }

func (s *memoryManagementUserStore) SeedDefaults(context.Context) error { return nil }

func (s *memoryManagementUserStore) GetByUsername(_ context.Context, username string) (managementauth.User, bool, error) {
	if s == nil {
		return managementauth.User{}, false, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[strings.TrimSpace(username)]
	return item, ok, nil
}
