package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/mykolabilyi/daily-notes-mcp/internal/notes"
	"github.com/mykolabilyi/daily-notes-mcp/internal/server"
	"github.com/mykolabilyi/daily-notes-mcp/internal/version"
)

func main() {
	host := flag.String("host", env("HOST", "localhost"), "host to listen on")
	port := flag.Int("port", envInt("PORT", 8000), "port to listen on")
	notesDir := flag.String("notes-dir", env("NOTES_DIR", "./notes"), "directory containing daily notes")
	tz := flag.String("tz", env("NOTES_TZ", "UTC"), "timezone used to determine today's date")

	out := flag.CommandLine.Output()
	flag.Usage = func() {
		fmt.Fprintf(out, "Usage: %s [flags]\n\n", os.Args[0])
		fmt.Fprintf(out, "Run the daily notes MCP server over streamable HTTP.\n\n")
		fmt.Fprintf(out, "Options:\n")
		flag.PrintDefaults()
		fmt.Fprintf(out, "\nEnvironment defaults: HOST, PORT, NOTES_DIR, NOTES_TZ\n")
	}
	flag.Parse()

	if flag.NArg() != 0 {
		fmt.Fprintf(out, "Error: unexpected argument %q\n\n", flag.Arg(0))
		flag.Usage()
		os.Exit(1)
	}

	loc, err := time.LoadLocation(*tz)
	if err != nil {
		log.Fatalf("Invalid timezone %q: %v", *tz, err)
	}
	store, err := notes.New(*notesDir, loc)
	if err != nil {
		log.Fatalf("Failed to open notes store: %v", err)
	}

	mcpServer := server.New(store)
	handler := server.NewHTTPHandler(mcpServer, store)
	addr := net.JoinHostPort(*host, strconv.Itoa(*port))
	httpServer := &http.Server{
		Addr:    addr,
		Handler: handler,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := httpServer.Shutdown(shutdownCtx); err != nil {
			log.Printf("Shutdown error: %v", err)
		}
	}()

	log.Printf("Daily notes MCP server %s listening on http://%s/mcp", version.Version, addr)
	log.Printf("Notes directory: %s (timezone %s)", store.Dir(), loc)
	log.Printf("Tools: get_today_note, update_today_note, get_note, search_notes")

	if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("Server failed: %v", err)
	}
}

func env(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) int {
	value, ok := os.LookupEnv(key)
	if !ok {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
}
