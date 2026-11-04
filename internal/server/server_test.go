package server_test

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mykolabilyi/daily-notes-mcp/internal/notes"
	"github.com/mykolabilyi/daily-notes-mcp/internal/server"
)

func TestTools(t *testing.T) {
	store, err := notes.New(t.TempDir(), time.UTC)
	if err != nil {
		t.Fatalf("notes.New: %v", err)
	}

	ts := httptest.NewServer(server.NewHTTPHandler(server.New(store), store))
	defer ts.Close()

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "0.1.0"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{
		Endpoint:             ts.URL + "/mcp",
		DisableStandaloneSSE: true,
	}, nil)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer session.Close()

	tools, err := session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
	}
	sort.Strings(names)
	want := []string{"get_note", "get_today_note", "search_notes", "update_today_note"}
	if fmt.Sprint(names) != fmt.Sprint(want) {
		t.Errorf("tools = %v, want %v", names, want)
	}

	today := store.Today()

	created := callTool(t, session, "get_today_note", map[string]any{})
	if created.IsError {
		t.Fatalf("get today failed: %s", resultText(created))
	}
	createdOut := structuredMap(t, created)
	if createdOut["date"] != today {
		t.Errorf("date = %v, want %s", createdOut["date"], today)
	}
	if createdOut["found"] != true {
		t.Errorf("found = %v, want true", createdOut["found"])
	}
	if createdOut["body"] != "" {
		t.Errorf("body = %q, want empty", createdOut["body"])
	}
	revision := createdOut["revision"].(string)
	if revision == "" {
		t.Fatal("expected a revision")
	}

	conflict := callTool(t, session, "update_today_note", map[string]any{
		"body":     "overwritten",
		"revision": "stale",
	})
	if !conflict.IsError {
		t.Fatal("expected stale revision to fail")
	}

	updated := callTool(t, session, "update_today_note", map[string]any{
		"body":     "updated\nTODO: ship",
		"revision": revision,
	})
	if updated.IsError {
		t.Fatalf("update failed: %s", resultText(updated))
	}
	if structuredMap(t, updated)["revision"] == revision {
		t.Error("expected revision to change")
	}

	got := callTool(t, session, "get_note", map[string]any{"date": today})
	if got.IsError {
		t.Fatalf("get failed: %s", resultText(got))
	}
	gotOut := structuredMap(t, got)
	if gotOut["body"] != "updated\nTODO: ship" {
		t.Errorf("body = %q", gotOut["body"])
	}
	if gotOut["found"] != true {
		t.Errorf("found = %v, want true", gotOut["found"])
	}

	missing := callTool(t, session, "get_note", map[string]any{"date": "2000-01-01"})
	if missing.IsError {
		t.Fatalf("get missing failed: %s", resultText(missing))
	}
	if structuredMap(t, missing)["found"] != false {
		t.Error("expected found=false for missing note")
	}

	missingDate := callTool(t, session, "get_note", map[string]any{})
	if !missingDate.IsError {
		t.Fatal("expected get_note without date to fail")
	}

	found := callTool(t, session, "search_notes", map[string]any{"query": "todo"})
	if found.IsError {
		t.Fatalf("search failed: %s", resultText(found))
	}
	matches := structuredMap(t, found)["matches"].([]any)
	if len(matches) != 1 {
		t.Fatalf("matches = %v", matches)
	}
	match := matches[0].(map[string]any)
	if match["date"] != today || match["line"] != float64(2) {
		t.Errorf("match = %v", match)
	}
}

func TestHealthEndpoints(t *testing.T) {
	store, err := notes.New(t.TempDir(), time.UTC)
	if err != nil {
		t.Fatalf("notes.New: %v", err)
	}
	ts := httptest.NewServer(server.NewHTTPHandler(server.New(store), store))
	defer ts.Close()

	for _, path := range []string{"/healthz", "/readyz"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("GET %s status = %d (%s)", path, resp.StatusCode, body)
		}
	}
}

func callTool(t *testing.T, session *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		t.Fatalf("CallTool %s: %v", name, err)
	}
	return result
}

func structuredMap(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	value, ok := result.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("StructuredContent type = %T, want map[string]any", result.StructuredContent)
	}
	return value
}

func resultText(result *mcp.CallToolResult) string {
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			return text.Text
		}
	}
	return ""
}
