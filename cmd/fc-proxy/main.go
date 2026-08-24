// Command fc-proxy is a single-binary interception proxy for the Flexcube
// (SANGAM) APIs. A tester swaps the base URL in Postman for this one and sends
// plain JSON; the proxy generates the unique ids, fetches and caches the bearer
// token, injects the SessionContext, AES-encrypts the request, decrypts the
// reply, and records every exchange in a local SQLite ledger that can be
// exported to CSV.
//
//	fc-proxy serve
//	fc-proxy export -date 2026-08-21
//	fc-proxy export -from 2026-08-01 -to 2026-08-21 -out august.csv
package main

import (
	"fmt"
	"log"
	"os"
	"strings"
)

const version = "1.0.0"

func main() {
	log.SetFlags(log.Ldate | log.Ltime)

	command := "serve"
	args := os.Args[1:]
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		command, args = args[0], args[1:]
	}

	var err error
	switch command {
	case "serve":
		err = runServe(args)
	case "export":
		err = runExport(args)
	case "version", "--version", "-v":
		fmt.Printf("fc-proxy %s\n", version)
	case "help", "--help", "-h":
		usage()
	default:
		usage()
		err = fmt.Errorf("unknown command %q", command)
	}

	if err != nil {
		fmt.Fprintf(os.Stderr, "\nerror: %v\n", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintf(os.Stderr, `fc-proxy %s - Flexcube testing proxy with a local ledger

Usage:
  fc-proxy serve  [options]   Start the proxy (default command)
  fc-proxy export [options]   Export the ledger to CSV
  fc-proxy version            Print the version

Run 'fc-proxy serve -h' or 'fc-proxy export -h' for the options of each.
`, version)
}
