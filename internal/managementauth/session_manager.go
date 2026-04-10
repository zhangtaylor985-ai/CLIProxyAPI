package managementauth

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"
	"sync"
	"time"
)

const defaultSessionTTL = 24 * time.Hour

type Session struct {
	Token     string
	Username  string
	Role      Role
	ExpiresAt time.Time
	CreatedAt time.Time
}

type SessionManager struct {
	mu       sync.RWMutex
	sessions map[string]Session
	ttl      time.Duration
}

func NewSessionManager(ttl time.Duration) *SessionManager {
	if ttl <= 0 {
		ttl = defaultSessionTTL
	}
	manager := &SessionManager{
		sessions: make(map[string]Session),
		ttl:      ttl,
	}
	go manager.cleanupLoop()
	return manager
}

func (m *SessionManager) Create(username string, role Role) (Session, error) {
	if m == nil {
		return Session{}, fmt.Errorf("management session manager: not initialized")
	}
	token, err := newSessionToken()
	if err != nil {
		return Session{}, err
	}
	now := time.Now().UTC()
	session := Session{
		Token:     token,
		Username:  strings.TrimSpace(username),
		Role:      normalizeRole(role),
		CreatedAt: now,
		ExpiresAt: now.Add(m.ttl),
	}
	m.mu.Lock()
	m.sessions[token] = session
	m.mu.Unlock()
	return session, nil
}

func (m *SessionManager) Get(token string) (Session, bool) {
	if m == nil {
		return Session{}, false
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return Session{}, false
	}
	m.mu.RLock()
	session, ok := m.sessions[token]
	m.mu.RUnlock()
	if !ok {
		return Session{}, false
	}
	if time.Now().UTC().After(session.ExpiresAt) {
		m.Delete(token)
		return Session{}, false
	}
	return session, true
}

func (m *SessionManager) Delete(token string) {
	if m == nil {
		return
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return
	}
	m.mu.Lock()
	delete(m.sessions, token)
	m.mu.Unlock()
}

func (m *SessionManager) cleanupLoop() {
	ticker := time.NewTicker(30 * time.Minute)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now().UTC()
		m.mu.Lock()
		for token, session := range m.sessions {
			if now.After(session.ExpiresAt) {
				delete(m.sessions, token)
			}
		}
		m.mu.Unlock()
	}
}

func newSessionToken() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("management session manager: generate token: %w", err)
	}
	return "mgmt_session_" + base64.RawURLEncoding.EncodeToString(raw), nil
}
