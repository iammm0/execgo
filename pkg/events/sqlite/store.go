package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/iammm0/execgo/pkg/events"
	"github.com/iammm0/execgo/pkg/models"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS runtime_event_log (
	global_offset INTEGER PRIMARY KEY AUTOINCREMENT,
	event_id TEXT NOT NULL UNIQUE,
	stream TEXT NOT NULL,
	aggregate_id TEXT NOT NULL,
	aggregate_version INTEGER NOT NULL,
	event_type TEXT NOT NULL,
	payload TEXT NOT NULL,
	metadata TEXT NOT NULL,
	created_at DATETIME NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_runtime_event_stream_version ON runtime_event_log(stream, aggregate_version);
CREATE INDEX IF NOT EXISTS idx_runtime_event_aggregate ON runtime_event_log(aggregate_id, aggregate_version);
`

// Store is a SQLite-backed append-only event store.
type Store struct {
	db *sql.DB
	mu sync.Mutex
}

var _ events.EventStore = (*Store)(nil)

// Open opens or creates a SQLite event store.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open sqlite event store: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init sqlite event schema: %w", err)
	}
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite pragma journal_mode: %w", err)
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		db.Close()
		return nil, fmt.Errorf("sqlite pragma busy_timeout: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) Append(ctx context.Context, stream string, expectedVersion int64, evs ...models.RuntimeEvent) ([]models.RuntimeEvent, error) {
	if len(evs) == 0 {
		return nil, nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var curVersion int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(aggregate_version),0) FROM runtime_event_log WHERE stream=?`, stream).Scan(&curVersion); err != nil {
		return nil, err
	}
	if expectedVersion >= 0 && curVersion != expectedVersion {
		return nil, events.ErrVersionConflict
	}

	applied := make([]models.RuntimeEvent, 0, len(evs))
	next := curVersion
	for _, e := range evs {
		next++
		if e.EventID == "" {
			e.EventID = fmt.Sprintf("evt-%d", time.Now().UnixNano())
		}
		if e.CreatedAt.IsZero() {
			e.CreatedAt = time.Now().UTC()
		}
		e.Stream = stream
		if e.AggregateID == "" {
			e.AggregateID = stream
		}
		e.AggregateVersion = next
		e.NormalizeLegacyFields()

		payload, err := json.Marshal(e.Payload)
		if err != nil {
			return nil, err
		}
		if len(payload) == 0 {
			payload = []byte("{}")
		}
		meta, err := json.Marshal(e.Metadata)
		if err != nil {
			return nil, err
		}
		if len(meta) == 0 {
			meta = []byte("{}")
		}
		if _, err := tx.ExecContext(
			ctx,
			`INSERT INTO runtime_event_log(event_id, stream, aggregate_id, aggregate_version, event_type, payload, metadata, created_at) VALUES(?,?,?,?,?,?,?,?)`,
			e.EventID,
			stream,
			e.AggregateID,
			e.AggregateVersion,
			string(e.Type),
			string(payload),
			string(meta),
			e.CreatedAt,
		); err != nil {
			return nil, err
		}
		if err := tx.QueryRowContext(ctx, `SELECT last_insert_rowid()`).Scan(&e.GlobalOffset); err != nil {
			return nil, err
		}
		applied = append(applied, e)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return applied, nil
}

func (s *Store) LoadStream(ctx context.Context, stream string, fromVersion int64, limit int) ([]models.RuntimeEvent, error) {
	query := `SELECT global_offset, event_id, stream, aggregate_id, aggregate_version, event_type, payload, metadata, created_at FROM runtime_event_log WHERE stream=? AND aggregate_version>? ORDER BY aggregate_version ASC`
	args := []any{stream, fromVersion}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRows(rows)
}

func (s *Store) LoadGlobal(ctx context.Context, afterOffset int64, limit int) ([]models.RuntimeEvent, error) {
	query := `SELECT global_offset, event_id, stream, aggregate_id, aggregate_version, event_type, payload, metadata, created_at FROM runtime_event_log WHERE global_offset>? ORDER BY global_offset ASC`
	args := []any{afterOffset}
	if limit > 0 {
		query += ` LIMIT ?`
		args = append(args, limit)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRows(rows)
}

func (s *Store) Replay(ctx context.Context, aggregateID string) ([]models.RuntimeEvent, error) {
	query := `SELECT global_offset, event_id, stream, aggregate_id, aggregate_version, event_type, payload, metadata, created_at FROM runtime_event_log WHERE aggregate_id=? ORDER BY aggregate_version ASC`
	rows, err := s.db.QueryContext(ctx, query, aggregateID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanRows(rows)
}

func scanRows(rows *sql.Rows) ([]models.RuntimeEvent, error) {
	out := make([]models.RuntimeEvent, 0)
	for rows.Next() {
		var e models.RuntimeEvent
		var typ string
		var payload string
		var metadata string
		if err := rows.Scan(
			&e.GlobalOffset,
			&e.EventID,
			&e.Stream,
			&e.AggregateID,
			&e.AggregateVersion,
			&typ,
			&payload,
			&metadata,
			&e.CreatedAt,
		); err != nil {
			return nil, err
		}
		e.Type = models.RuntimeEventType(typ)
		e.Payload = json.RawMessage(payload)
		_ = json.Unmarshal([]byte(metadata), &e.Metadata)
		e.NormalizeLegacyFields()
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
