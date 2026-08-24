// Package proxy is the interception layer between Postman and Flexcube. A
// tester sends plain JSON; the proxy does the token, the unique ids, the
// session context and the AES in both directions, forwards to Flexcube, and
// writes the whole exchange to the local ledger.
package proxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ZethicTech/fc-proxy/internal/config"
	"github.com/ZethicTech/fc-proxy/internal/encryption"
	"github.com/ZethicTech/fc-proxy/internal/ledger"
	"github.com/google/uuid"
)

// Proxy is the interception layer. A tester points Postman at it with plain
// JSON; it does the token, the unique ids, the session context and the AES in
// both directions, forwards to Flexcube, and writes the whole exchange to the
// local ledger.
type Proxy struct {
	cfg    *config.Config
	client *http.Client
	tokens *tokenManager
	ledger *ledger.Ledger
}

// fallbackServiceCode is used only when neither the payload, a header nor the
// env file names one. It is a placeholder - real service codes live in
// fc-proxy.env.
const fallbackServiceCode = "S001"

// SessionContext is the block the upstream expects inside Data.
type SessionContext struct {
	BankCode            int    `json:"BankCode"`
	TransactionBranch   int    `json:"TransactionBranch"`
	ExternalReferenceNo string `json:"ExternalReferenceNo"`
	Channel             string `json:"Channel"`
	UserId              string `json:"UserId"`
	UserNo              int    `json:"UserNo"`
	ServiceCode         string `json:"ServiceCode"`
}

// New wires a proxy onto a config and an open ledger.
func New(cfg *config.Config, l *ledger.Ledger) *Proxy {
	client := cfg.HTTPClient()
	return &Proxy{
		cfg:    cfg,
		client: client,
		tokens: newTokenManager(cfg, client),
		ledger: l,
	}
}

// ServeHTTP forwards everything that is not a /_proxy/* control endpoint.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()

	entry := &ledger.Entry{
		TS:       start,
		Operator: p.cfg.Operator,
		Method:   r.Method,
		Path:     r.URL.Path,
	}

	rawBody, err := io.ReadAll(r.Body)
	if err != nil {
		p.fail(w, entry, start, http.StatusBadRequest, fmt.Errorf("read request body: %w", err))
		return
	}
	defer r.Body.Close()

	opts := p.resolveOptions(r)
	// Keep exactly what the tester typed, before anything is injected.
	entry.RequestOriginal = strings.TrimSpace(string(rawBody))

	// Build the outbound body: lift the payload's ServiceCode, inject the
	// SessionContext, then encrypt the whole thing.
	plainBody, err := p.buildBody(rawBody, opts)
	if err != nil {
		p.fail(w, entry, start, http.StatusBadRequest, err)
		return
	}
	entry.RequestPlain = plainBody
	entry.ServiceCode = opts.ServiceCode
	entry.Channel = opts.Channel
	entry.ExternalRefNo = opts.ExternalRefNo

	outBody := plainBody
	if plainBody != "" && opts.EncryptBody {
		outBody, err = encryption.EncryptAES(plainBody, p.cfg.SharedSecret)
		if err != nil {
			p.fail(w, entry, start, http.StatusInternalServerError, fmt.Errorf("encrypt request body: %w", err))
			return
		}
	}
	entry.RequestEncrypted = outBody

	// Build the outbound URL, encrypting each query value the way the upstream expects.
	targetURL := p.cfg.BaseURL + r.URL.Path
	entry.QueryPlain = r.URL.RawQuery
	if len(r.URL.Query()) > 0 {
		query := url.Values{}
		for key, values := range r.URL.Query() {
			for _, value := range values {
				finalValue := value
				if opts.EncryptQuery {
					encrypted, err := encryption.EncryptAES(value, p.cfg.SharedSecret)
					if err != nil {
						p.fail(w, entry, start, http.StatusInternalServerError, fmt.Errorf("encrypt query parameter %q: %w", key, err))
						return
					}
					finalValue = encrypted
				}
				query.Add(key, finalValue)
			}
		}
		entry.QueryEncrypted = query.Encode()
		targetURL += "?" + entry.QueryEncrypted
	}
	entry.TargetURL = targetURL

	// First attempt, then one retry on 401 with a fresh token.
	resp, respBody, headers, err := p.forward(r, targetURL, outBody, opts, entry)
	if err != nil {
		p.fail(w, entry, start, http.StatusBadGateway, err)
		return
	}
	if resp.StatusCode == http.StatusUnauthorized && !p.cfg.UsesStaticToken() {
		p.logf("upstream returned 401, regenerating token and retrying once")
		p.tokens.invalidate()
		entry.Retried = true
		resp, respBody, headers, err = p.forward(r, targetURL, outBody, opts, entry)
		if err != nil {
			p.fail(w, entry, start, http.StatusBadGateway, err)
			return
		}
	}
	entry.RequestHeaders = headers
	entry.StatusCode = resp.StatusCode
	entry.ResponseRaw = string(respBody)
	entry.ResponseHeaders = headerJSON(resp.Header)

	// Decrypt the reply so Postman shows readable JSON.
	readable, decrypted := p.decryptResponse(respBody, opts)
	entry.ResponsePlain = readable
	entry.DecryptOK = decrypted
	entry.DurationMS = time.Since(start).Milliseconds()

	id, dbErr := p.ledger.Insert(entry)
	if dbErr != nil {
		log.Printf("ledger write failed: %v", dbErr)
	}

	p.logf("%s %s -> %d in %dms (ledger #%d)", r.Method, r.URL.Path, resp.StatusCode, entry.DurationMS, id)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Proxy-Ledger-Id", strconv.FormatInt(id, 10))
	w.Header().Set("X-Proxy-Duration-Ms", strconv.FormatInt(entry.DurationMS, 10))
	w.Header().Set("X-Proxy-Session-Id", entry.SessionID)
	w.Header().Set("X-Proxy-Message-Id", entry.MessageID)
	w.Header().Set("X-Proxy-External-Ref", entry.ExternalRefNo)
	w.Header().Set("X-Proxy-Service-Code", entry.ServiceCode)
	w.Header().Set("X-Proxy-Decrypted", strconv.FormatBool(decrypted))
	w.Header().Set("X-Proxy-Target-Url", targetURL)
	if entry.Retried {
		w.Header().Set("X-Proxy-Retried", "true")
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = w.Write([]byte(readable))
}

// forward performs one attempt against Flexcube with freshly generated headers.
func (p *Proxy) forward(r *http.Request, targetURL, body string, opts *callOptions, entry *ledger.Entry) (*http.Response, []byte, string, error) {
	var bodyReader io.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	}

	req, err := http.NewRequestWithContext(r.Context(), r.Method, targetURL, bodyReader)
	if err != nil {
		return nil, nil, "", fmt.Errorf("build upstream request: %w", err)
	}

	token, err := p.tokens.get(r.Context())
	if err != nil {
		return nil, nil, "", err
	}

	// Every call gets its own session and message id, so the tester never has
	// to generate one by hand.
	sessionID := newTraceID()
	messageID := uuid.New().String()
	entry.SessionID = sessionID
	entry.MessageID = messageID

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "fc-proxy/1.0")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("x-user-id", p.cfg.UserIDPrefix+"_"+strings.ToUpper(opts.Channel))
	req.Header.Set("x-session-id", sessionID)
	req.Header.Set("x-message-id", messageID)
	req.Header.Set("x-channel-id", p.cfg.ChannelID)

	// Pass through anything the tester set that is not a proxy directive.
	for key, values := range r.Header {
		if isProxyHeader(key) || isHopHeader(key) {
			continue
		}
		for _, v := range values {
			req.Header.Set(key, v)
		}
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, nil, headerJSON(req.Header), fmt.Errorf("call %s: %w", targetURL, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, headerJSON(req.Header), fmt.Errorf("read upstream response: %w", err)
	}

	return resp, respBody, headerJSON(req.Header), nil
}

// buildBody lifts a top-level ServiceCode out of the payload, wraps the rest
// under "Data" and injects the SessionContext.
func (p *Proxy) buildBody(raw []byte, opts *callOptions) (string, error) {
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return "", nil
	}

	var bodyMap map[string]interface{}
	if err := json.Unmarshal([]byte(trimmed), &bodyMap); err != nil {
		// Not a JSON object - forward it untouched rather than guessing.
		return trimmed, nil
	}

	// Service code comes from the payload when the tester supplies it.
	if v, ok := bodyMap["ServiceCode"]; ok {
		if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
			opts.ServiceCode = strings.TrimSpace(s)
		}
		// It is a proxy directive, not a Flexcube field.
		delete(bodyMap, "ServiceCode")
	}

	if !opts.InjectSession {
		out, err := json.Marshal(bodyMap)
		return string(out), err
	}

	data, exists := bodyMap["Data"].(map[string]interface{})
	if !exists || data == nil {
		data = make(map[string]interface{})
		for k, v := range bodyMap {
			data[k] = v
		}
		bodyMap = map[string]interface{}{"Data": data}
	}

	// A SessionContext the tester wrote by hand always wins.
	if existing, ok := data["SessionContext"].(map[string]interface{}); ok {
		if s, ok := existing["ServiceCode"].(string); ok && s != "" {
			opts.ServiceCode = s
		}
		if s, ok := existing["ExternalReferenceNo"].(string); ok && s != "" {
			opts.ExternalRefNo = s
		}
	} else if _, ok := data["SessionContext"]; !ok {
		if opts.ExternalRefNo == "" {
			opts.ExternalRefNo = newTraceID()
		}
		if strings.TrimSpace(opts.ServiceCode) == "" {
			opts.ServiceCode = fallbackServiceCode
		}
		data["SessionContext"] = SessionContext{
			BankCode:            opts.BankCode,
			TransactionBranch:   opts.Branch,
			ExternalReferenceNo: opts.ExternalRefNo,
			Channel:             opts.SessionChannel,
			UserId:              opts.SessionUserID,
			UserNo:              opts.SessionUserNo,
			ServiceCode:         opts.ServiceCode,
		}
	}

	out, err := json.Marshal(bodyMap)
	if err != nil {
		return "", fmt.Errorf("marshal request body: %w", err)
	}
	return string(out), nil
}

// decryptResponse turns whatever came back into something readable, and
// reports whether decryption actually happened.
func (p *Proxy) decryptResponse(respBody []byte, opts *callOptions) (string, bool) {
	raw := strings.TrimSpace(string(respBody))
	if raw == "" || !opts.DecryptReply {
		return raw, false
	}

	// The upstream returns the ciphertext as a JSON string.
	candidate := raw
	var asString string
	if err := json.Unmarshal(respBody, &asString); err == nil {
		candidate = asString
	}

	if plain, err := encryption.DecryptAES(candidate, p.cfg.SharedSecret); err == nil {
		return prettyJSON(plain), true
	}

	// Error responses come back as plain JSON with the FC reason inside.
	return prettyJSON(raw), false
}

func (p *Proxy) fail(w http.ResponseWriter, entry *ledger.Entry, start time.Time, status int, err error) {
	entry.Error = err.Error()
	entry.StatusCode = status
	entry.DurationMS = time.Since(start).Milliseconds()

	id, dbErr := p.ledger.Insert(entry)
	if dbErr != nil {
		log.Printf("ledger write failed: %v", dbErr)
	}
	log.Printf("%s %s -> proxy error: %v (ledger #%d)", entry.Method, entry.Path, err, id)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Proxy-Ledger-Id", strconv.FormatInt(id, 10))
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"proxy_error": err.Error(),
		"ledger_id":   id,
		"path":        entry.Path,
	})
}

func (p *Proxy) logf(format string, args ...interface{}) {
	if p.cfg.Verbose {
		log.Printf(format, args...)
	}
}

func headerJSON(h http.Header) string {
	redacted := make(map[string]string, len(h))
	for k, v := range h {
		if strings.EqualFold(k, "Authorization") {
			redacted[k] = "Bearer <redacted>"
			continue
		}
		redacted[k] = strings.Join(v, ", ")
	}
	out, err := json.Marshal(redacted)
	if err != nil {
		return "{}"
	}
	return string(out)
}

func prettyJSON(s string) string {
	var buf bytes.Buffer
	if err := json.Indent(&buf, []byte(s), "", "  "); err != nil {
		return s
	}
	return buf.String()
}

func newTraceID() string {
	return strings.ReplaceAll(uuid.New().String(), "-", "")
}
