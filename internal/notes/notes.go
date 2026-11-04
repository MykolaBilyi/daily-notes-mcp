package notes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

// DateLayout is the format used for note dates.
const DateLayout = "2006-01-02"

// DefaultFormat is the default note filename format.
const DefaultFormat = "YYYY-MM-DD"

var (
	// ErrNotFound is returned when a note does not exist.
	ErrNotFound = errors.New("note not found")
	// ErrRevisionRequired is returned when replacing an existing note without a revision.
	ErrRevisionRequired = errors.New("revision is required to replace an existing note")
	// ErrRevisionMismatch is returned when the supplied revision does not match the current note.
	ErrRevisionMismatch = errors.New("revision does not match the current note")
)

// Store reads and writes daily notes below a root directory.
type Store struct {
	dir          string
	loc          *time.Location
	cfg          Config
	templatePath string
}

// New creates a Store with the default flat YYYY-MM-DD layout.
func New(dir string, loc *time.Location) (*Store, error) {
	return NewWithConfig(dir, loc, Config{})
}

// NewWithConfig creates a Store rooted at dir. The directory is created if it does not exist.
func NewWithConfig(dir string, loc *time.Location, cfg Config) (*Store, error) {
	if dir == "" {
		return nil, errors.New("notes directory is required")
	}
	if loc == nil {
		loc = time.UTC
	}
	if strings.TrimSpace(cfg.Format) == "" {
		cfg.Format = DefaultFormat
	}
	cfg.Folder = strings.TrimSpace(cfg.Folder)
	cfg.Template = strings.TrimSpace(cfg.Template)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create notes directory: %w", err)
	}

	store := &Store{dir: dir, loc: loc, cfg: cfg}
	if cfg.Template != "" {
		templatePath, err := store.resolveTemplatePath(cfg.Template)
		if err != nil {
			return nil, err
		}
		store.templatePath = templatePath
	}
	if err := store.validateFormat(); err != nil {
		return nil, err
	}
	return store, nil
}

func (s *Store) validateFormat() error {
	sample := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	paths := make([]string, 0, 3)
	for _, t := range []time.Time{sample, sample.AddDate(0, 0, 1), sample.AddDate(1, 0, 0)} {
		path, err := s.pathFor(t)
		if err != nil {
			return err
		}
		paths = append(paths, path)
	}
	if paths[0] == paths[1] {
		return fmt.Errorf("note format %q must include a day token", s.cfg.Format)
	}
	if paths[0] == paths[2] {
		return fmt.Errorf("note format %q must include a year token", s.cfg.Format)
	}
	return nil
}

// Dir returns the notes directory.
func (s *Store) Dir() string {
	return s.dir
}

// Config returns the active notes configuration.
func (s *Store) Config() Config {
	return s.cfg
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
	doc := parseDocument(content)
	return Note{
		Date:     resolved,
		Body:     doc.body,
		Revision: revisionOf(content),
	}, nil
}

// Replace sets the body of the note for date and returns the new revision.
// A revision is required when the note already exists and must be omitted when creating.
// New notes use the configured template when one is set.
func (s *Store) Replace(date, body, revision string) (string, error) {
	resolved, err := s.ResolveDate(date)
	if err != nil {
		return "", err
	}
	path, err := s.path(resolved)
	if err != nil {
		return "", err
	}

	var newRevision string
	err = s.withLock(path, func() error {
		existing, readErr := s.readFile(path)
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

		var frontmatter string
		newBody := body
		if exists {
			frontmatter = parseDocument(existing).frontmatter
		} else if s.templatePath != "" {
			template, err := s.renderTemplate(resolved, path)
			if err != nil {
				return err
			}
			frontmatter = template.frontmatter
			if newBody == "" {
				newBody = template.body
			}
		}

		content := render(resolved, frontmatter, newBody)
		if err := s.writeAtomic(path, content); err != nil {
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
	files, err := s.files(from, to)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(files))
	dates := make([]string, 0, len(files))
	for _, file := range files {
		if seen[file.date] {
			continue
		}
		seen[file.date] = true
		dates = append(dates, file.date)
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
	files, err := s.files(from, to)
	if err != nil {
		return nil, err
	}

	needle := strings.ToLower(query)
	var matches []Match
	for _, file := range files {
		content, err := os.ReadFile(file.path)
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, fmt.Errorf("read note %s: %w", file.date, err)
		}
		body := parseDocument(string(content)).body
		for i, line := range strings.Split(body, "\n") {
			if !strings.Contains(strings.ToLower(line), needle) {
				continue
			}
			matches = append(matches, Match{
				Date:    file.date,
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

type noteFile struct {
	date string
	path string
}

func (s *Store) files(from, to string) ([]noteFile, error) {
	fromTime, err := parseBound(from)
	if err != nil {
		return nil, err
	}
	toTime, err := parseBound(to)
	if err != nil {
		return nil, err
	}
	parser := newPathDateParser(s.cfg.Format)

	var files []noteFile
	err = filepath.WalkDir(s.dir, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			if path != s.dir && strings.HasPrefix(entry.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(entry.Name(), ".md") {
			return nil
		}
		if s.templatePath != "" && filepath.Clean(path) == filepath.Clean(s.templatePath) {
			return nil
		}

		date, ok := s.dateFromPath(path, parser)
		if !ok {
			content, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			date, ok = dateFromFrontmatter(parseDocument(string(content)).frontmatter)
		}
		if !ok {
			return nil
		}
		t, err := ParseDate(date)
		if err != nil {
			return nil
		}
		if fromTime != nil && t.Before(*fromTime) {
			return nil
		}
		if toTime != nil && t.After(*toTime) {
			return nil
		}
		files = append(files, noteFile{date: date, path: path})
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read notes directory: %w", err)
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].date != files[j].date {
			return files[i].date < files[j].date
		}
		return files[i].path < files[j].path
	})
	return files, nil
}

func (s *Store) dateFromPath(path string, parser *pathDateParser) (string, bool) {
	rel, err := filepath.Rel(s.dir, path)
	if err != nil {
		return "", false
	}
	rel = filepath.ToSlash(rel)
	if folder := strings.Trim(filepath.ToSlash(filepath.Clean(s.cfg.Folder)), "/"); folder != "" && folder != "." {
		prefix := folder + "/"
		if !strings.HasPrefix(rel, prefix) {
			return "", false
		}
		rel = strings.TrimPrefix(rel, prefix)
	}
	if date, ok := parser.parse(strings.TrimSuffix(rel, ".md")); ok {
		return date, true
	}
	return parser.parse(rel)
}

func dateFromFrontmatter(frontmatter string) (string, bool) {
	for _, line := range strings.Split(frontmatter, "\n") {
		if line != strings.TrimLeft(line, " \t") {
			continue
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) != "date" {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		if _, err := ParseDate(value); err == nil {
			return value, true
		}
	}
	return "", false
}

func (s *Store) path(date string) (string, error) {
	t, err := ParseDate(date)
	if err != nil {
		return "", err
	}
	return s.pathFor(t)
}

func (s *Store) pathFor(t time.Time) (string, error) {
	name := formatMoment(s.cfg.Format, t)
	if strings.TrimSpace(name) == "" {
		return "", fmt.Errorf("note format %q produced an empty filename", s.cfg.Format)
	}
	filename := name
	if !strings.HasSuffix(strings.ToLower(filename), ".md") {
		filename += ".md"
	}
	parts := make([]string, 0, 2)
	if s.cfg.Folder != "" {
		parts = append(parts, s.cfg.Folder)
	}
	parts = append(parts, filename)
	return s.resolvePath(filepath.Join(parts...))
}

func (s *Store) resolveTemplatePath(template string) (string, error) {
	if !strings.HasSuffix(strings.ToLower(template), ".md") {
		template += ".md"
	}
	return s.resolvePath(template)
}

func (s *Store) resolvePath(rel string) (string, error) {
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("note path %q must be relative to the notes directory", rel)
	}
	clean := filepath.Clean(rel)
	if clean == "." || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("note path %q escapes the notes directory", rel)
	}
	full := filepath.Join(s.dir, clean)
	relative, err := filepath.Rel(s.dir, full)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("note path %q escapes the notes directory", rel)
	}
	return full, nil
}

func (s *Store) read(date string) (string, error) {
	path, err := s.path(date)
	if err != nil {
		return "", err
	}
	return s.readFile(path)
}

func (s *Store) readFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("read note %s: %w", path, err)
	}
	return string(data), nil
}

func (s *Store) writeAtomic(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create note directory: %w", err)
	}
	f, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary note: %w", err)
	}
	name := f.Name()
	defer os.Remove(name)

	if _, err := f.WriteString(content); err != nil {
		f.Close()
		return fmt.Errorf("write note %s: %w", path, err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("sync note %s: %w", path, err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("close note %s: %w", path, err)
	}
	if err := os.Chmod(name, 0o644); err != nil {
		return fmt.Errorf("chmod note %s: %w", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("replace note %s: %w", path, err)
	}
	return nil
}

func (s *Store) withLock(path string, fn func() error) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create note directory: %w", err)
	}
	lockPath := path + ".lock"
	lock, err := s.acquireLock(path, lockPath)
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

func (s *Store) acquireLock(path, lockPath string) (*os.File, error) {
	for {
		lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
		if err != nil {
			return nil, fmt.Errorf("open note lock: %w", err)
		}
		if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
			lock.Close()
			return nil, fmt.Errorf("lock note %s: %w", path, err)
		}

		held, err := lock.Stat()
		if err != nil {
			syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
			lock.Close()
			return nil, fmt.Errorf("stat note lock %s: %w", path, err)
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
			return nil, fmt.Errorf("stat note lock %s: %w", path, err)
		}
		if os.SameFile(held, current) {
			return lock, nil
		}

		syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		lock.Close()
	}
}

func (s *Store) renderTemplate(date, notePath string) (document, error) {
	data, err := os.ReadFile(s.templatePath)
	if err != nil {
		return document{}, fmt.Errorf("read note template %s: %w", s.templatePath, err)
	}
	noteDate, err := ParseDate(date)
	if err != nil {
		return document{}, err
	}
	title := strings.TrimSuffix(filepath.Base(notePath), filepath.Ext(notePath))
	content := renderPlaceholders(string(data), noteDate, time.Now().In(s.loc), title)
	return parseDocument(content), nil
}

type document struct {
	frontmatter    string
	body           string
	hasFrontmatter bool
}

func parseDocument(content string) document {
	normalized := strings.ReplaceAll(content, "\r\n", "\n")
	normalized = strings.TrimPrefix(normalized, "\ufeff")
	lines := strings.Split(normalized, "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return document{body: strings.TrimRight(normalized, "\n")}
	}

	end := -1
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" || strings.TrimSpace(lines[i]) == "..." {
			end = i
			break
		}
	}
	if end < 0 {
		return document{body: strings.TrimRight(normalized, "\n")}
	}

	body := strings.Join(lines[end+1:], "\n")
	body = strings.TrimPrefix(body, "\n")
	return document{
		frontmatter:    strings.Join(lines[1:end], "\n"),
		body:           strings.TrimRight(body, "\n"),
		hasFrontmatter: true,
	}
}

func render(date, frontmatter, body string) string {
	merged := mergeFrontmatter(frontmatter, date)
	header := fmt.Sprintf("---\n%s\n---\n", merged)
	body = strings.TrimRight(body, "\r\n")
	if body == "" {
		return header
	}
	return header + "\n" + body + "\n"
}

func mergeFrontmatter(frontmatter, date string) string {
	var extra []string
	for _, line := range strings.Split(frontmatter, "\n") {
		if strings.HasPrefix(line, "date:") {
			continue
		}
		extra = append(extra, line)
	}
	for len(extra) > 0 && strings.TrimSpace(extra[0]) == "" {
		extra = extra[1:]
	}
	for len(extra) > 0 && strings.TrimSpace(extra[len(extra)-1]) == "" {
		extra = extra[:len(extra)-1]
	}
	return strings.Join(append([]string{"date: " + date}, extra...), "\n")
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
