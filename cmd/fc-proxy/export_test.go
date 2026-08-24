package main

import (
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ZethicTech/fc-proxy/internal/ledger"
)

// TestExportCSV runs the export command over a seeded ledger.
func TestExportCSV(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "ledger.db")
	log, err := ledger.Open(dbPath)
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}

	today := time.Now()
	for i := 0; i < 3; i++ {
		if _, err := log.Insert(&ledger.Entry{
			TS:            today,
			Operator:      "tester",
			Method:        "POST",
			Path:          "/prov/v1/thing",
			RequestPlain:  `{"Data":{"x":1}}`,
			ResponsePlain: `{"ok":true}`,
			StatusCode:    200,
			ServiceCode:   "S001",
		}); err != nil {
			t.Fatalf("insert: %v", err)
		}
	}
	// An older row that must fall outside the range.
	if _, err := log.Insert(&ledger.Entry{TS: today.AddDate(0, 0, -10), Method: "GET", Path: "/old"}); err != nil {
		t.Fatalf("insert old: %v", err)
	}
	log.Close()

	outPath := filepath.Join(dir, "out.csv")
	date := today.Format("2006-01-02")
	if err := runExport([]string{"-db", dbPath, "-date", date, "-out", outPath}); err != nil {
		t.Fatalf("export: %v", err)
	}

	f, err := os.Open(outPath)
	if err != nil {
		t.Fatalf("open csv: %v", err)
	}
	defer f.Close()

	records, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if len(records) != 4 { // header + 3 rows
		t.Fatalf("csv has %d records, want 4 (header + 3)", len(records))
	}
	if records[0][0] != "id" || records[0][1] != "ts" {
		t.Errorf("unexpected header: %v", records[0])
	}
	if !strings.Contains(strings.Join(records[1], ","), "SessionContext") &&
		!strings.Contains(strings.Join(records[1], ","), `{"Data":{"x":1}}`) {
		t.Errorf("request body missing from CSV row: %v", records[1])
	}
}

func TestResolveDateRange(t *testing.T) {
	today := time.Now().Format("2006-01-02")

	cases := []struct {
		name             string
		date, from, to   string
		wantFrom, wantTo string
		wantErr          bool
	}{
		{name: "defaults to today", wantFrom: today, wantTo: today},
		{name: "single date", date: "2026-08-21", wantFrom: "2026-08-21", wantTo: "2026-08-21"},
		{name: "range", from: "2026-08-01", to: "2026-08-21", wantFrom: "2026-08-01", wantTo: "2026-08-21"},
		{name: "from only runs to today", from: "2026-08-01", wantFrom: "2026-08-01", wantTo: today},
		{name: "to only", to: "2026-08-21", wantFrom: "2026-08-21", wantTo: "2026-08-21"},
		{name: "date with range rejected", date: "2026-08-21", from: "2026-08-01", wantErr: true},
		{name: "bad format rejected", date: "21-08-2026", wantErr: true},
		{name: "reversed range rejected", from: "2026-08-21", to: "2026-08-01", wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotFrom, gotTo, err := resolveDateRange(tc.date, tc.from, tc.to)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %s..%s", gotFrom, gotTo)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if gotFrom != tc.wantFrom || gotTo != tc.wantTo {
				t.Errorf("got %s..%s, want %s..%s", gotFrom, gotTo, tc.wantFrom, tc.wantTo)
			}
		})
	}
}
