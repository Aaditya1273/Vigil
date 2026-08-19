package policy

import (
	"errors"
	"sync"
)

// ErrNoDraft means the draft does not exist or was already confirmed.
var ErrNoDraft = errors.New("policy: draft not found")

// Store holds active policies and pending drafts.
//
// Injected rather than a package singleton, unlike the older state trackers in
// this codebase: a singleton makes tests order-dependent and gives every caller
// write access to security state it may not own.
type Store struct {
	mu     sync.RWMutex
	active map[string]*Policy
	drafts map[string]*Draft
}

func NewStore() *Store {
	return &Store{
		active: map[string]*Policy{},
		drafts: map[string]*Draft{},
	}
}

// Get returns a copy of a session's active policy, or nil if none is declared.
func (s *Store) Get(sessionID string) *Policy {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.active[sessionID].Clone()
}

// GetOrDefault returns the session's policy, falling back to the permissive
// baseline so callers never have to nil-check on the hot path.
func (s *Store) GetOrDefault(sessionID string, budget float64) *Policy {
	if p := s.Get(sessionID); p != nil {
		return p
	}
	return Default(sessionID, budget)
}

// Set activates a policy for a session.
func (s *Store) Set(p *Policy) {
	if p == nil {
		return
	}
	cp := p.Clone()
	cp.Active = true
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active[cp.SessionID] = cp
}

// PutDraft stores an unconfirmed draft.
func (s *Store) PutDraft(d *Draft) {
	if d == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.drafts[d.ID] = d
}

// GetDraft returns a pending draft.
func (s *Store) GetDraft(id string) (*Draft, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	d, ok := s.drafts[id]
	return d, ok
}

// Drafts lists pending drafts.
func (s *Store) Drafts() []*Draft {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*Draft, 0, len(s.drafts))
	for _, d := range s.drafts {
		out = append(out, d)
	}
	return out
}

// Confirm promotes a draft to the session's active policy.
//
// This is the only path from model output to enforcement, and it exists solely
// to be called by an explicit human action.
func (s *Store) Confirm(draftID string) (*Policy, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	d, ok := s.drafts[draftID]
	if !ok {
		return nil, ErrNoDraft
	}
	p := d.Policy.Clone()
	p.Active = true
	s.active[p.SessionID] = p
	delete(s.drafts, draftID)
	return p.Clone(), nil
}

// DiscardDraft drops a draft without activating it.
func (s *Store) DiscardDraft(draftID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.drafts[draftID]; !ok {
		return false
	}
	delete(s.drafts, draftID)
	return true
}

// Drop removes a session's policy, e.g. on disconnect.
func (s *Store) Drop(sessionID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.active, sessionID)
}
