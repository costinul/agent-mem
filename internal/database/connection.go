package database

import (
	"database/sql"
	"fmt"

	_ "github.com/lib/pq"
)

type DB struct {
	*sql.DB
}

func Connect(dsn string) (*DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}

	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &DB{db}, nil
}
