package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ZethicTech/fc-proxy/internal/config"
)

// tokenManager caches the bearer token until it is about to expire, and
// regenerates on demand after a 401.
type tokenManager struct {
	cfg    *config.Config
	client *http.Client

	mu     sync.Mutex
	token  string
	expiry time.Time
}

func newTokenManager(cfg *config.Config, client *http.Client) *tokenManager {
	return &tokenManager{cfg: cfg, client: client}
}

func (t *tokenManager) get(ctx context.Context) (string, error) {
	// Non-production environments use the static token.
	if t.cfg.UsesStaticToken() {
		return "static-dev-token", nil
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.token != "" && time.Now().Before(t.expiry) {
		return t.token, nil
	}

	authURL := t.cfg.BaseURL + "/oauth/oauth20/generateaccesstoken"

	data := url.Values{}
	data.Set("client_id", t.cfg.ClientID)
	data.Set("client_secret", t.cfg.ClientSecret)
	data.Set("grant_type", "client_credentials")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, authURL, strings.NewReader(data.Encode()))
	if err != nil {
		return "", fmt.Errorf("build auth token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("x-trace-id", newTraceID())

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("call auth service at %s: %w", authURL, err)
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("auth service returned %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var tokenResp struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		return "", fmt.Errorf("decode auth token response: %w", err)
	}
	if tokenResp.AccessToken == "" {
		return "", fmt.Errorf("auth service returned an empty access_token")
	}

	t.token = tokenResp.AccessToken
	// One minute of headroom before the upstream would start rejecting it.
	t.expiry = time.Now().Add(time.Duration(tokenResp.ExpiresIn-60) * time.Second)

	return t.token, nil
}

func (t *tokenManager) invalidate() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.token = ""
}
