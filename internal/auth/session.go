package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"agentmem/internal/database"
	models "agentmem/internal/models"
)

var ErrSessionExpired = errors.New("session expired or not found")

type SessionStore interface {
	Create(ctx context.Context, userID string, ttl time.Duration) (token string, err error)
	Validate(ctx context.Context, token string) (*models.Session, error)
	Delete(ctx context.Context, token string) error
	DeleteAllForUser(ctx context.Context, userID string) error
}

// --- Postgres implementation ---

type pgSessionStore struct {
	db *database.DB
}

func NewPgSessionStore(db *database.DB) SessionStore {
	return &pgSessionStore{db: db}
}

func (s *pgSessionStore) Create(ctx context.Context, userID string, ttl time.Duration) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := hex.EncodeToString(raw)
	hash := hashToken(token)
	expiresAt := time.Now().UTC().Add(ttl)

	_, err := s.db.ExecContext(ctx,
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		hash, userID, expiresAt)
	if err != nil {
		return "", err
	}
	return token, nil
}

func (s *pgSessionStore) Validate(ctx context.Context, token string) (*models.Session, error) {
	hash := hashToken(token)
	var sess models.Session
	err := s.db.QueryRowContext(ctx,
		`SELECT token_hash, user_id, expires_at, created_at
		 FROM sessions WHERE token_hash = $1`, hash,
	).Scan(&sess.TokenHash, &sess.UserID, &sess.ExpiresAt, &sess.CreatedAt)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, ErrSessionExpired
		}
		return nil, err
	}
	if time.Now().UTC().After(sess.ExpiresAt) {
		_ = s.Delete(ctx, token)
		return nil, ErrSessionExpired
	}
	return &sess, nil
}

func (s *pgSessionStore) Delete(ctx context.Context, token string) error {
	hash := hashToken(token)
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = $1`, hash)
	return err
}

func (s *pgSessionStore) DeleteAllForUser(ctx context.Context, userID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE user_id = $1`, userID)
	return err
}

// --- In-memory LRU cache wrapper ---

type cacheEntry struct {
	session   *models.Session
	cachedAt  time.Time
}

type CachedSessionStore struct {
	inner    SessionStore
	cacheTTL time.Duration

	mu    sync.RWMutex
	cache map[string]*cacheEntry
	order []string // tracks insertion order for eviction
	cap   int
}

func NewCachedSessionStore(inner SessionStore, cacheTTL time.Duration, capacity int) SessionStore {
	if capacity <= 0 {
		capacity = 1000
	}
	return &CachedSessionStore{
		inner:    inner,
		cacheTTL: cacheTTL,
		cache:    make(map[string]*cacheEntry, capacity),
		order:    make([]string, 0, capacity),
		cap:      capacity,
	}
}

func (c *CachedSessionStore) Create(ctx context.Context, userID string, ttl time.Duration) (string, error) {
	return c.inner.Create(ctx, userID, ttl)
}

func (c *CachedSessionStore) Validate(ctx context.Context, token string) (*models.Session, error) {
	hash := hashToken(token)

	c.mu.RLock()
	entry, ok := c.cache[hash]
	c.mu.RUnlock()

	if ok && time.Since(entry.cachedAt) < c.cacheTTL {
		if time.Now().UTC().After(entry.session.ExpiresAt) {
			c.evict(hash)
			return nil, ErrSessionExpired
		}
		return entry.session, nil
	}

	sess, err := c.inner.Validate(ctx, token)
	if err != nil {
		c.evict(hash)
		return nil, err
	}

	c.put(hash, sess)
	return sess, nil
}

func (c *CachedSessionStore) Delete(ctx context.Context, token string) error {
	c.evict(hashToken(token))
	return c.inner.Delete(ctx, token)
}

func (c *CachedSessionStore) DeleteAllForUser(ctx context.Context, userID string) error {
	c.mu.Lock()
	for k, v := range c.cache {
		if v.session.UserID == userID {
			delete(c.cache, k)
		}
	}
	c.mu.Unlock()
	return c.inner.DeleteAllForUser(ctx, userID)
}

func (c *CachedSessionStore) put(hash string, sess *models.Session) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, exists := c.cache[hash]; !exists {
		if len(c.cache) >= c.cap && len(c.order) > 0 {
			oldest := c.order[0]
			c.order = c.order[1:]
			delete(c.cache, oldest)
		}
		c.order = append(c.order, hash)
	}
	c.cache[hash] = &cacheEntry{session: sess, cachedAt: time.Now()}
}

func (c *CachedSessionStore) evict(hash string) {
	c.mu.Lock()
	delete(c.cache, hash)
	c.mu.Unlock()
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
