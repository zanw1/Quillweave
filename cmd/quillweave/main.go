package main

import (
	"fmt"
	"github.com/wailsapp/wails/v3/pkg/application"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"quillweave/frontend"
	"quillweave/internal/core"
	"quillweave/internal/desktop"
	"quillweave/internal/headless"
	"quillweave/internal/platform"
)

var production = "false"

// desktopAssets restricts production resources to the local app and enables Vite in development.
func desktopAssets(next http.Handler) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if production == "true" {
			writer.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: blob:; connect-src 'self'; font-src 'self' data:")
		}
		next.ServeHTTP(writer, request)
	})
}

// run starts the native window with shared assets and the existing data location.
func run() error {
	directory, err := platform.DataDirectory()
	if err != nil {
		return err
	}
	engine, err := core.NewEngine(filepath.Join(directory, "quillweave.sqlite3"))
	if err != nil {
		return err
	}
	defer engine.Close()
	service := desktop.NewService(engine)
	app := application.New(application.Options{Name: "Quillweave", Description: "Quillweave local fiction workspace", Services: []application.Service{application.NewService(service)}, Flags: map[string]any{"quillweaveDesktop": true}, Assets: application.AssetOptions{Handler: application.AssetFileServerFS(frontend.Assets()), Middleware: desktopAssets, DisableLogging: true}, Mac: application.MacOptions{ApplicationShouldTerminateAfterLastWindowClosed: true}, LogLevel: slog.LevelWarn, OnShutdown: func() { _ = engine.Close() }})
	desktop.Attach(service, app)
	app.Window.NewWithOptions(application.WebviewWindowOptions{Title: "Quillweave", Width: 1440, Height: 940, MinWidth: 860, MinHeight: 640, BackgroundColour: application.NewRGB(255, 255, 255), URL: "/"})
	return app.Run()
}

// main reports startup failures using credential-free diagnostics.
func main() {
	if len(os.Args) > 1 && headless.IsCommand(os.Args[1]) {
		os.Exit(headless.Run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
	}
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
