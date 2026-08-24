package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/ZethicTech/fc-proxy/internal/config"
	"github.com/ZethicTech/fc-proxy/internal/ledger"
	"github.com/ZethicTech/fc-proxy/internal/proxy"
)

// runServe starts the interception proxy on the configured port.
func runServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	var (
		port     = fs.String("port", "", "port to listen on (default FC_PROXY_PORT or 9090)")
		baseURL  = fs.String("base-url", "", "Flexcube base URL (default CHANNEL_SANGAM_BASE_URL)")
		dbPath   = fs.String("db", "", "ledger file (default FC_PROXY_DB or fc-proxy-ledger.db)")
		envFile  = fs.String("env", "", "path to an env file to load")
		insecure = fs.Bool("insecure", false, "skip TLS verification for the upstream (self-signed certs)")
		quiet    = fs.Bool("quiet", false, "only log errors")
	)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: fc-proxy serve [options]\n\nStart the interception proxy.\n\nOptions:\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	config.LoadEnvFiles(*envFile)

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *port != "" {
		cfg.Port = *port
	}
	if *baseURL != "" {
		cfg.BaseURL = strings.TrimRight(*baseURL, "/")
	}
	if *dbPath != "" {
		cfg.DBPath = *dbPath
	}
	if *insecure {
		cfg.InsecureTLS = true
	}
	if *quiet {
		cfg.Verbose = false
	}

	log, err := ledger.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer log.Close()

	printBanner(cfg)

	// Every path is forwarded - the proxy owns no routes of its own, so a
	// tester only ever swaps the base URL.
	addr := ":" + cfg.Port
	if err := http.ListenAndServe(addr, proxy.New(cfg, log)); err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	return nil
}

func printBanner(cfg *config.Config) {
	tokenMode := "OAuth client_credentials"
	if cfg.UsesStaticToken() {
		tokenMode = "static-dev-token (APP_ENV=" + cfg.AppEnv + ")"
	}
	fmt.Printf(`
fc-proxy %s

  Listening on   http://localhost:%s
  Forwarding to  %s
  Ledger         %s
  Operator       %s
  Auth           %s

  In Postman, replace the Flexcube base URL with http://localhost:%s and send
  plain JSON. Everything else - token, x-session-id, x-message-id,
  ExternalReferenceNo, SessionContext, and the AES layer in both directions -
  is handled here.

`, version, cfg.Port, cfg.BaseURL, cfg.DBPath, cfg.Operator, tokenMode, cfg.Port)
}
