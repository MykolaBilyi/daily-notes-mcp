package notes

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	store, err := New(t.TempDir(), time.UTC)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return store
}

func TestReplaceCreateAndGet(t *testing.T) {
	store := newStore(t)

	rev, err := store.Replace("2026-11-04", "Hello\nworld", "")
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if rev == "" {
		t.Fatal("expected a revision")
	}

	note, err := store.Get("2026-11-04")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if note.Date != "2026-11-04" {
		t.Errorf("Date = %q, want 2026-11-04", note.Date)
	}
	if note.Body != "Hello\nworld" {
		t.Errorf("Body = %q, want %q", note.Body, "Hello\nworld")
	}
	if note.Revision != rev {
		t.Errorf("Revision = %q, want %q", note.Revision, rev)
	}

	data, err := os.ReadFile(filepath.Join(store.Dir(), "2026-11-04.md"))
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	want := "---\ndate: 2026-11-04\n---\n\nHello\nworld\n"
	if string(data) != want {
		t.Errorf("file = %q, want %q", string(data), want)
	}
	if strings.Contains(string(data), "created") || strings.Contains(string(data), "updated") {
		t.Error("frontmatter must only contain date")
	}
}

func TestReplaceEmptyBody(t *testing.T) {
	store := newStore(t)

	if _, err := store.Replace("2026-11-04", "", ""); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	note, err := store.Get("2026-11-04")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if note.Body != "" {
		t.Errorf("Body = %q, want empty", note.Body)
	}
}

func TestGetMissing(t *testing.T) {
	store := newStore(t)

	_, err := store.Get("2026-11-04")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Get error = %v, want ErrNotFound", err)
	}
}

func TestReplaceConcurrency(t *testing.T) {
	store := newStore(t)

	first, err := store.Replace("2026-11-04", "first", "")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := store.Replace("2026-11-04", "second", ""); !errors.Is(err, ErrRevisionRequired) {
		t.Fatalf("replace without revision error = %v, want ErrRevisionRequired", err)
	}
	if _, err := store.Replace("2026-11-04", "second", "stale"); !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("replace with stale revision error = %v, want ErrRevisionMismatch", err)
	}

	second, err := store.Replace("2026-11-04", "second", first)
	if err != nil {
		t.Fatalf("replace with revision: %v", err)
	}
	if second == first {
		t.Error("expected revision to change")
	}

	note, err := store.Get("2026-11-04")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if note.Body != "second" {
		t.Errorf("Body = %q, want second", note.Body)
	}
}

func TestLockFileCleanup(t *testing.T) {
	store := newStore(t)

	if _, err := store.Replace("2026-11-04", "body", ""); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(store.Dir(), "*.lock"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("lock files remain: %v", matches)
	}
}

func TestConcurrentReplaceCleansLockFiles(t *testing.T) {
	store := newStore(t)

	const writers = 8
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := range writers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 100 {
				note, err := store.Get("2026-11-04")
				if err != nil && !errors.Is(err, ErrNotFound) {
					errs <- err
					return
				}
				revision := ""
				if err == nil {
					revision = note.Revision
				}
				_, err = store.Replace("2026-11-04", fmt.Sprintf("writer-%d", i), revision)
				if err == nil {
					return
				}
				if !errors.Is(err, ErrRevisionMismatch) && !errors.Is(err, ErrRevisionRequired) {
					errs <- err
					return
				}
			}
			errs <- errors.New("too many replace retries")
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent replace: %v", err)
	}

	matches, err := filepath.Glob(filepath.Join(store.Dir(), "*.lock"))
	if err != nil {
		t.Fatalf("Glob: %v", err)
	}
	if len(matches) != 0 {
		t.Errorf("lock files remain: %v", matches)
	}
}

func TestCreateWithRevisionFails(t *testing.T) {
	store := newStore(t)

	_, err := store.Replace("2026-11-04", "body", "some-revision")
	if !errors.Is(err, ErrRevisionMismatch) {
		t.Fatalf("error = %v, want ErrRevisionMismatch", err)
	}
}

func TestReplaceRewritesFrontmatter(t *testing.T) {
	store := newStore(t)
	path := filepath.Join(store.Dir(), "2026-11-04.md")
	content := "---\ndate: 2026-11-04\ncreated: yesterday\n---\n\nold\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	note, err := store.Get("2026-11-04")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, err := store.Replace("2026-11-04", "new", note.Revision); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	want := "---\ndate: 2026-11-04\n---\n\nnew\n"
	if string(data) != want {
		t.Errorf("file = %q, want %q", string(data), want)
	}
}

func TestList(t *testing.T) {
	store := newStore(t)
	for _, date := range []string{"2026-11-03", "2026-11-01", "2026-11-02"} {
		if _, err := store.Replace(date, "body", ""); err != nil {
			t.Fatalf("Replace %s: %v", date, err)
		}
	}

	dates, err := store.List("", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"2026-11-01", "2026-11-02", "2026-11-03"}
	if strings.Join(dates, ",") != strings.Join(want, ",") {
		t.Errorf("List = %v, want %v", dates, want)
	}

	dates, err = store.List("2026-11-02", "2026-11-03")
	if err != nil {
		t.Fatalf("List range: %v", err)
	}
	if strings.Join(dates, ",") != "2026-11-02,2026-11-03" {
		t.Errorf("List range = %v", dates)
	}
}

func TestSearch(t *testing.T) {
	store := newStore(t)
	if _, err := store.Replace("2026-11-01", "first line\nTODO: write docs", ""); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if _, err := store.Replace("2026-11-02", "nothing here", ""); err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if _, err := store.Replace("2026-11-03", "todo later", ""); err != nil {
		t.Fatalf("Replace: %v", err)
	}

	matches, err := store.Search("todo", "", "", 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("len(matches) = %d, want 2", len(matches))
	}
	if matches[0].Date != "2026-11-01" || matches[0].Line != 2 || matches[0].Snippet != "TODO: write docs" {
		t.Errorf("first match = %+v", matches[0])
	}
	if matches[1].Date != "2026-11-03" || matches[1].Line != 1 {
		t.Errorf("second match = %+v", matches[1])
	}

	matches, err = store.Search("todo", "", "", 1)
	if err != nil {
		t.Fatalf("Search limited: %v", err)
	}
	if len(matches) != 1 {
		t.Errorf("len(matches) = %d, want 1", len(matches))
	}

	if _, err := store.Search("  ", "", "", 0); err == nil {
		t.Error("expected an error for empty query")
	}
}

func TestInvalidDates(t *testing.T) {
	store := newStore(t)
	for _, date := range []string{"2026-1-1", "../evil", "2026-13-01", "not-a-date"} {
		if _, err := store.ResolveDate(date); err == nil {
			t.Errorf("ResolveDate(%q) expected an error", date)
		}
		if _, err := store.Replace(date, "body", ""); err == nil {
			t.Errorf("Replace(%q) expected an error", date)
		}
	}
}

func TestReady(t *testing.T) {
	store := newStore(t)
	if err := store.Ready(context.Background()); err != nil {
		t.Fatalf("Ready: %v", err)
	}
}
