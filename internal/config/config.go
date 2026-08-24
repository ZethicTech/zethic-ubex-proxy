// Package config resolves everything the proxy needs to talk to Flexcube from
// the environment: an optional .env file beside the binary, real environment
// variables on top of that, and command-line flags on top of those.
package config

import (
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/joho/godotenv"
)

// Config holds everything the proxy needs to talk to Flexcube.
//
// Values are read from the environment (a .env file next to the binary is
// loaded automatically), and every one of them can be overridden by a flag.
type Config struct {
	Port         string
	BaseURL      string
	SharedSecret string
	ClientID     string
	ClientSecret string
	AppEnv       string
	Channel      string

	// UserIDPrefix and ChannelID are the deployment-specific values that go out as
	// the x-user-id / x-channel-id headers. The defaults here are placeholders
	// on purpose - set the real ones in fc-proxy.env, which is never committed.
	UserIDPrefix string
	ChannelID    string

	DBPath   string
	Operator string

	Timeout     time.Duration
	InsecureTLS bool
	Verbose     bool

	// Session context defaults. Placeholders again - the real bank code, branch
	// and test user belong in fc-proxy.env.
	BankCode       int
	Branch         int
	SessionUserID  string
	SessionUserNo  int
	SessionChannel string
	ServiceCode    string
}

// LoadEnvFiles loads the first .env-style file it finds beside the binary or in
// the working directory. Missing files are fine - real env vars still win.
func LoadEnvFiles(explicit string) {
	if explicit != "" {
		if err := godotenv.Load(explicit); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not load %s: %v\n", explicit, err)
		}
		return
	}
	for _, name := range []string{"fc-proxy.env", ".env"} {
		if _, err := os.Stat(name); err == nil {
			_ = godotenv.Load(name)
			return
		}
	}
}

// Load builds a Config from the current environment.
func Load() (*Config, error) {
	cfg := &Config{
		Port:    getEnv("FC_PROXY_PORT", "9090"),
		BaseURL: strings.TrimRight(getEnv("CHANNEL_SANGAM_BASE_URL", "http://localhost:8051"), "/"),

		ClientID:     getEnv("SANGAM_CLIENT_ID", "sangam_client_id"),
		ClientSecret: getEnv("SANGAM_CLIENT_SECRET", "sangam_client_secret"),
		AppEnv:       getEnv("APP_ENV", "development"),
		Channel:      getEnv("FC_PROXY_CHANNEL", "sangam"),
		UserIDPrefix: getEnv("FC_PROXY_USER_ID_PREFIX", "BANK"),
		ChannelID:    getEnv("FC_PROXY_CHANNEL_ID", "web"),

		DBPath:   getEnv("FC_PROXY_DB", "fc-proxy-ledger.db"),
		Operator: getEnv("FC_PROXY_OPERATOR", defaultOperator()),

		Timeout:     time.Duration(getEnvAsInt("FC_PROXY_TIMEOUT_SECONDS", 60)) * time.Second,
		InsecureTLS: getEnvAsBool("FC_PROXY_INSECURE_TLS", false),
		Verbose:     getEnvAsBool("FC_PROXY_VERBOSE", true),

		BankCode:       getEnvAsInt("FC_PROXY_BANK_CODE", 100),
		Branch:         getEnvAsInt("FC_PROXY_BRANCH", 1),
		SessionUserID:  getEnv("FC_PROXY_SESSION_USER_ID", "USER"),
		SessionUserNo:  getEnvAsInt("FC_PROXY_SESSION_USER_NO", 1),
		SessionChannel: getEnv("FC_PROXY_SESSION_CHANNEL", "API"),
		ServiceCode:    getEnv("FC_PROXY_SERVICE_CODE", "S001"),
	}

	// API_SECRET is stored hex-encoded; FC_PROXY_SECRET_RAW lets a
	// tester paste the literal secret instead of hex.
	if raw := os.Getenv("FC_PROXY_SECRET_RAW"); raw != "" {
		cfg.SharedSecret = raw
	} else {
		hexSecret := getEnv("API_SECRET", "73656372657473")
		secretBytes, err := hex.DecodeString(hexSecret)
		if err != nil {
			return nil, fmt.Errorf("API_SECRET is not valid hex (%v) - set FC_PROXY_SECRET_RAW instead to pass the secret literally", err)
		}
		cfg.SharedSecret = string(secretBytes)
	}

	return cfg, nil
}

// HTTPClient builds the outbound client used for both Flexcube calls and the
// OAuth token request.
func (c *Config) HTTPClient() *http.Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if c.InsecureTLS {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &http.Client{Timeout: c.Timeout, Transport: transport}
}

// UsesStaticToken reports whether to skip OAuth: outside production/uat the
// token endpoint is never called.
func (c *Config) UsesStaticToken() bool {
	return c.AppEnv != "production" && c.AppEnv != "uat"
}

func defaultOperator() string {
	if u := os.Getenv("USERNAME"); u != "" { // Windows
		return u
	}
	if u := os.Getenv("USER"); u != "" { // unix
		return u
	}
	return "unknown"
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvAsInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if parsed, err := strconv.Atoi(v); err == nil {
			return parsed
		}
	}
	return fallback
}

func getEnvAsBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			return parsed
		}
	}
	return fallback
}
