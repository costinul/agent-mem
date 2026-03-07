package account

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"agentmem/internal/database"
	models "agentmem/internal/models"

	"github.com/DATA-DOG/go-sqlmock"
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
		svc, mock, closeFn := newMockService(t)
		defer closeFn()

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE api_keys SET valid = false WHERE prefix = $1`)).
			WithArgs("abc123").
			WillReturnResult(sqlmock.NewResult(0, 1))

		if err := svc.InvalidateAPIKeyByPrefix(context.Background(), "abc123"); err != nil {
			t.Fatalf("InvalidateAPIKeyByPrefix() error = %v", err)
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet sql expectations: %v", err)
		}
	})

	t.Run("not found", func(t *testing.T) {
		svc, mock, closeFn := newMockService(t)
		defer closeFn()

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE api_keys SET valid = false WHERE prefix = $1`)).
			WithArgs("missing").
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := svc.InvalidateAPIKeyByPrefix(context.Background(), "missing")
		if !errors.Is(err, ErrAPIKeyNotFound) {
			t.Fatalf("InvalidateAPIKeyByPrefix() error = %v, want ErrAPIKeyNotFound", err)
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet sql expectations: %v", err)
		}
	})
}

func TestGetAndValidateKey(t *testing.T) {
	t.Run("invalid format no database hit", func(t *testing.T) {
		svc, mock, closeFn := newMockService(t)
		defer closeFn()

		_, err := svc.GetAndValidateKey(context.Background(), "invalid")
		if !errors.Is(err, ErrInvalidAPIKey) {
			t.Fatalf("GetAndValidateKey() error = %v, want ErrInvalidAPIKey", err)
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("expected no DB calls, got: %v", err)
		}
	})

	t.Run("success", func(t *testing.T) {
		svc, mock, closeFn := newMockService(t)
		defer closeFn()

		full, prefix, err := generateAPIKey()
		if err != nil {
			t.Fatalf("generateAPIKey() error = %v", err)
		}

		rows := sqlmock.NewRows([]string{
			"id", "account_id", "prefix", "key_hash", "label", "expires_at", "valid", "created_at",
		}).AddRow(
			"key-1",
			"acct-1",
			prefix,
			hashAPIKey(full),
			nil,
			nil,
			true,
			time.Now().UTC(),
		)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, account_id, prefix, key_hash, label, expires_at, valid, created_at
		 FROM api_keys
		 WHERE prefix = $1`)).
			WithArgs(prefix).
			WillReturnRows(rows)

		key, err := svc.GetAndValidateKey(context.Background(), full)
		if err != nil {
			t.Fatalf("GetAndValidateKey() error = %v", err)
		}
		if key.ID != "key-1" {
			t.Fatalf("GetAndValidateKey() id = %q, want %q", key.ID, "key-1")
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet sql expectations: %v", err)
		}
	})

	t.Run("expired key", func(t *testing.T) {
		svc, mock, closeFn := newMockService(t)
		defer closeFn()

		full, prefix, err := generateAPIKey()
		if err != nil {
			t.Fatalf("generateAPIKey() error = %v", err)
		}

		expiredAt := time.Now().UTC().Add(-time.Minute)
		rows := sqlmock.NewRows([]string{
			"id", "account_id", "prefix", "key_hash", "label", "expires_at", "valid", "created_at",
		}).AddRow(
			"key-1",
			"acct-1",
			prefix,
			hashAPIKey(full),
			nil,
			expiredAt,
			true,
			time.Now().UTC(),
		)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, account_id, prefix, key_hash, label, expires_at, valid, created_at
		 FROM api_keys
		 WHERE prefix = $1`)).
			WithArgs(prefix).
			WillReturnRows(rows)

		_, err = svc.GetAndValidateKey(context.Background(), full)
		if !errors.Is(err, ErrInvalidAPIKey) {
			t.Fatalf("GetAndValidateKey() error = %v, want ErrInvalidAPIKey", err)
		}

		if err := mock.ExpectationsWereMet(); err != nil {
			t.Fatalf("unmet sql expectations: %v", err)
		}
	})
}

func newMockService(t *testing.T) (*Service, sqlmock.Sqlmock, func()) {
	t.Helper()

	sqlDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("sqlmock.New() error = %v", err)
	}

	svc := NewService(&database.DB{DB: sqlDB})
	return svc, mock, func() { _ = sqlDB.Close() }
}
