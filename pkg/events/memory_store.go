package events

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/iammm0/execgo/pkg/models"
)

// MemoryStore is an in-memory append-only event store.
type MemoryStore struct {
	mu           sync.RWMutex
	streams      map[string][]models.RuntimeEvent
	global       []models.RuntimeEvent
	globalOffset int64
}

// NewMemoryStore creates a new in-memory event store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		streams: make(map[string][]models.RuntimeEvent),
		global:  make([]models.RuntimeEvent, 0, 1024),
	}
}

var _ EventStore = (*MemoryStore)(nil)

func (s *MemoryStore) Append(ctx context.Context, stream string, expectedVersion int64, evs ...models.RuntimeEvent) ([]models.RuntimeEvent, error) {
	_ = ctx
	if len(evs) == 0 {
		return nil, nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	curVersion := int64(len(s.streams[stream]))
	if expectedVersion >= 0 && curVersion != expectedVersion {
		return nil, ErrVersionConflict
	}

	applied := make([]models.RuntimeEvent, 0, len(evs))
	nextVersion := curVersion
	for _, e := range evs {
		nextVersion++
		if e.EventID == "" {
			e.EventID = newEventID()
		}
		if e.CreatedAt.IsZero() {
			e.CreatedAt = time.Now().UTC()
		}
		e.Stream = stream
		if e.AggregateID == "" {
			e.AggregateID = stream
		}
		e.AggregateVersion = nextVersion
		s.globalOffset++
		e.GlobalOffset = s.globalOffset
		e.NormalizeLegacyFields()

		s.streams[stream] = append(s.streams[stream], e)
		s.global = append(s.global, e)
		applied = append(applied, e)
	}
	return applied, nil
}

func (s *MemoryStore) LoadStream(ctx context.Context, stream string, fromVersion int64, limit int) ([]models.RuntimeEvent, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()

	entries := s.streams[stream]
	if len(entries) == 0 {
		return nil, nil
	}
	if fromVersion < 0 {
		fromVersion = 0
	}
	start := 0
	if fromVersion > 0 {
		start = int(fromVersion)
		if start > len(entries) {
			start = len(entries)
		}
	}
	out := make([]models.RuntimeEvent, 0, len(entries)-start)
	for i := start; i < len(entries); i++ {
		out = append(out, entries[i])
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *MemoryStore) LoadGlobal(ctx context.Context, afterOffset int64, limit int) ([]models.RuntimeEvent, error) {
	_ = ctx
	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.global) == 0 {
		return nil, nil
	}
	idx := sort.Search(len(s.global), func(i int) bool {
		return s.global[i].GlobalOffset > afterOffset
	})
	if idx >= len(s.global) {
		return nil, nil
	}

	out := make([]models.RuntimeEvent, 0, len(s.global)-idx)
	for i := idx; i < len(s.global); i++ {
		out = append(out, s.global[i])
		if limit > 0 && len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (s *MemoryStore) Replay(ctx context.Context, aggregateID string) ([]models.RuntimeEvent, error) {
	stream := aggregateID
	if stream == "" {
		return s.LoadGlobal(ctx, 0, 0)
	}
	return s.LoadStream(ctx, stream, 0, 0)
}
