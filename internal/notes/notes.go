package notes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// DateLayout is the format used for note dates and filenames.
const DateLayout = "2006-01-02"

var (
	// ErrNotFound is returned when a note does not exist.
	ErrNotFound = errors.New("note not found")
	// ErrRevisionRequired is returned when replacing an existing note without a revision.
	ErrRevisionRequired = errors.New("revision is required to replace an existing note")
	// ErrRevisionMismatch is returned when the supplied revision does not match the current note.
	ErrRevisionMismatch = errors.New("revision does not match the current note")
)

// Store reads and writes daily notes in a flat directory.
type Store struct {
	dir string
	loc *time.Location
}

// New creates a Store rooted at dir. The directory is created if it does not exist.
func New(dir string, loc *time.Location) (*Store, error) {
	if dir == "" {
		return nil, errors.New("notes directory is required")
	}
	if loc == nil {
		loc = time.UTC
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create notes directory: %w", err)
	}
	return &Store{dir: dir, loc: loc}, nil
}

// Dir returns the notes directory.
func (s *Store) Dir() string {
	return s.dir
}

// Today returns the current date in the store's timezone.
func (s *Store) Today() string {
	return time.Now().In(s.loc).Format(DateLayout)
}

// ParseDate parses a date in YYYY-MM-DD format.
func ParseDate(date string) (time.Time, error) {
	t, err := time.Parse(DateLayout, date)
	if err != nil || t.Format(DateLayout) != date {
		return time.Time{}, fmt.Errorf("invalid date %q: expected YYYY-MM-DD", date)
	}
	return t, nil
}

// ResolveDate validates date, returning today's date when date is empty.
func (s *Store) ResolveDate(date string) (string, error) {
	if date == "" {
		return s.Today(), nil
	}
	if _, err := ParseDate(date); err != nil {
		return "", err
	}
	return date, nil
}

// Note is a daily note and its current revision.
type Note struct {
	Date     string
	Body     string
	Revision string
}

// Get returns the note for date.
func (s *Store) Get(date string) (Note, error) {
	resolved, err := s.ResolveDate(date)
	if err != nil {
		return Note{}, err
	}
	content, err := s.read(resolved)
	if err != nil {
		return Note{}, err
	}
	return Note{
		Date:     resolved,
		Body:     splitFrontmatter(content),
		Revision: revisionOf(content),
	}, nil
}

// Replace sets the body of the note for date and returns the new revision.
// A revision is required when the note already exists and must be omitted when creating.
func (s *Store) Replace(date, body, revision string) (string, error) {
	resolved, err := s.ResolveDate(date)
	if err != nil {
		return "", err
	}

	var newRevision string
	err = s.withLock(resolved, func() error {
		existing, readErr := s.read(resolved)
		exists := readErr == nil
		if readErr != nil && !errors.Is(readErr, ErrNotFound) {
			return readErr
		}

		switch {
		case exists && revision == "":
			return ErrRevisionRequired
		case exists && revision != revisionOf(existing):
			return fmt.Errorf("%w: expected %s", ErrRevisionMismatch, revisionOf(existing))
		case !exists && revision != "":
			return fmt.Errorf("%w: note %s does not exist; omit revision to create", ErrRevisionMismatch, resolved)
		}

		content := render(resolved, body)
		if err := s.writeAtomic(resolved, content); err != nil {
			return err
		}
		newRevision = revisionOf(content)
		return nil
	})
	if err != nil {
		return "", err
	}
	return newRevision, nil
}

// List returns existing note dates, optionally bounded by from and to (inclusive).
func (s *Store) List(from, to string) ([]string, error) {
	fromTime, err := parseBound(from)
	if err != nil {
		return nil, err
	}
	toTime, err := parseBound(to)
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		return nil, fmt.Errorf("read notes directory: %w", err)
	}

	var dates []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".md") {
			continue
		}
		date := strings.TrimSuffix(name, ".md")
		t, err := ParseDate(date)
		if err != nil {
			continue
		}
		if fromTime != nil && t.Before(*fromTime) {
			continue
		}
		if toTime != nil && t.After(*toTime) {
			continue
		}
		dates = append(dates, date)
	}
	sort.Strings(dates)
	return dates, nil
}

// Match is a search hit within a note.
type Match struct {
	Date    string `json:"date"`
	Line    int    `json:"line"`
	Snippet string `json:"snippet"`
}

// Search finds case-insensitive substring matches in note bodies.
func (s *Store) Search(query, from, to string, maxResults int) ([]Match, error) {
	if strings.TrimSpace(query) == "" {
		return nil, errors.New("query must not be empty")
	}
	if maxResults <= 0 {
		maxResults = 50
	}
	dates, err := s.List(from, to)
	if err != nil {
		return nil, err
	}

	needle := strings.ToLower(query)
	var matches []Match
	for _, date := range dates {
		content, err := s.read(date)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		if err != nil {
			return nil, err
		}
		for i, line := range strings.Split(splitFrontmatter(content), "\n") {
			if !strings.Contains(strings.ToLower(line), needle) {
				continue
			}
			matches = append(matches, Match{
				Date:    date,
				Line:    i + 1,
				Snippet: truncate(strings.TrimSpace(line), 240),
			})
			if len(matches) >= maxResults {
				return matches, nil
			}
		}
	}
	return matches, nil
}

// Ready reports whether the notes directory is writable.
func (s *Store) Ready(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dir, ".readyz-*")
	if err != nil {
		return fmt.Errorf("notes directory is not writable: %w", err)
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.WriteString("ok"); err != nil {
		f.Close()
		return fmt.Errorf("write readiness probe: %w", err)
	}
	return f.Close()
}

func (s *Store) path(date string) string {
	return filepath.Join(s.dir, date+".md")
}

func (s *Store) read(date string) (string, error) {
	data, err := os.ReadFile(s.path(date))
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read note %s: %w", date, err)
	}
	return string(data), nil
}

func (s *Store) writeAtomic(date, content string) error {
	f, err := os.CreateTemp(s.dir, "."+date+".md.tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary note: %w", err)
	}
	name := f.Name()
	defer os.Remove(name)

	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return fmt.Errorf("write note %s: %w", date, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync note %s: %w", date, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close note %s: %w", date, err)
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return fmt.Errorf("chmod note %s: %w", date, err)
	}
	if err := os.Rename(name, s.path(date)); err != nil {
		return fmt.Errorf("replace note %s: %w", date, err)
	}
	return nil
}

func (s *Store) withLock(date string, fn func() error) error {
	lockPath := s.path(date) + ".lock"
	lock, err := s.acquireLock(date, lockPath)
	if err != nil {
		return err
	}
	defer func() {
		os.Remove(lockPath)
		syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		lock.Close()
	}()

	return fn()
}

func (s *Store) acquireLock(date, lockPath string) (*os.File, error) {
	for {
		lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			return nil, fmt.Errorf("open note lock: %w", err)
		}
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
			lock.Close()
			return nil, fmt.Errorf("lock note %s: %w", date, err)
		}

		held, err := lock.Stat()
		if err != nil {
			syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
			lock.Close()
			return nil, fmt.Errorf("stat note lock %s: %w", date, err)
		}
		current, err := os.Stat(lockPath)
		if errors.Is(err, os.ErrNotExist) {
			syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
			lock.Close()
			continue
		}
		if err != nil {
			syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
			lock.Close()
			return nil, fmt.Errorf("stat note lock %s: %w", date, err)
		}
		if os.SameFile(held, current) {
			return lock, nil
		}

		syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		lock.Close()
	}
}

func render(date, body string) string {
	header := fmt.Sprintf("---\ndate: %s\n---\n", date)
	body = strings.TrimRight(body, "\r\n")
	if body == "" {
		return header
	}
	return header + "\n" + body + "\n"
}

func splitFrontmatter(content string) string {
	if !strings.HasPrefix(content, "---\n") {
		return strings.TrimRight(content, "\r\n")
	}
	rest := content[len("---\n"):]
	end := strings.Index(rest, "\n---")
	if end < 0 {
		return strings.TrimRight(content, "\r\n")
	}
	body := rest[end+len("\n---"):]
	body = strings.TrimPrefix(body, "\r")
	body = strings.TrimPrefix(body, "\n")
	body = strings.TrimPrefix(body, "\n")
	return strings.TrimRight(body, "\r\n")
}

func revisionOf(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}

func parseBound(date string) (*time.Time, error) {
	if date == "" {
		return nil, nil
	}
	t, err := ParseDate(date)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func truncate(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit]) + "..."
}
