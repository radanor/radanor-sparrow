package storage

import (
	"context"
	"errors"
	"time"
)

// Persisted user preferences.
//
// The store is deliberately generic: it enforces only structural bounds (key and
// value size, key shape). The set of *supported* keys and their value domains is
// owned by the core API, which validates every update against a fixed allowlist
// before it reaches this store. Nothing here can store a file path, a command,
// or an arbitrary blob.

const (
	// MaxSettingKeyLen bounds a preference key.
	MaxSettingKeyLen = 32
	// MaxSettingValueLen bounds a preference value.
	MaxSettingValueLen = 64
	// MaxSettings bounds how many preferences may exist.
	MaxSettings = 32
)

// ErrInvalidSetting reports a structurally unusable preference.
var ErrInvalidSetting = errors.New("invalid setting")

// Settings returns every persisted preference as a key/value map.
func (s *Store) Settings(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key,value FROM settings ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, rows.Err()
}

// PutSettings applies a batch of preference changes in one transaction. Either
// every change is stored or none is, so a rejected update never leaves a
// half-applied configuration behind.
func (s *Store) PutSettings(ctx context.Context, updates map[string]string) error {
	if len(updates) == 0 {
		return nil
	}
	if len(updates) > MaxSettings {
		return ErrInvalidSetting
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var existing int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM settings`).Scan(&existing); err != nil {
		return err
	}
	now := time.Now().UnixNano()
	newKeys := 0
	for key, value := range updates {
		if len(key) == 0 || len(key) > MaxSettingKeyLen || len(value) > MaxSettingValueLen {
			return ErrInvalidSetting
		}
		res, err := tx.ExecContext(ctx, `UPDATE settings SET value=?,updated_ns=? WHERE key=?`, value, now, key)
		if err != nil {
			return err
		}
		if updated, aerr := res.RowsAffected(); aerr == nil && updated == 0 {
			newKeys++
			if _, err := tx.ExecContext(ctx, `INSERT INTO settings(key,value,updated_ns) VALUES(?,?,?)`, key, value, now); err != nil {
				return err
			}
		}
	}
	if existing+newKeys > MaxSettings {
		return ErrInvalidSetting
	}
	return tx.Commit()
}
