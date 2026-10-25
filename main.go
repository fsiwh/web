// web is the server-rendered Fachschaft website.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"github.com/fachschaftinformatik/web/internal/database"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

// Version is injected by the image build.
var Version = "dev"

// Revision identifies the source commit independently of the release version.
var Revision string

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:]); err != nil {
		log.Print(err)
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string) error {
	flags := flag.NewFlagSet("web", flag.ContinueOnError)
	addr := flags.String("addr", "127.0.0.1:8090", "HTTP listen address")
	databasePath := flags.String("database", "data/sqlite.db", "Writable SQLite database path")
	blobs := flags.String("blobs", "", "Local object directory; otherwise use configured S3 or blobs beside the database")
	migrate := flags.Bool("migrate-only", false, "Apply database schema migrations and exit without loading site settings")
	importRegulations := flags.String("import-regulations", "", "Import reviewed regulation PDFs from a directory into configured file storage and exit")
	version := flags.Bool("version", false, "Print application version")
	health := flags.String("check-health", "", "Check a running application's health URL and exit")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected arguments: %v", flags.Args())
	}
	if *version {
		fmt.Println(Version)
		return nil
	}
	if *health != "" {
		client := &http.Client{Timeout: 3 * time.Second}
		response, err := client.Get(*health)
		if err != nil {
			return err
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return fmt.Errorf("health check returned HTTP %d", response.StatusCode)
		}
		return nil
	}
	if *migrate {
		return database.Migrate(*databasePath, log.Default())
	}
	app, err := newApplication(ctx, *databasePath, *blobs)
	if err != nil {
		return err
	}
	defer app.Close()
	if *importRegulations != "" {
		return app.exams.ImportRegulationDocuments(ctx, *importRegulations)
	}
	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return err
	}
	return serve(ctx, listener, app.handler)
}

func serve(ctx context.Context, listener net.Listener, handler http.Handler) error {
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 30 * time.Second, WriteTimeout: 60 * time.Second, IdleTimeout: 120 * time.Second}
	result := make(chan error, 1)
	go func() { result <- server.Serve(listener) }()
	log.Printf("Web %s: http://%s/", Version, listener.Addr())
	select {
	case err := <-result:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
		shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(shutdown); err != nil {
			_ = server.Close()
			return err
		}
		if err := <-result; !errors.Is(err, http.ErrServerClosed) {
			return err
		}
		return nil
	}
}
