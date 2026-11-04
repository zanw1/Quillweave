package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"quillweave/frontend"
	"quillweave/internal/core"
	"quillweave/internal/preview"
	"strconv"
	"syscall"
	"time"
)

// run serves the loopback preview and cleans up workers and connections on exit.
func run() error {
	directory := os.Getenv("QUILLWEAVE_DATA_DIR")
	if directory == "" {
		directory = filepath.Join("data", "preview")
	}
	port := 1421
	if value := os.Getenv("QUILLWEAVE_PORT"); value != "" {
		parsed, err := strconv.ParseUint(value, 10, 16)
		if err != nil {
			return err
		}
		port = int(parsed)
	}
	engine, err := core.NewEngine(filepath.Join(directory, "quillweave.sqlite3"))
	if err != nil {
		return err
	}
	defer engine.Close()
	var assets fs.FS = frontend.Assets()
	if info, err := os.Stat("frontend/dist/index.html"); err == nil && !info.IsDir() {
		assets = os.DirFS("frontend/dist")
	}
	server := &http.Server{Addr: fmt.Sprintf("127.0.0.1:%d", port), Handler: preview.Handler(engine, assets, port), ReadHeaderTimeout: 10 * time.Second}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		deadline, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(deadline)
	}()
	fmt.Printf("Quillweave local preview: http://127.0.0.1:%d\n", port)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// main reports startup errors while run owns resource cleanup.
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
