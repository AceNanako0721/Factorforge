package application

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	d "github.com/AceNanako0721/Factorforge/src/factorforge/applications/console/domain"
	"strconv"
	"strings"
	"sync"
	"time"
)

type session struct {
	UserID, AuthVersion, CSRF string
	Expires                   time.Time
}
type challenge struct {
	CSRF, Peer string
	Expires    time.Time
}
type SessionService struct {
	mu            sync.Mutex
	users         map[string]d.User
	sessions      map[string]session
	challenges    map[string]challenge
	policy        d.Policy
	maxIterations int
	attempts      int
	rateUntil     time.Time
	now           func() time.Time
}

func ParseHash(hash string, maxIterations int) (int, []byte, []byte, error) {
	p := strings.Split(hash, "$")
	if len(p) != 5 || p[0] != "" || p[1] != "pbkdf2-sha256" {
		return 0, nil, nil, d.Fail("AUTH_CONFIGURATION_REQUIRED", 503)
	}
	n, e := strconv.Atoi(p[2])
	salt, e2 := hex.DecodeString(p[3])
	digest, e3 := hex.DecodeString(p[4])
	if e != nil || e2 != nil || e3 != nil || n < 1 || n > maxIterations || len(salt) < 16 || len(digest) != 32 {
		return 0, nil, nil, d.Fail("AUTH_CONFIGURATION_REQUIRED", 503)
	}
	return n, salt, digest, nil
}

// HashPassword is an operator utility. Iterations are explicit registered
// deployment settings; no production cost is chosen by the application.
func HashPassword(password string, iterations int) (string, error) {
	if password == "" || iterations < 1 {
		return "", d.Fail("PASSWORD_INVALID", 422)
	}
	salt := make([]byte, 32)
	if _, e := rand.Read(salt); e != nil {
		return "", e
	}
	key, e := pbkdf2.Key(sha256.New, password, salt, iterations, 32)
	if e != nil {
		return "", e
	}
	return "$pbkdf2-sha256$" + strconv.Itoa(iterations) + "$" + hex.EncodeToString(salt) + "$" + hex.EncodeToString(key), nil
}
func NewSessions(users []d.User, p d.Policy, maxIterations int, now func() time.Time) (*SessionService, error) {
	if !p.Valid() || maxIterations < 1 || now == nil || len(users) == 0 {
		return nil, d.Fail("AUTH_CONFIGURATION_REQUIRED", 503)
	}
	s := &SessionService{users: map[string]d.User{}, sessions: map[string]session{}, challenges: map[string]challenge{}, policy: p, maxIterations: maxIterations, now: now}
	for _, u := range users {
		if !u.Valid() || s.users[u.ID].ID != "" {
			return nil, d.Fail("AUTH_CONFIGURATION_REQUIRED", 503)
		}
		if _, _, _, e := ParseHash(u.PasswordHash, maxIterations); e != nil {
			return nil, e
		}
		for _, old := range s.users {
			if old.Username == u.Username {
				return nil, d.Fail("AUTH_CONFIGURATION_REQUIRED", 503)
			}
		}
		u.Capabilities = append([]string(nil), u.Capabilities...)
		u.SelectionIDs = append([]string(nil), u.SelectionIDs...)
		s.users[u.ID] = u
	}
	return s, nil
}
func randomID() string {
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
func (s *SessionService) clean(now time.Time) {
	for id, v := range s.sessions {
		if !now.Before(v.Expires) {
			delete(s.sessions, id)
		}
	}
	for id, v := range s.challenges {
		if !now.Before(v.Expires) {
			delete(s.challenges, id)
		}
	}
}
func (s *SessionService) Challenge(peer string) (string, d.Challenge, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	s.clean(now)
	if len(s.challenges) >= s.policy.MaxChallenges {
		return "", d.Challenge{}, d.RateLimited("AUTH_RATE_LIMITED", s.policy.ChallengeTTL)
	}
	id, csrf := randomID(), randomID()
	if id == "" || csrf == "" {
		return "", d.Challenge{}, d.Fail("AUTH_UNAVAILABLE", 503)
	}
	expires := now.Add(s.policy.ChallengeTTL)
	s.challenges[id] = challenge{csrf, peer, expires}
	return id, d.Challenge{CSRF: csrf, ExpiresAt: expires}, nil
}
func (s *SessionService) Login(id, peer, username, password, csrf string) (string, d.SessionView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	s.clean(now)
	ch, ok := s.challenges[id]
	delete(s.challenges, id)
	if !ok || ch.Peer != peer || subtle.ConstantTimeCompare([]byte(ch.CSRF), []byte(csrf)) != 1 {
		return "", d.SessionView{}, d.Fail("CSRF_REJECTED", 403)
	}
	if !now.Before(s.rateUntil) {
		s.attempts = 0
		s.rateUntil = now.Add(s.policy.RateWindow)
	}
	if s.attempts >= s.policy.MaxAttempts {
		return "", d.SessionView{}, d.RateLimited("AUTH_RATE_LIMITED", s.rateUntil.Sub(now))
	}
	s.attempts++
	var user d.User
	for _, u := range s.users {
		if u.Username == username {
			user = u
			break
		}
	}
	// Unknown users perform the same configured hash work as an existing user.
	probe := user
	if probe.ID == "" {
		for _, u := range s.users {
			probe = u
			break
		}
	}
	n, salt, digest, e := ParseHash(probe.PasswordHash, s.maxIterations)
	if e != nil {
		return "", d.SessionView{}, e
	}
	key, e := pbkdf2.Key(sha256.New, password, salt, n, 32)
	if e != nil || subtle.ConstantTimeCompare(key, digest) != 1 || user.ID == "" {
		return "", d.SessionView{}, d.Fail("AUTHENTICATION_FAILED", 401)
	}
	if len(s.sessions) >= s.policy.MaxSessions {
		return "", d.SessionView{}, d.RateLimited("AUTH_RATE_LIMITED", s.policy.SessionTTL)
	}
	sid, token := randomID(), randomID()
	if sid == "" || token == "" {
		return "", d.SessionView{}, d.Fail("AUTH_UNAVAILABLE", 503)
	}
	expires := now.Add(s.policy.SessionTTL)
	s.sessions[sid] = session{user.ID, user.AuthorizationVersion, token, expires}
	return sid, d.SessionView{UserID: user.ID, DisplayName: user.Name, Capabilities: append([]string(nil), user.Capabilities...), ExpiresAt: expires, CSRF: token}, nil
}
func (s *SessionService) Current(id string) (d.User, d.SessionView, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.now().UTC()
	s.clean(now)
	v, ok := s.sessions[id]
	u, exists := s.users[v.UserID]
	if !ok || !exists || u.AuthorizationVersion != v.AuthVersion {
		delete(s.sessions, id)
		return d.User{}, d.SessionView{}, d.Fail("AUTHENTICATION_REQUIRED", 401)
	}
	u.SelectionIDs = append([]string(nil), u.SelectionIDs...)
	u.Capabilities = append([]string(nil), u.Capabilities...)
	return u, d.SessionView{UserID: u.ID, DisplayName: u.Name, Capabilities: u.Capabilities, ExpiresAt: v.Expires, CSRF: v.CSRF}, nil
}
func (s *SessionService) Logout(id, csrf string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.sessions[id]
	if !ok || !s.now().Before(v.Expires) {
		delete(s.sessions, id)
		return d.Fail("AUTHENTICATION_REQUIRED", 401)
	}
	if subtle.ConstantTimeCompare([]byte(v.CSRF), []byte(csrf)) != 1 {
		return d.Fail("CSRF_REJECTED", 403)
	}
	delete(s.sessions, id)
	return nil
}

// Revoke invalidates existing sessions, rather than allowing a cookie to retain
// an old deployment grant. Restarts also naturally revoke in-memory sessions.
func (s *SessionService) Revoke(userID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.users, userID)
	for id, v := range s.sessions {
		if v.UserID == userID {
			delete(s.sessions, id)
		}
	}
}
