package archive

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mrsimpson/agentic-office/hub/archive/storage"
)

// commitInfo is one commit as LastChange reads it.
type commitInfo struct {
	id     string
	author storage.Author
	time   time.Time
	body   string
}

// logFormat is the format of git log that parseLog reads: ID, author name, author e-mail, author
// time and the message, separated by NUL, one record per commit.
const logFormat = "--format=%H%x00%an%x00%ae%x00%aI%x00%B%x1e"

// parseLog reads the output of git log with logFormat.
func parseLog(out string) ([]commitInfo, error) {
	var commits []commitInfo
	for _, rec := range strings.Split(out, "\x1e") {
		fields := strings.SplitN(strings.TrimLeft(rec, "\n"), "\x00", 5)
		if len(fields) != 5 {
			continue
		}
		c := commitInfo{id: fields[0], body: fields[4]}
		if fields[2] != hubEmail {
			c.author = storage.Author{Name: fields[1], Email: fields[2]}
		}
		t, err := time.Parse(time.RFC3339, fields[3])
		if err != nil {
			return nil, fmt.Errorf("archive: the time of commit %s: %w", c.id, err)
		}
		c.time = t
		commits = append(commits, c)
	}
	return commits, nil
}

// LastChange implements storage.Archive. It asks git for the newest commit of main that added or
// changed the file (git log with the filter "added, modified or type changed"). When its
// message names a session in the trailer of a release, it looks in the staging repository for
// the newest commit that wrote the file in the area of that session.
func (r *Repo) LastChange(ctx context.Context, p string) (storage.PathChange, error) {
	if err := storage.CheckPath(p); err != nil {
		return storage.PathChange{}, errors.Join(storage.ErrNoChange, err)
	}
	head, err := r.head(ctx)
	if err != nil {
		return storage.PathChange{}, err
	}
	if head == "" {
		return storage.PathChange{}, storage.ErrNoChange
	}
	out, err := r.run(ctx, "log", "-1", "--no-renames", "--diff-filter=AMT", logFormat, head, "--", p)
	if err != nil {
		return storage.PathChange{}, err
	}
	commits, err := parseLog(out)
	if err != nil {
		return storage.PathChange{}, err
	}
	if len(commits) == 0 {
		return storage.PathChange{}, storage.ErrNoChange
	}
	c := commits[0]
	ch := storage.PathChange{Time: c.time, Author: c.author}
	name := releasedArea(c.body)
	if name == "" {
		return ch, nil
	}
	session, ok := sessionOfArea(name)
	if !ok {
		return ch, nil
	}
	ch.Release = &storage.PathRelease{Session: session, By: c.author, Time: c.time}
	if r.staging != nil {
		w, found, err := r.staging.lastWriteInArea(ctx, name, p)
		if err != nil {
			return storage.PathChange{}, err
		}
		if found {
			ch.Time, ch.Author = w.time, w.author
		}
	}
	return ch, nil
}

// releasedArea returns the name of the area that the trailer of a release names in a commit
// message, or "".
func releasedArea(body string) string {
	for _, l := range strings.Split(strings.TrimSpace(body), "\n") {
		if name, ok := strings.CutPrefix(l, releaseTrailer+": "); ok {
			return strings.TrimSpace(name)
		}
	}
	return ""
}

// sessionOfArea returns the session ID of an area name (see areaName).
func sessionOfArea(name string) (string, bool) {
	enc, ok := strings.CutPrefix(name, "=")
	if !ok {
		return name, name != ""
	}
	b, err := base64.RawURLEncoding.DecodeString(enc)
	if err != nil || len(b) == 0 {
		return "", false
	}
	return string(b), true
}

// lastWriteInArea returns the newest commit of the staging repository that added or changed the
// file at p in the area with the given name. Every write of an area changes its state in the same
// commit, so a commit of the area is one that also changed the state of the area.
func (r *Repo) lastWriteInArea(ctx context.Context, name, p string) (commitInfo, bool, error) {
	head, err := r.head(ctx)
	if err != nil || head == "" {
		return commitInfo{}, false, err
	}
	out, err := r.run(ctx, "log", "--no-renames", "--diff-filter=AMT", logFormat, head, "--", p)
	if err != nil {
		return commitInfo{}, false, err
	}
	writes, err := parseLog(out)
	if err != nil || len(writes) == 0 {
		return commitInfo{}, false, err
	}
	state := areasDir + "/" + name + "/" + areaStateFile
	ids, err := r.run(ctx, "log", "--format=%H", head, "--", state)
	if err != nil {
		return commitInfo{}, false, err
	}
	ofArea := map[string]bool{}
	for _, id := range strings.Fields(ids) {
		ofArea[id] = true
	}
	for _, w := range writes {
		if ofArea[w.id] {
			return w, true, nil
		}
	}
	return commitInfo{}, false, nil
}
