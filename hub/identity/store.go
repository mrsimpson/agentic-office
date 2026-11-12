package identity

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"path"
	"strings"
	"time"

	"github.com/mrsimpson/agentic-office/hub/archive/storage"
	identityv1 "github.com/mrsimpson/agentic-office/hub/identity/api"
	"github.com/mrsimpson/agentic-office/hub/identity/api/auth"
)

// Bootstrap names the first system admin. The hub needs it at its first start, when the hub
// repository has no users yet. Later starts ignore it.
type Bootstrap struct {
	UserID    string
	PublicKey ed25519.PublicKey
}

// load reads the hub repository, checks the rules and, at the first start, creates the first
// system admin. It fails when the repository breaks a rule: nobody gets rights by a manual edit.
func (s *Service) load(ctx context.Context) error {
	users, err := s.opts.Archive.Read(ctx, usersDir)
	if err != nil {
		return fmt.Errorf("read users: %w", err)
	}
	members, err := s.opts.Archive.Read(ctx, membershipsDir)
	if err != nil {
		return fmt.Errorf("read memberships: %w", err)
	}
	if len(users) == 0 && len(members) == 0 {
		return s.bootstrap(ctx)
	}
	if b := s.opts.Bootstrap; b != nil && s.opts.Logf != nil {
		s.opts.Logf("the hub repository has users: the first system admin %s is ignored", b.UserID)
	}
	var ue, me []fileEntry
	for _, f := range users {
		ue = append(ue, fileEntry{path: f.Path, name: path.Base(f.Path), data: f.Data})
	}
	for _, f := range members {
		me = append(me, fileEntry{path: f.Path, name: path.Base(f.Path), data: f.Data})
	}
	st, err := parseFiles(ue, me)
	if err != nil {
		return fmt.Errorf("hub repository: %w", err)
	}
	if err := st.validate(time.Now()); err != nil {
		return fmt.Errorf("hub repository breaks a rule, not starting: %w", err)
	}
	files, err := st.files()
	if err != nil {
		return err
	}
	s.st, s.files = st, files
	s.reindex()
	return nil
}

func (s *Service) bootstrap(ctx context.Context) error {
	b := s.opts.Bootstrap
	if b == nil {
		return fmt.Errorf("the hub repository has no users: at the first start, name the first system admin " +
			"with -first-system-admin ID=PUBLIC_KEY (see the user guide)")
	}
	if !participantIDPattern.MatchString(b.UserID) || len(b.PublicKey) != ed25519.PublicKeySize {
		return fmt.Errorf("first system admin: need a valid user ID and an Ed25519 public key")
	}
	now := time.Now().UTC().Truncate(time.Second)
	st := newState()
	st.users[b.UserID] = &userRecord{DisplayName: b.UserID, Kind: kindName(identityv1.ActorKind_ACTOR_KIND_HUMAN),
		SystemAdmin: true, ValidFrom: now,
		Keys: []*keyRecord{{KeyID: auth.KeyID(b.PublicKey), PublicKey: auth.FormatPublicKey(b.PublicKey)}}}
	if err := s.commit(ctx, st, "Create the first system admin "+b.UserID); err != nil {
		return err
	}
	if s.opts.Logf != nil {
		s.opts.Logf("created the first system admin %s", b.UserID)
	}
	return nil
}

// commit writes the files of st that differ from the repository as one commit and makes st the
// state. The caller holds s.mu (or is New).
func (s *Service) commit(ctx context.Context, st *state, message string) error {
	files, err := st.files()
	if err != nil {
		return err
	}
	var changed []storage.File
	for p, data := range files {
		if string(s.files[p]) != string(data) {
			changed = append(changed, storage.File{Path: p, Data: data})
		}
	}
	if len(changed) > 0 {
		if _, err := s.opts.Archive.Commit(ctx, storage.Change{Message: message, Files: changed}); err != nil {
			return fmt.Errorf("write the hub repository: %w", err)
		}
	}
	s.st, s.files = st, files
	s.reindex()
	return nil
}

// keyRef is a registered key in the index of keys.
type keyRef struct {
	userID  string
	public  ed25519.PublicKey
	revoked bool
}

// reindex rebuilds the index from key IDs to users.
func (s *Service) reindex() {
	s.keys = map[string]keyRef{}
	for id, u := range s.st.users {
		for _, k := range u.Keys {
			s.keys[k.KeyID] = keyRef{userID: id, public: publicKeyOf(k), revoked: k.Revoked}
		}
	}
}

// usable reports whether the key may log in now: it is not revoked and its user is active.
func (s *Service) usable(keyID string, now time.Time) (keyRef, *userRecord, bool) {
	k, ok := s.keys[keyID]
	if !ok || k.revoked {
		return keyRef{}, nil, false
	}
	u := s.st.users[k.userID]
	if u == nil || !u.active(now) {
		return keyRef{}, nil, false
	}
	return k, u, true
}

// ParseBootstrap parses the value of -first-system-admin: "ID=PUBLIC_KEY".
func ParseBootstrap(v string) (*Bootstrap, error) {
	id, key, ok := strings.Cut(v, "=")
	if !ok {
		return nil, fmt.Errorf("want ID=PUBLIC_KEY")
	}
	pub, err := auth.ParsePublicKey(strings.TrimSpace(key))
	if err != nil {
		return nil, err
	}
	if !participantIDPattern.MatchString(id) {
		return nil, fmt.Errorf("bad user ID %q: use 1 to 64 letters, digits, '.', '_' or '-'", id)
	}
	return &Bootstrap{UserID: id, PublicKey: pub}, nil
}
