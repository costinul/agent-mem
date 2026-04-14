package account

import (
	"context"
	"errors"
	"testing"
	"time"

	models "agentmem/internal/models"
	"agentmem/internal/repository/accountrepo"
)

func TestPrefixFromAPIKey(t *testing.T) {
	full, prefix, err := generateAPIKey()
	if err != nil {
		t.Fatalf("generateAPIKey() error = %v", err)
	}

	gotPrefix, err := prefixFromAPIKey(full)
	if err != nil {
		t.Fatalf("prefixFromAPIKey() error = %v", err)
	}
	if gotPrefix != prefix {
		t.Fatalf("prefixFromAPIKey() = %q, want %q", gotPrefix, prefix)
	}

	if _, err := prefixFromAPIKey("bad-format"); err == nil {
		t.Fatalf("prefixFromAPIKey() expected error for malformed key")
	}
}

func TestMatchesAPIKey(t *testing.T) {
	full, _, err := generateAPIKey()
	if err != nil {
		t.Fatalf("generateAPIKey() error = %v", err)
	}

	hash := hashAPIKey(full)
	if !matchesAPIKey(hash, full) {
		t.Fatalf("matchesAPIKey() = false, want true")
	}
	if matchesAPIKey(hash, full+"x") {
		t.Fatalf("matchesAPIKey() = true for wrong key, want false")
	}
}

func TestGenerateAndHashAPIKey_NoDatabaseHit(t *testing.T) {
	full, prefix, err := generateAPIKey()
	if err != nil {
		t.Fatalf("generateAPIKey() error = %v", err)
	}
	if full == "" || prefix == "" {
		t.Fatalf("generateAPIKey() returned empty values")
	}

	hash := hashAPIKey(full)
	if len(hash) != 64 {
		t.Fatalf("hashAPIKey() length = %d, want 64", len(hash))
	}

	derivedPrefix, err := prefixFromAPIKey(full)
	if err != nil {
		t.Fatalf("prefixFromAPIKey() error = %v", err)
	}
	if derivedPrefix != prefix {
		t.Fatalf("prefixFromAPIKey() = %q, want %q", derivedPrefix, prefix)
	}
	if !matchesAPIKey(hash, full) {
		t.Fatalf("matchesAPIKey() = false, want true")
	}
}

func TestIsAPIKeyUsable(t *testing.T) {
	now := time.Now().UTC()

	usable := &models.APIKey{Valid: true}
	if !isAPIKeyUsable(usable, now) {
		t.Fatalf("expected key to be usable")
	}

	invalid := &models.APIKey{Valid: false}
	if isAPIKeyUsable(invalid, now) {
		t.Fatalf("expected invalid key to be unusable")
	}

	expiredAt := now.Add(-time.Minute)
	expired := &models.APIKey{Valid: true, ExpiresAt: &expiredAt}
	if isAPIKeyUsable(expired, now) {
		t.Fatalf("expected expired key to be unusable")
	}
}

func TestInvalidateAPIKeyByPrefix(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		svc, repo := newMockService()
		repo.invalidateByPrefixFn = func(_ context.Context, prefix string) (bool, error) {
			if prefix != "abc123" {
				t.Fatalf("prefix = %q, want %q", prefix, "abc123")
			}
			return true, nil
		}

		if err := svc.InvalidateAPIKeyByPrefix(context.Background(), "abc123"); err != nil {
			t.Fatalf("InvalidateAPIKeyByPrefix() error = %v", err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		svc, repo := newMockService()
		repo.invalidateByPrefixFn = func(_ context.Context, _ string) (bool, error) {
			return false, nil
		}

		err := svc.InvalidateAPIKeyByPrefix(context.Background(), "missing")
		if !errors.Is(err, ErrAPIKeyNotFound) {
			t.Fatalf("InvalidateAPIKeyByPrefix() error = %v, want ErrAPIKeyNotFound", err)
		}
	})
}

func TestGetAndValidateKey(t *testing.T) {
	t.Run("invalid format no database hit", func(t *testing.T) {
		svc, repo := newMockService()
		repo.listByPrefixFn = func(_ context.Context, _ string) ([]models.APIKey, error) {
			t.Fatalf("ListAPIKeysByPrefix() should not be called for invalid format")
			return nil, nil
		}

		_, err := svc.GetAndValidateKey(context.Background(), "invalid")
		if !errors.Is(err, ErrInvalidAPIKey) {
			t.Fatalf("GetAndValidateKey() error = %v, want ErrInvalidAPIKey", err)
		}
	})

	t.Run("success", func(t *testing.T) {
		svc, repo := newMockService()

		full, prefix, err := generateAPIKey()
		if err != nil {
			t.Fatalf("generateAPIKey() error = %v", err)
		}

		repo.listByPrefixFn = func(_ context.Context, gotPrefix string) ([]models.APIKey, error) {
			if gotPrefix != prefix {
				t.Fatalf("prefix = %q, want %q", gotPrefix, prefix)
			}
			return []models.APIKey{{
				ID:        "key-1",
				AccountID: "acct-1",
				Prefix:    prefix,
				KeyHash:   hashAPIKey(full),
				Valid:     true,
				CreatedAt: time.Now().UTC(),
			}}, nil
		}

		key, err := svc.GetAndValidateKey(context.Background(), full)
		if err != nil {
			t.Fatalf("GetAndValidateKey() error = %v", err)
		}
		if key.ID != "key-1" {
			t.Fatalf("GetAndValidateKey() id = %q, want %q", key.ID, "key-1")
		}
	})

	t.Run("expired key", func(t *testing.T) {
		svc, repo := newMockService()

		full, prefix, err := generateAPIKey()
		if err != nil {
			t.Fatalf("generateAPIKey() error = %v", err)
		}

		expiredAt := time.Now().UTC().Add(-time.Minute)
		repo.listByPrefixFn = func(_ context.Context, gotPrefix string) ([]models.APIKey, error) {
			if gotPrefix != prefix {
				t.Fatalf("prefix = %q, want %q", gotPrefix, prefix)
			}
			return []models.APIKey{{
				ID:        "key-1",
				AccountID: "acct-1",
				Prefix:    prefix,
				KeyHash:   hashAPIKey(full),
				ExpiresAt: &expiredAt,
				Valid:     true,
				CreatedAt: time.Now().UTC(),
			}}, nil
		}

		_, err = svc.GetAndValidateKey(context.Background(), full)
		if !errors.Is(err, ErrInvalidAPIKey) {
			t.Fatalf("GetAndValidateKey() error = %v, want ErrInvalidAPIKey", err)
		}
	})
}

type mockRepo struct {
	createAccountFn      func(ctx context.Context, name string) (*models.Account, error)
	createAPIKeyFn       func(ctx context.Context, params accountrepo.CreateAPIKeyParams) (*models.APIKey, error)
	invalidateByIDFn     func(ctx context.Context, apiKeyID string) (bool, error)
	invalidateByPrefixFn func(ctx context.Context, prefix string) (bool, error)
	listByPrefixFn       func(ctx context.Context, prefix string) ([]models.APIKey, error)
}

func (m *mockRepo) CreateAccount(ctx context.Context, name string) (*models.Account, error) {
	if m.createAccountFn == nil {
		return nil, errors.New("unexpected CreateAccount call")
	}
	return m.createAccountFn(ctx, name)
}

func (m *mockRepo) GetAccountByID(context.Context, string) (*models.Account, error) {
	return nil, nil
}

func (m *mockRepo) ListAllAccounts(context.Context) ([]models.Account, error) {
	return nil, nil
}

func (m *mockRepo) DeleteAccountByID(context.Context, string) error {
	return nil
}

func (m *mockRepo) CreateAPIKey(ctx context.Context, params accountrepo.CreateAPIKeyParams) (*models.APIKey, error) {
	if m.createAPIKeyFn == nil {
		return nil, errors.New("unexpected CreateAPIKey call")
	}
	return m.createAPIKeyFn(ctx, params)
}

func (m *mockRepo) InvalidateAPIKeyByID(ctx context.Context, apiKeyID string) (bool, error) {
	if m.invalidateByIDFn == nil {
		return false, errors.New("unexpected InvalidateAPIKeyByID call")
	}
	return m.invalidateByIDFn(ctx, apiKeyID)
}

func (m *mockRepo) InvalidateAPIKeyByPrefix(ctx context.Context, prefix string) (bool, error) {
	if m.invalidateByPrefixFn == nil {
		return false, errors.New("unexpected InvalidateAPIKeyByPrefix call")
	}
	return m.invalidateByPrefixFn(ctx, prefix)
}

func (m *mockRepo) ListAPIKeysByPrefix(ctx context.Context, prefix string) ([]models.APIKey, error) {
	if m.listByPrefixFn == nil {
		return nil, errors.New("unexpected ListAPIKeysByPrefix call")
	}
	return m.listByPrefixFn(ctx, prefix)
}

func newMockService() (*Service, *mockRepo) {
	repo := &mockRepo{}
	return NewService(repo), repo
}
