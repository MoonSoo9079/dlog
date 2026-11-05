package agent

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"

	"dlog/internal/protocol"
)

// Cursor is the last log line acknowledged by the server for a container.
type Cursor struct {
	UnixNano int64  `json:"unix_nano"`
	Hash     string `json:"hash"`
}

// State is the agent's durable identity and delivery cursors.
type State struct {
	HostID    string            `json:"host_id,omitempty"`
	HostToken string            `json:"host_token,omitempty"`
	Cursors   map[string]Cursor `json:"cursors,omitempty"`
}

// StateFile stores State as JSON with mode 0600. Writes replace the file atomically.
type StateFile struct {
	mu   sync.Mutex
	path string
	State
}

func LoadState(dir string) (*StateFile, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	sf := &StateFile{path: filepath.Join(dir, "state.json"), State: State{Cursors: map[string]Cursor{}}}
	b, err := os.ReadFile(sf.path)
	if errors.Is(err, os.ErrNotExist) {
		return sf, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &sf.State); err != nil {
		return nil, err
	}
	if sf.Cursors == nil {
		sf.Cursors = map[string]Cursor{}
	}
	return sf, nil
}

func (s *StateFile) Token() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.HostToken
}

func (s *StateFile) SetIdentity(id, tok string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if id != "" {
		s.HostID = id
	}
	s.HostToken = tok
	return s.saveLocked()
}

func (s *StateFile) Cursor(id string) (Cursor, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.Cursors[id]
	return c, ok && c.UnixNano != 0
}

// Advance moves cursors forward for a batch the server accepted, then saves.
func (s *StateFile) Advance(recs []protocol.Record) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Cursors == nil {
		s.Cursors = map[string]Cursor{}
	}
	for _, r := range recs {
		nano := r.Timestamp.UnixNano()
		cur := s.Cursors[r.ContainerID]
		if nano >= cur.UnixNano {
			s.Cursors[r.ContainerID] = Cursor{UnixNano: nano, Hash: lineHash(r.Stream, r.Message)}
		}
	}
	return s.saveLocked()
}

func (s *StateFile) saveLocked() error {
	if s.Cursors == nil {
		s.Cursors = map[string]Cursor{}
	}
	b, err := json.MarshalIndent(s.State, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

func lineHash(stream, message string) string {
	sum := sha256.Sum256([]byte(stream + "\x00" + message))
	return hex.EncodeToString(sum[:8])
}
