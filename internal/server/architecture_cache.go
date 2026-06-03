// SPDX-License-Identifier: MIT

package server

import (
	"time"

	"github.com/kwad77/pincher/internal/db"
)

const architectureCacheTTL = 30 * time.Second

type architectureCacheKey struct {
	projectID    string
	includeTests bool
}

type architectureCacheEntry struct {
	expiresAt time.Time
	indexedAt time.Time
	symCount  int
	edgeCount int
	data      map[string]any
}

func (s *Server) getArchitectureCache(projectID string, includeTests bool, p *db.Project) (map[string]any, bool) {
	if p == nil {
		return nil, false
	}
	key := architectureCacheKey{projectID: projectID, includeTests: includeTests}
	raw, ok := s.architectureCache.Load(key)
	if !ok {
		return nil, false
	}
	entry, ok := raw.(architectureCacheEntry)
	if !ok || time.Now().After(entry.expiresAt) {
		s.architectureCache.Delete(key)
		return nil, false
	}
	if !entry.indexedAt.Equal(p.IndexedAt) || entry.symCount != p.SymCount || entry.edgeCount != p.EdgeCount {
		s.architectureCache.Delete(key)
		return nil, false
	}
	return cloneArchitectureData(entry.data), true
}

func (s *Server) setArchitectureCache(projectID string, includeTests bool, p *db.Project, data map[string]any) {
	if p == nil || data == nil {
		return
	}
	key := architectureCacheKey{projectID: projectID, includeTests: includeTests}
	s.architectureCache.Store(key, architectureCacheEntry{
		expiresAt: time.Now().Add(architectureCacheTTL),
		indexedAt: p.IndexedAt,
		symCount:  p.SymCount,
		edgeCount: p.EdgeCount,
		data:      cloneArchitectureData(data),
	})
}

func cloneArchitectureData(in map[string]any) map[string]any {
	if in == nil {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		if k == "_meta" {
			if meta, ok := v.(map[string]any); ok {
				metaCopy := make(map[string]any, len(meta))
				for mk, mv := range meta {
					metaCopy[mk] = mv
				}
				out[k] = metaCopy
				continue
			}
		}
		out[k] = v
	}
	return out
}
