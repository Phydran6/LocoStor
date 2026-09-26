package auth

import "sync"

// MemStore keeps credentials in memory (demo mode and tests).
type MemStore struct {
	mu sync.Mutex
	c  Credentials
}

// NewMemStore creates a store with the given username and password.
func NewMemStore(user, password string) (*MemStore, error) {
	hash, err := HashPassword(password)
	if err != nil {
		return nil, err
	}
	return &MemStore{c: Credentials{Username: user, PasswordHash: hash}}, nil
}

// Credentials implements Store.
func (s *MemStore) Credentials() Credentials {
	s.mu.Lock()
	defer s.mu.Unlock()
	c := s.c
	c.RecoveryCodes = append([]string(nil), s.c.RecoveryCodes...)
	return c
}

// UpdateCredentials implements Store.
func (s *MemStore) UpdateCredentials(fn func(*Credentials)) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.c)
	return nil
}
