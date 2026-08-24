package main

import (
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ZethicTech/fc-proxy/internal/config"
	"github.com/ZethicTech/fc-proxy/internal/ledger"
)

// runExport dumps ledger rows for a single date or a date range into a CSV.
func runExport(args []string) error {
	fs := flag.NewFlagSet("export", flag.ExitOnError)
	var (
		date    = fs.String("date", "", "single day to export, YYYY-MM-DD (shorthand for -from X -to X)")
		from    = fs.String("from", "", "start date, YYYY-MM-DD (inclusive)")
		to      = fs.String("to", "", "end date, YYYY-MM-DD (inclusive)")
		out     = fs.String("out", "", "output CSV path (default fc-proxy-<from>_to_<to>.csv)")
		dbPath  = fs.String("db", "", "ledger file (default FC_PROXY_DB or fc-proxy-ledger.db)")
		envFile = fs.String("env", "", "path to an env file to load")
	)
	fs.Usage = func() {
		fmt.Fprintf(os.Stderr, "Usage: fc-proxy export [options]\n\nExport the local ledger to CSV.\n\nOptions:\n")
		fs.PrintDefaults()
		fmt.Fprintf(os.Stderr, "\nExamples:\n"+
			"  fc-proxy export -date 2026-08-21\n"+
			"  fc-proxy export -from 2026-08-01 -to 2026-08-21 -out august.csv\n"+
			"  fc-proxy export -from 2026-08-01            (from that date to today)\n")
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	config.LoadEnvFiles(*envFile)

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if *dbPath != "" {
		cfg.DBPath = *dbPath
	}

	fromDate, toDate, err := resolveDateRange(*date, *from, *to)
	if err != nil {
		return err
	}

	if _, err := os.Stat(cfg.DBPath); err != nil {
		return fmt.Errorf("ledger %s not found - run 'fc-proxy serve' first, or pass -db", cfg.DBPath)
	}

	log, err := ledger.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer log.Close()

	count, err := log.Count(fromDate, toDate)
	if err != nil {
		return fmt.Errorf("count rows: %w", err)
	}

	outPath := *out
	if outPath == "" {
		if fromDate == toDate {
			outPath = fmt.Sprintf("fc-proxy-%s.csv", fromDate)
		} else {
			outPath = fmt.Sprintf("fc-proxy-%s_to_%s.csv", fromDate, toDate)
		}
	}

	file, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("create %s: %w", outPath, err)
	}
	defer file.Close()

	written, err := log.WriteCSV(file, fromDate, toDate)
	if err != nil {
		return err
	}

	fmt.Printf("Exported %d of %d exchange(s) for %s..%s to %s\n", written, count, fromDate, toDate, outPath)
	return nil
}

// resolveDateRange turns the -date/-from/-to flags into an inclusive range.
// With nothing given it exports today; with only -from it runs to today.
func resolveDateRange(date, from, to string) (string, string, error) {
	today := time.Now().Format("2006-01-02")

	if date != "" {
		if from != "" || to != "" {
			return "", "", fmt.Errorf("-date cannot be combined with -from/-to")
		}
		from, to = date, date
	}
	if from == "" && to == "" {
		from, to = today, today
	}
	if from == "" {
		from = to
	}
	if to == "" {
		to = today
	}

	for _, d := range []string{from, to} {
		if _, err := time.Parse("2006-01-02", d); err != nil {
			return "", "", fmt.Errorf("invalid date %q - expected YYYY-MM-DD", d)
		}
	}
	if from > to {
		return "", "", fmt.Errorf("-from (%s) is after -to (%s)", from, to)
	}

	return from, to, nil
}
