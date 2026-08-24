// Package ledger is the local SQLite log of intercepted exchanges: one file,
// WAL mode, no server. Every request the proxy handles lands here, and the
// rows can be exported to CSV.
package ledger

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Entry is one intercepted exchange: what the tester sent, what actually went
// on the wire, and what came back.
type Entry struct {
	ID       int64
	TS       time.Time
	Operator string

	Method    string
	Path      string
	TargetURL string

	QueryPlain     string
	QueryEncrypted string

	RequestOriginal  string
	RequestPlain     string
	RequestEncrypted string
	RequestHeaders   string

	StatusCode      int
	ResponseRaw     string
	ResponsePlain   string
	ResponseHeaders string
	DecryptOK       bool

	DurationMS int64
	Retried    bool

	SessionID     string
	MessageID     string
	ExternalRefNo string
	ServiceCode   string
	Channel       string

	Error string
}

// Ledger is the local SQLite log. One file, WAL mode, no server.
type Ledger struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS exchanges (
	id                INTEGER PRIMARY KEY AUTOINCREMENT,
	ts                TEXT NOT NULL,
	ts_date           TEXT NOT NULL,
	operator          TEXT,
	method            TEXT,
	path              TEXT,
	target_url        TEXT,
	query_plain       TEXT,
	query_encrypted   TEXT,
	request_original  TEXT,
	request_plain     TEXT,
	request_encrypted TEXT,
	request_headers   TEXT,
	status_code       INTEGER,
	response_raw      TEXT,
	response_plain    TEXT,
	response_headers  TEXT,
	decrypt_ok        INTEGER,
	duration_ms       INTEGER,
	retried           INTEGER,
	session_id        TEXT,
	message_id        TEXT,
	external_ref_no   TEXT,
	service_code      TEXT,
	channel           TEXT,
	error             TEXT
);
CREATE INDEX IF NOT EXISTS idx_exchanges_ts_date ON exchanges(ts_date);
CREATE INDEX IF NOT EXISTS idx_exchanges_ts ON exchanges(ts);
`

// Open opens (creating it if needed) the ledger file at path.
func Open(path string) (*Ledger, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open ledger %s: %w", path, err)
	}
	// modernc/sqlite handles concurrency better with a single writer.
	db.SetMaxOpenConns(1)
	if err := db.Ping(); err != nil {
		return nil, fmt.Errorf("ping ledger %s: %w", path, err)
	}
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("create ledger schema: %w", err)
	}
	return &Ledger{db: db}, nil
}

func (l *Ledger) Close() error { return l.db.Close() }

// Insert writes one exchange and returns its ledger id.
func (l *Ledger) Insert(e *Entry) (int64, error) {
	res, err := l.db.Exec(`
		INSERT INTO exchanges (
			ts, ts_date, operator, method, path, target_url,
			query_plain, query_encrypted,
			request_original, request_plain, request_encrypted, request_headers,
			status_code, response_raw, response_plain, response_headers, decrypt_ok,
			duration_ms, retried,
			session_id, message_id, external_ref_no, service_code, channel, error
		) VALUES (?,?,?,?,?,?, ?,?, ?,?,?,?, ?,?,?,?,?, ?,?, ?,?,?,?,?,?)`,
		e.TS.Format(time.RFC3339), e.TS.Format("2006-01-02"), e.Operator, e.Method, e.Path, e.TargetURL,
		e.QueryPlain, e.QueryEncrypted,
		e.RequestOriginal, e.RequestPlain, e.RequestEncrypted, e.RequestHeaders,
		e.StatusCode, e.ResponseRaw, e.ResponsePlain, e.ResponseHeaders, boolToInt(e.DecryptOK),
		e.DurationMS, boolToInt(e.Retried),
		e.SessionID, e.MessageID, e.ExternalRefNo, e.ServiceCode, e.Channel, e.Error,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// Count reports how many rows an export of [from, to] would cover.
func (l *Ledger) Count(from, to string) (int, error) {
	var n int
	err := l.db.QueryRow("SELECT COUNT(*) FROM exchanges WHERE ts_date BETWEEN ? AND ?", from, to).Scan(&n)
	return n, err
}

// Recent returns the newest n exchanges, newest first, as column/value maps.
func (l *Ledger) Recent(n int) ([]map[string]string, error) {
	rows, err := l.db.Query("SELECT "+strings.Join(Columns, ", ")+" FROM exchanges ORDER BY id DESC LIMIT ?", n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []map[string]string
	for rows.Next() {
		values, err := scanRowAsStrings(rows, len(Columns))
		if err != nil {
			return nil, err
		}
		row := make(map[string]string, len(Columns))
		for i, col := range Columns {
			row[col] = values[i]
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

// selectRange returns every exchange whose local date falls in [from, to],
// both inclusive and formatted YYYY-MM-DD.
func (l *Ledger) selectRange(from, to string) (*sql.Rows, error) {
	query := "SELECT " + strings.Join(Columns, ", ") + " FROM exchanges WHERE ts_date BETWEEN ? AND ? ORDER BY id ASC"
	return l.db.Query(query, from, to)
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
