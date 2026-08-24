package proxy

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ZethicTech/fc-proxy/internal/config"
	"github.com/ZethicTech/fc-proxy/internal/encryption"
	"github.com/ZethicTech/fc-proxy/internal/ledger"
)

const testSecret = "secrets"

// newTestProxy wires a proxy against a fake Flexcube and a throwaway ledger.
func newTestProxy(t *testing.T, upstreamURL string) (*Proxy, *ledger.Ledger, *config.Config) {
	t.Helper()

	dbPath := filepath.Join(t.TempDir(), "ledger.db")
	log, err := ledger.Open(dbPath)
	if err != nil {
		t.Fatalf("open ledger: %v", err)
	}
	t.Cleanup(func() { log.Close() })

	cfg := &config.Config{
		BaseURL:        strings.TrimRight(upstreamURL, "/"),
		SharedSecret:   testSecret,
		AppEnv:         "development", // static token path
		Channel:        "sangam",
		UserIDPrefix:   "BANK",
		ChannelID:      "web",
		DBPath:         dbPath,
		Operator:       "tester",
		Timeout:        10 * time.Second,
		BankCode:       100,
		Branch:         1,
		SessionUserID:  "USER",
		SessionUserNo:  1,
		SessionChannel: "API",
		ServiceCode:    "S001",
	}

	return New(cfg, log), log, cfg
}

// TestRoundTrip walks a plain-JSON Postman request all the way to an encrypted
// upstream call and back to readable JSON.
func TestRoundTrip(t *testing.T) {
	var gotHeaders http.Header
	var gotPlain map[string]interface{}
	var gotQuery string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		gotQuery = r.URL.Query().Get("accountNo")

		body, _ := io.ReadAll(r.Body)
		decrypted, err := encryption.DecryptAES(string(body), testSecret)
		if err != nil {
			t.Errorf("upstream could not decrypt request: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := json.Unmarshal([]byte(decrypted), &gotPlain); err != nil {
			t.Errorf("upstream got non-JSON request: %v", err)
		}

		reply, _ := encryption.EncryptAES(`{"Data":{"Balance":"1500.00"}}`, testSecret)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reply) // ciphertext as a JSON string
	}))
	defer upstream.Close()

	proxy, log, _ := newTestProxy(t, upstream.URL)

	req := httptest.NewRequest(http.MethodPost, "/prov/v1/query/accountbalance?accountNo=123456", strings.NewReader(
		`{"ServiceCode":"S999","AccountNumber":"123456"}`))
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 back to Postman, got %d: %s", rec.Code, rec.Body.String())
	}

	// The tester sees plain JSON, not ciphertext.
	var visible map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &visible); err != nil {
		t.Fatalf("response to Postman was not readable JSON: %v (%s)", err, rec.Body.String())
	}
	data, ok := visible["Data"].(map[string]interface{})
	if !ok || data["Balance"] != "1500.00" {
		t.Fatalf("decrypted response did not survive: %#v", visible)
	}
	if rec.Header().Get("X-Proxy-Decrypted") != "true" {
		t.Errorf("expected X-Proxy-Decrypted=true, got %q", rec.Header().Get("X-Proxy-Decrypted"))
	}

	// Headers the tester never had to generate.
	for _, h := range []string{"x-session-id", "x-message-id", "Authorization"} {
		if gotHeaders.Get(h) == "" {
			t.Errorf("upstream did not receive %s", h)
		}
	}
	if got := gotHeaders.Get("x-user-id"); got != "BANK_SANGAM" {
		t.Errorf("x-user-id = %q, want BANK_SANGAM", got)
	}
	if got := gotHeaders.Get("x-channel-id"); got != "web" {
		t.Errorf("x-channel-id = %q, want web", got)
	}

	// Body was wrapped under Data with a SessionContext, and the payload's
	// ServiceCode won over the default.
	upstreamData, ok := gotPlain["Data"].(map[string]interface{})
	if !ok {
		t.Fatalf("request was not wrapped under Data: %#v", gotPlain)
	}
	if upstreamData["AccountNumber"] != "123456" {
		t.Errorf("payload field lost: %#v", upstreamData)
	}
	if _, present := upstreamData["ServiceCode"]; present {
		t.Errorf("ServiceCode directive should be lifted out of the payload: %#v", upstreamData)
	}
	sc, ok := upstreamData["SessionContext"].(map[string]interface{})
	if !ok {
		t.Fatalf("SessionContext was not injected: %#v", upstreamData)
	}
	if sc["ServiceCode"] != "S999" {
		t.Errorf("ServiceCode = %v, want S999 from the payload", sc["ServiceCode"])
	}
	if sc["BankCode"] != float64(100) || sc["TransactionBranch"] != float64(1) {
		t.Errorf("session defaults wrong: %#v", sc)
	}
	if ref, _ := sc["ExternalReferenceNo"].(string); len(ref) != 32 {
		t.Errorf("ExternalReferenceNo = %q, want a 32-char uuid", ref)
	}

	// The query param went up encrypted and decrypts back to what was typed.
	if gotQuery == "123456" {
		t.Error("query param reached the upstream in plaintext")
	}
	if plain, err := encryption.DecryptAES(gotQuery, testSecret); err != nil {
		t.Errorf("upstream query param did not decrypt: %v", err)
	} else if plain != "123456" {
		t.Errorf("query param decrypted to %q, want 123456", plain)
	}

	// The exchange landed in the ledger.
	rows, err := log.Recent(10)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("nothing was written to the ledger")
	}
	row := rows[0]
	if row["status_code"] != "200" {
		t.Errorf("ledger status_code = %q", row["status_code"])
	}
	if row["service_code"] != "S999" {
		t.Errorf("ledger service_code = %q, want S999", row["service_code"])
	}
	if !strings.Contains(row["request_plain"], "SessionContext") {
		t.Errorf("ledger did not keep the plaintext request: %q", row["request_plain"])
	}
	if !strings.Contains(row["response_plain"], "1500.00") {
		t.Errorf("ledger did not keep the decrypted response: %q", row["response_plain"])
	}
	if row["query_plain"] != "accountNo=123456" {
		t.Errorf("ledger query_plain = %q", row["query_plain"])
	}
	if row["query_encrypted"] == "" || strings.Contains(row["query_encrypted"], "123456") {
		t.Errorf("query should have been encrypted on the wire: %q", row["query_encrypted"])
	}
	if row["operator"] != "tester" {
		t.Errorf("ledger operator = %q", row["operator"])
	}
}

// TestHeaderOverrides checks that a Postman header beats the configured default.
func TestHeaderOverrides(t *testing.T) {
	var gotPlain map[string]interface{}
	var gotUserID string

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUserID = r.Header.Get("x-user-id")
		body, _ := io.ReadAll(r.Body)
		decrypted, _ := encryption.DecryptAES(string(body), testSecret)
		_ = json.Unmarshal([]byte(decrypted), &gotPlain)
		w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	proxy, _, _ := newTestProxy(t, upstream.URL)

	req := httptest.NewRequest(http.MethodPost, "/prov/v1/some/call", strings.NewReader(`{"Foo":"bar"}`))
	req.Header.Set("X-Proxy-Service-Code", "S123")
	req.Header.Set("X-Proxy-Bank-Code", "999")
	req.Header.Set("X-Proxy-Branch", "2000")
	req.Header.Set("X-Proxy-User-Id", "TESTER")
	req.Header.Set("X-Proxy-Channel", "mobile")
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	sc := gotPlain["Data"].(map[string]interface{})["SessionContext"].(map[string]interface{})
	if sc["ServiceCode"] != "S123" {
		t.Errorf("ServiceCode = %v, want S123 from the header", sc["ServiceCode"])
	}
	if sc["BankCode"] != float64(999) {
		t.Errorf("BankCode = %v, want 999", sc["BankCode"])
	}
	if sc["TransactionBranch"] != float64(2000) {
		t.Errorf("TransactionBranch = %v, want 2000", sc["TransactionBranch"])
	}
	if sc["UserId"] != "TESTER" {
		t.Errorf("UserId = %v, want TESTER", sc["UserId"])
	}
	if gotUserID != "BANK_MOBILE" {
		t.Errorf("x-user-id = %q, want BANK_MOBILE", gotUserID)
	}

	// The directive headers must not leak upstream.
	if strings.Contains(rec.Header().Get("X-Proxy-Target-Url"), "X-Proxy") {
		t.Error("proxy directives leaked into the target URL")
	}
}

// TestPlaintextQueryOptOut covers a GET with encryption turned off.
func TestPlaintextQueryOptOut(t *testing.T) {
	var gotQuery string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		w.Write([]byte(`{"ok":true}`))
	}))
	defer upstream.Close()

	proxy, _, _ := newTestProxy(t, upstream.URL)

	req := httptest.NewRequest(http.MethodGet, "/prov/v1/thing?id=abc", nil)
	req.Header.Set("X-Proxy-Encrypt-Query", "false")
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	if gotQuery != "id=abc" {
		t.Errorf("query = %q, want plaintext id=abc", gotQuery)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
}

// TestUpstreamDown records a connection failure in the ledger instead of losing it.
func TestUpstreamDown(t *testing.T) {
	proxy, log, _ := newTestProxy(t, "http://127.0.0.1:1") // nothing listening

	req := httptest.NewRequest(http.MethodPost, "/prov/v1/thing", strings.NewReader(`{"a":1}`))
	rec := httptest.NewRecorder()
	proxy.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rec.Code)
	}
	var body map[string]interface{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("error body not JSON: %v", err)
	}
	if body["proxy_error"] == nil {
		t.Errorf("expected proxy_error in the body: %#v", body)
	}

	rows, err := log.Recent(1)
	if err != nil {
		t.Fatalf("read ledger: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("failure was not recorded in the ledger")
	}
	if rows[0]["error"] == "" {
		t.Error("ledger row has no error recorded")
	}
}
