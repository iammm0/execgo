package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/iammm0/execgo/pkg/events"
	"github.com/iammm0/execgo/pkg/models"

	_ "github.com/jackc/pgx/v5/stdlib"
)

const schema = `
CREATE TABLE IF NOT EXISTS runtime_event_log (
	global_offset BIGSERIAL PRIMARY KEY,
	event_id TEXT NOT NULL UNIQUE,
	stream TEXT NOT NULL,
	aggregate_id TEXT NOT NULL,
	aggregate_version BIGINT NOT NULL,
	event_type TEXT NOT NULL,
	payload JSONB NOT NULL,
	metadata JSONB NOT NULL,
	created_at TIMESTAMPTZ NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_runtime_event_stream_version ON runtime_event_log(stream, aggregate_version);
CREATE INDEX IF NOT EXISTS idx_runtime_event_aggregate ON runtime_event_log(aggregate_id, aggregate_version);
`

// Store is a Postgres-backed append-only event store.
type Store struct {
	db *sql.DB
	mu sync.Mutex
}

var _ events.EventStore = (*Store)(nil)

// Open opens a Postgres event store using DSN.
func Open(dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("open postgres event store: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("init postgres event schema: %w", err)
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
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(aggregate_version),0) FROM runtime_event_log WHERE stream=$1`, stream).Scan(&curVersion); err != nil {
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

		payload := json.RawMessage(`{}`)
		if len(e.Payload) > 0 {
			payload = e.Payload
		}
		meta, err := json.Marshal(e.Metadata)
		if err != nil {
			return nil, err
		}
		if err := tx.QueryRowContext(
			ctx,
			`INSERT INTO runtime_event_log(event_id, stream, aggregate_id, aggregate_version, event_type, payload, metadata, created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING global_offset`,
			e.EventID,
			stream,
			e.AggregateID,
			e.AggregateVersion,
			string(e.Type),
			[]byte(payload),
			meta,
			e.CreatedAt,
		).Scan(&e.GlobalOffset); err != nil {
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
	query := `SELECT global_offset, event_id, stream, aggregate_id, aggregate_version, event_type, payload, metadata, created_at FROM runtime_event_log WHERE stream=$1 AND aggregate_version>$2 ORDER BY aggregate_version ASC`
	args := []any{stream, fromVersion}
	if limit > 0 {
		query += ` LIMIT $3`
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
	query := `SELECT global_offset, event_id, stream, aggregate_id, aggregate_version, event_type, payload, metadata, created_at FROM runtime_event_log WHERE global_offset>$1 ORDER BY global_offset ASC`
	args := []any{afterOffset}
	if limit > 0 {
		query += ` LIMIT $2`
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
	rows, err := s.db.QueryContext(
		ctx,
		`SELECT global_offset, event_id, stream, aggregate_id, aggregate_version, event_type, payload, metadata, created_at FROM runtime_event_log WHERE aggregate_id=$1 ORDER BY aggregate_version ASC`,
		aggregateID,
	)
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
		var payload []byte
		var metadata []byte
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
		_ = json.Unmarshal(metadata, &e.Metadata)
		e.NormalizeLegacyFields()
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
