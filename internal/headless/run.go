// Package headless shares CLI and MCP startup between desktop and console executables.
package headless

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"quillweave/internal/core"
	"quillweave/internal/mcpserver"
	"quillweave/internal/platform"
	"syscall"
)

// IsCommand recognizes both the existing CLI and the long-lived stdio server entrypoint.
func IsCommand(name string) bool { return name == "mcp" || core.IsCLICommand(name) }

// Run routes console operations without initializing Wails, browser assets or model credentials.
func Run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] != "mcp" {
		return core.RunCLIWithInput(args, stdin, stdout, stderr)
	}
	flags := flag.NewFlagSet("quillweave mcp", flag.ContinueOnError)
	flags.SetOutput(stderr)
	project := flags.String("project", "", "Connect one project by its ID; omit it to choose the project on each call")
	database := flags.String("db", os.Getenv("QUILLWEAVE_DB"), "Shared workspace database")
	readOnly := flags.Bool("read-only", false, "Expose only pure reading tools")
	client := flags.String("client", "mcp-client", "Provenance label for the connected client")
	if err := flags.Parse(args[1:]); err != nil {
		if err == flag.ErrHelp {
			return 0
		}
		return 1
	}
	if flags.NArg() != 0 {
		fmt.Fprintln(stderr, "Unexpected MCP startup arguments")
		return 1
	}
	if *database == "" {
		directory, err := platform.DataDirectory()
		if err != nil {
			fmt.Fprintln(stderr, err)
			return 1
		}
		*database = filepath.Join(directory, "quillweave.sqlite3")
	}
	service, err := core.OpenExternalService(*database, core.ExternalOptions{ProjectID: *project, ReadOnly: *readOnly, Client: *client, Transport: "mcp"})
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer service.Close()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := mcpserver.Run(ctx, service, *readOnly); err != nil && ctx.Err() == nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
