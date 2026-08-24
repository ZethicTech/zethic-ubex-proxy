package ledger

import (
	"database/sql"
	"encoding/csv"
	"fmt"
	"io"
	"strings"
)

// Columns is the column order used by both the CSV export and the Recent
// listing, so the CSV and the JSON always line up.
var Columns = []string{
	"id", "ts", "ts_date", "operator", "method", "path", "target_url",
	"query_plain", "query_encrypted",
	"request_original", "request_plain", "request_encrypted", "request_headers",
	"status_code", "response_raw", "response_plain", "response_headers", "decrypt_ok",
	"duration_ms", "retried",
	"session_id", "message_id", "external_ref_no", "service_code", "channel", "error",
}

// WriteCSV streams every exchange in [from, to] to w, header row first, and
// reports how many data rows were written.
func (l *Ledger) WriteCSV(w io.Writer, from, to string) (int, error) {
	rows, err := l.selectRange(from, to)
	if err != nil {
		return 0, fmt.Errorf("query ledger: %w", err)
	}
	defer rows.Close()

	out := csv.NewWriter(w)
	if err := out.Write(Columns); err != nil {
		return 0, fmt.Errorf("write CSV header: %w", err)
	}

	written := 0
	for rows.Next() {
		values, err := scanRowAsStrings(rows, len(Columns))
		if err != nil {
			return written, fmt.Errorf("scan row: %w", err)
		}
		if err := out.Write(values); err != nil {
			return written, fmt.Errorf("write CSV row: %w", err)
		}
		written++
	}
	if err := rows.Err(); err != nil {
		return written, fmt.Errorf("read rows: %w", err)
	}

	out.Flush()
	if err := out.Error(); err != nil {
		return written, fmt.Errorf("flush CSV: %w", err)
	}
	return written, nil
}

// scanRowAsStrings reads a row into strings, turning NULLs into empty cells and
// stripping the control characters Excel chokes on.
func scanRowAsStrings(rows *sql.Rows, n int) ([]string, error) {
	raw := make([]sql.NullString, n)
	targets := make([]interface{}, n)
	for i := range raw {
		targets[i] = &raw[i]
	}
	if err := rows.Scan(targets...); err != nil {
		return nil, err
	}

	values := make([]string, n)
	for i, v := range raw {
		if v.Valid {
			values[i] = sanitizeCell(v.String)
		}
	}
	return values, nil
}

func sanitizeCell(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\r' {
			return -1
		}
		return r
	}, s)
}
