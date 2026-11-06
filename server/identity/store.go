package identity

import (
	"bufio"
	"crypto/ed25519"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	officev1 "github.com/mrsimpson/agentic-office/core/api/office/v1"
	"github.com/mrsimpson/agentic-office/core/auth"
)

// Admin is one key of the admin file.
type Admin struct {
	ParticipantID string
	DisplayName   string
	PublicKey     ed25519.PublicKey
}

// LoadAdmins reads the admin file. A missing file means no admins.
//
// Each line has a participant ID, a public key in text form (see core/auth) and optionally a
// display name, separated by spaces. Empty lines and lines that start with "#" are ignored.
// Example:
//
//	# participant  public key                                     display name
//	ada            hGs7aXk0n1V6lH1d3mFzWQ0bX2j7zv8nq3C4oD5pE6A=  Ada Lovelace
//
// Admins are humans. A participant ID may have several lines (several keys).
func LoadAdmins(path string) ([]Admin, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Admin
	sc := bufio.NewScanner(f)
	for n := 1; sc.Scan(); n++ {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return nil, fmt.Errorf("%s:%d: want: participant-id public-key [display name]", path, n)
		}
		if !participantIDPattern.MatchString(fields[0]) {
			return nil, fmt.Errorf("%s:%d: bad participant ID %q", path, n, fields[0])
		}
		pub, err := auth.ParsePublicKey(fields[1])
		if err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, n, err)
		}
		name := strings.Join(fields[2:], " ")
		if name == "" {
			name = fields[0]
		}
		out = append(out, Admin{ParticipantID: fields[0], DisplayName: name, PublicKey: pub})
	}
	return out, sc.Err()
}

// addAdmin adds a key of the admin file. A participant that is already known keeps its name.
func (s *Service) addAdmin(a Admin) error {
	id := auth.KeyID(a.PublicKey)
	if k := s.keys[id]; k != nil && k.ParticipantID != a.ParticipantID {
		return fmt.Errorf("admin key %s of %s is registered for participant %s", id, a.ParticipantID, k.ParticipantID)
	}
	p := s.participants[a.ParticipantID]
	if p == nil {
		s.participants[a.ParticipantID] = &participant{DisplayName: a.DisplayName,
			Kind: officev1.ParticipantKind_PARTICIPANT_KIND_HUMAN}
	} else if p.Kind != officev1.ParticipantKind_PARTICIPANT_KIND_HUMAN {
		return fmt.Errorf("admin %s is registered as an agent; admins are humans", a.ParticipantID)
	}
	s.keys[id] = &key{ParticipantID: a.ParticipantID, PublicKey: a.PublicKey, admin: true}
	return nil
}

type stateFile struct {
	Participants map[string]*participant `json:"participants"`
	Keys         map[string]*key         `json:"keys"`
}

func (s *Service) load() error {
	if s.opts.StateFile == "" {
		return nil
	}
	b, err := os.ReadFile(s.opts.StateFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var st stateFile
	if err := json.Unmarshal(b, &st); err != nil {
		return fmt.Errorf("%s: %w", s.opts.StateFile, err)
	}
	for id, p := range st.Participants {
		s.participants[id] = p
	}
	for id, k := range st.Keys {
		if s.participants[k.ParticipantID] == nil || auth.KeyID(k.PublicKey) != id {
			return fmt.Errorf("%s: key %s is broken", s.opts.StateFile, id)
		}
		s.keys[id] = k
	}
	return nil
}

// save writes participants and keys that were registered through the API. Keys of the admin
// file are not written. The caller holds s.mu.
func (s *Service) save() error {
	if s.opts.StateFile == "" {
		return nil
	}
	st := stateFile{Participants: map[string]*participant{}, Keys: map[string]*key{}}
	for id, k := range s.keys {
		if !k.admin {
			st.Keys[id] = k
			st.Participants[k.ParticipantID] = s.participants[k.ParticipantID]
		}
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.opts.StateFile), 0o700); err != nil {
		return err
	}
	tmp := s.opts.StateFile + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.opts.StateFile)
}
