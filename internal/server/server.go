package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mykolabilyi/daily-notes-mcp/internal/middleware"
	"github.com/mykolabilyi/daily-notes-mcp/internal/notes"
	"github.com/mykolabilyi/daily-notes-mcp/internal/version"
)

// New creates the daily notes MCP server.
func New(store *notes.Store) *mcp.Server {
	s := mcp.NewServer(&mcp.Implementation{
		Name:        "daily-notes-mcp",
		Title:       "Daily Notes",
		Description: "Read and edit daily markdown notes.",
		Version:     version.Version,
	}, nil)
	s.AddReceivingMiddleware(middleware.CreateLoggingMiddleware())
	registerTools(s, store)
	return s
}

// NewHTTPHandler returns an HTTP handler exposing the MCP endpoint and health probes.
func NewHTTPHandler(s *mcp.Server, store *notes.Store) http.Handler {
	mcpHandler := mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server {
		return s
	}, nil)

	mux := http.NewServeMux()
	mux.Handle("/mcp", mcpHandler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, "ok\n")
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := store.Ready(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		io.WriteString(w, "ok\n")
	})
	return mux
}

type getNoteInput struct {
	Date string `json:"date" jsonschema:"note date in YYYY-MM-DD format"`
}

type getOutput struct {
	Date     string `json:"date"`
	Found    bool   `json:"found"`
	Body     string `json:"body"`
	Revision string `json:"revision"`
}

type updateTodayInput struct {
	Body     string `json:"body" jsonschema:"full markdown body of the note"`
	Revision string `json:"revision,omitempty" jsonschema:"current revision from get_today_note; required to replace an existing note, omit to create"`
}

type updateOutput struct {
	Date     string `json:"date"`
	Revision string `json:"revision"`
}

type searchInput struct {
	Query      string `json:"query" jsonschema:"case-insensitive text to search for"`
	From       string `json:"from,omitempty" jsonschema:"earliest date in YYYY-MM-DD format (inclusive)"`
	To         string `json:"to,omitempty" jsonschema:"latest date in YYYY-MM-DD format (inclusive)"`
	MaxResults int    `json:"max_results,omitempty" jsonschema:"maximum number of matches to return; defaults to 50"`
}

type searchOutput struct {
	Query   string        `json:"query"`
	Matches []notes.Match `json:"matches"`
}

func registerTools(s *mcp.Server, store *notes.Store) {
	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_today_note",
		Description: "Get today's daily note, creating it with an empty body if it does not exist.",
	}, getTodayNote(store))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "update_today_note",
		Description: "Replace the full markdown body of today's daily note. Requires the current revision for existing notes.",
	}, updateTodayNote(store))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "get_note",
		Description: "Get the markdown body and revision of a daily note by date.",
	}, getNote(store))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "search_notes",
		Description: "Search daily note bodies for case-insensitive text and return matching line snippets.",
	}, searchNotes(store))
}

func getTodayNote(store *notes.Store) mcp.ToolHandlerFor[struct{}, getOutput] {
	return func(_ context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, getOutput, error) {
		today := store.Today()
		note, err := store.Get(today)
		if errors.Is(err, notes.ErrNotFound) {
			_, createErr := store.Replace(today, "", "")
			if createErr != nil && !errors.Is(createErr, notes.ErrRevisionRequired) && !errors.Is(createErr, notes.ErrRevisionMismatch) {
				return nil, getOutput{}, createErr
			}
			note, err = store.Get(today)
		}
		if err != nil {
			return nil, getOutput{}, err
		}

		out := getOutput{
			Date:     note.Date,
			Found:    true,
			Body:     note.Body,
			Revision: note.Revision,
		}
		text := note.Body
		if text == "" {
			text = fmt.Sprintf("Daily note for %s is empty.", note.Date)
		}
		return textResult(text), out, nil
	}
}

func updateTodayNote(store *notes.Store) mcp.ToolHandlerFor[updateTodayInput, updateOutput] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in updateTodayInput) (*mcp.CallToolResult, updateOutput, error) {
		today := store.Today()
		revision, err := store.Replace(today, in.Body, in.Revision)
		if err != nil {
			return nil, updateOutput{}, err
		}

		out := updateOutput{Date: today, Revision: revision}
		return textResult(fmt.Sprintf("Saved daily note for %s (revision %s).", today, revision)), out, nil
	}
}

func getNote(store *notes.Store) mcp.ToolHandlerFor[getNoteInput, getOutput] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in getNoteInput) (*mcp.CallToolResult, getOutput, error) {
		date, err := store.ResolveDate(in.Date)
		if err != nil {
			return nil, getOutput{}, err
		}
		note, err := store.Get(date)
		if errors.Is(err, notes.ErrNotFound) {
			out := getOutput{Date: date}
			return textResult(fmt.Sprintf("No daily note exists for %s.", date)), out, nil
		}
		if err != nil {
			return nil, getOutput{}, err
		}

		out := getOutput{
			Date:     note.Date,
			Found:    true,
			Body:     note.Body,
			Revision: note.Revision,
		}
		text := note.Body
		if text == "" {
			text = fmt.Sprintf("Daily note for %s is empty.", note.Date)
		}
		return textResult(text), out, nil
	}
}

func searchNotes(store *notes.Store) mcp.ToolHandlerFor[searchInput, searchOutput] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in searchInput) (*mcp.CallToolResult, searchOutput, error) {
		matches, err := store.Search(in.Query, in.From, in.To, in.MaxResults)
		if err != nil {
			return nil, searchOutput{}, err
		}
		out := searchOutput{Query: in.Query, Matches: matches}
		if len(matches) == 0 {
			return textResult(fmt.Sprintf("No matches for %q.", in.Query)), out, nil
		}

		var b strings.Builder
		for _, match := range matches {
			fmt.Fprintf(&b, "%s:%d: %s\n", match.Date, match.Line, match.Snippet)
		}
		return textResult(strings.TrimRight(b.String(), "\n")), out, nil
	}
}

func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: text},
		},
	}
}
