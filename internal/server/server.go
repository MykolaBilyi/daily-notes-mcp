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

type getInput struct {
	Date string `json:"date,omitempty" jsonschema:"note date in YYYY-MM-DD format; defaults to today"`
}

type getOutput struct {
	Date     string `json:"date"`
	Found    bool   `json:"found"`
	Body     string `json:"body"`
	Revision string `json:"revision"`
}

type replaceInput struct {
	Date     string `json:"date,omitempty" jsonschema:"note date in YYYY-MM-DD format; defaults to today"`
	Body     string `json:"body" jsonschema:"full markdown body of the note"`
	Revision string `json:"revision,omitempty" jsonschema:"current revision from get_daily_note; required to replace an existing note, omit to create"`
}

type replaceOutput struct {
	Date     string `json:"date"`
	Revision string `json:"revision"`
}

type listInput struct {
	From string `json:"from,omitempty" jsonschema:"earliest date in YYYY-MM-DD format (inclusive)"`
	To   string `json:"to,omitempty" jsonschema:"latest date in YYYY-MM-DD format (inclusive)"`
}

type listOutput struct {
	Dates []string `json:"dates"`
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
		Name:        "get_daily_note",
		Description: "Get the markdown body and revision of a daily note by date; defaults to today.",
	}, getDailyNote(store))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "replace_daily_note",
		Description: "Replace the full markdown body of a daily note. Requires the current revision for existing notes.",
	}, replaceDailyNote(store))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "list_daily_notes",
		Description: "List dates that have daily notes, optionally bounded by an inclusive date range.",
	}, listDailyNotes(store))

	mcp.AddTool(s, &mcp.Tool{
		Name:        "search_daily_notes",
		Description: "Search daily note bodies for case-insensitive text and return matching line snippets.",
	}, searchDailyNotes(store))
}

func getDailyNote(store *notes.Store) mcp.ToolHandlerFor[getInput, getOutput] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in getInput) (*mcp.CallToolResult, getOutput, error) {
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

func replaceDailyNote(store *notes.Store) mcp.ToolHandlerFor[replaceInput, replaceOutput] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in replaceInput) (*mcp.CallToolResult, replaceOutput, error) {
		date, err := store.ResolveDate(in.Date)
		if err != nil {
			return nil, replaceOutput{}, err
		}
		revision, err := store.Replace(date, in.Body, in.Revision)
		if err != nil {
			return nil, replaceOutput{}, err
		}

		out := replaceOutput{Date: date, Revision: revision}
		return textResult(fmt.Sprintf("Saved daily note for %s (revision %s).", date, revision)), out, nil
	}
}

func listDailyNotes(store *notes.Store) mcp.ToolHandlerFor[listInput, listOutput] {
	return func(_ context.Context, _ *mcp.CallToolRequest, in listInput) (*mcp.CallToolResult, listOutput, error) {
		dates, err := store.List(in.From, in.To)
		if err != nil {
			return nil, listOutput{}, err
		}
		out := listOutput{Dates: dates}
		if len(dates) == 0 {
			return textResult("No daily notes found."), out, nil
		}
		return textResult(strings.Join(dates, "\n")), out, nil
	}
}

func searchDailyNotes(store *notes.Store) mcp.ToolHandlerFor[searchInput, searchOutput] {
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
