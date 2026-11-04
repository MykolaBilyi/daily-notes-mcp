package notes

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFormatMoment(t *testing.T) {
	date := time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC)
	got := formatMoment("YYYY/MM-MMMM/YYYY-MM-DD-dddd", date)
	want := "2026/11-November/2026-11-04-Wednesday"
	if got != want {
		t.Fatalf("formatMoment = %q, want %q", got, want)
	}

	if got := formatMoment("[Week] YYYY-MM-DD", date); got != "Week 2026-11-04" {
		t.Errorf("escaped literal = %q", got)
	}
}

func TestPathDateParser(t *testing.T) {
	parser := newPathDateParser("YYYY/MM-MMMM/YYYY-MM-DD-dddd")
	got, ok := parser.parse("2026/11-November/2026-11-04-Wednesday")
	if !ok || got != "2026-11-04" {
		t.Fatalf("parse = %q, %v", got, ok)
	}
	if _, ok := parser.parse("2026/11-November/not-a-date"); ok {
		t.Fatal("expected invalid path to be rejected")
	}
}

func TestLoadConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daily-notes.json")
	content := `{"folder":"Daily","format":"YYYY/MM-MMMM/YYYY-MM-DD-dddd","template":"Daily/Template"}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("LoadConfig: %v", err)
	}
	if cfg.Folder != "Daily" || cfg.Format != "YYYY/MM-MMMM/YYYY-MM-DD-dddd" || cfg.Template != "Daily/Template" {
		t.Errorf("cfg = %+v", cfg)
	}
}

func TestConfiguredLayoutAndTemplate(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "Daily"), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	template := "---\ntags: [daily]\ndate: {{date}}\n---\n# {{title}}\n\nDate: {{date:YYYY/MM/DD}}\nTime: {{time:HH}}\n"
	if err := os.WriteFile(filepath.Join(dir, "Daily", "Template.md"), []byte(template), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	store, err := NewWithConfig(dir, time.UTC, Config{
		Folder:   "Daily",
		Format:   "YYYY/MM-MMMM/YYYY-MM-DD-dddd",
		Template: "Daily/Template",
	})
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}

	revision, err := store.Replace("2026-11-04", "", "")
	if err != nil {
		t.Fatalf("Replace: %v", err)
	}
	if revision == "" {
		t.Fatal("expected revision")
	}

	path := filepath.Join(dir, "Daily", "2026", "11-November", "2026-11-04-Wednesday.md")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content := string(data)
	for _, want := range []string{
		"date: 2026-11-04",
		"tags: [daily]",
		"# 2026-11-04-Wednesday",
		"Date: 2026/11/04",
	} {
		if !strings.Contains(content, want) {
			t.Errorf("content missing %q:\n%s", want, content)
		}
	}
	if strings.Count(content, "date:") != 1 {
		t.Errorf("expected one date property:\n%s", content)
	}

	note, err := store.Get("2026-11-04")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !strings.HasPrefix(note.Body, "# 2026-11-04-Wednesday") {
		t.Errorf("body = %q", note.Body)
	}

	if _, err := store.Replace("2026-11-04", "updated body", revision); err != nil {
		t.Fatalf("Replace existing: %v", err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	content = string(data)
	if !strings.Contains(content, "tags: [daily]") {
		t.Errorf("frontmatter not preserved:\n%s", content)
	}
	if !strings.Contains(content, "updated body") || strings.Contains(content, "# 2026-11-04-Wednesday") {
		t.Errorf("body not replaced:\n%s", content)
	}
}

func TestCustomLayoutListAndSearch(t *testing.T) {
	dir := t.TempDir()
	store, err := NewWithConfig(dir, time.UTC, Config{
		Folder: "Daily",
		Format: "YYYY/MM-MMMM/YYYY-MM-DD-dddd",
	})
	if err != nil {
		t.Fatalf("NewWithConfig: %v", err)
	}
	if _, err := store.Replace("2026-11-04", "TODO: ship", ""); err != nil {
		t.Fatalf("Replace server note: %v", err)
	}

	obsidianPath := filepath.Join(dir, "Daily", "2026", "11-November", "2026-11-03-Tuesday.md")
	if err := os.MkdirAll(filepath.Dir(obsidianPath), 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(obsidianPath, []byte("Existing obsidian note\nTODO: migrate\n"), 0o644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}

	dates, err := store.List("", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if strings.Join(dates, ",") != "2026-11-03,2026-11-04" {
		t.Fatalf("List = %v", dates)
	}

	matches, err := store.Search("todo", "", "", 0)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(matches) != 2 {
		t.Fatalf("matches = %+v", matches)
	}
	if matches[0].Date != "2026-11-03" || matches[1].Date != "2026-11-04" {
		t.Errorf("matches = %+v", matches)
	}
}

func TestConfigRejectsPathTraversal(t *testing.T) {
	dir := t.TempDir()
	for _, cfg := range []Config{
		{Folder: "../outside", Format: DefaultFormat},
		{Format: "../YYYY-MM-DD"},
		{Format: DefaultFormat, Template: "../Template"},
	} {
		if _, err := NewWithConfig(dir, time.UTC, cfg); err == nil {
			t.Errorf("NewWithConfig(%+v) expected an error", cfg)
		}
	}
}

func TestConfigRequiresDateTokens(t *testing.T) {
	dir := t.TempDir()
	for _, format := range []string{"Notes", "YYYY/MMMM", "MM-DD"} {
		if _, err := NewWithConfig(dir, time.UTC, Config{Format: format}); err == nil {
			t.Errorf("NewWithConfig format %q expected an error", format)
		}
	}
}
