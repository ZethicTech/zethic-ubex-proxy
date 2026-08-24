package proxy

import (
	"net/http"
	"strconv"
	"strings"
)

// callOptions are the per-request knobs a tester can turn, resolved from the
// payload first and then from X-Proxy-* headers.
type callOptions struct {
	ServiceCode    string
	Channel        string
	BankCode       int
	Branch         int
	SessionUserID  string
	SessionUserNo  int
	SessionChannel string

	EncryptBody   bool
	EncryptQuery  bool
	DecryptReply  bool
	InjectSession bool
	ExternalRefNo string
}

func (p *Proxy) resolveOptions(r *http.Request) *callOptions {
	o := &callOptions{
		ServiceCode:    p.cfg.ServiceCode,
		Channel:        p.cfg.Channel,
		BankCode:       p.cfg.BankCode,
		Branch:         p.cfg.Branch,
		SessionUserID:  p.cfg.SessionUserID,
		SessionUserNo:  p.cfg.SessionUserNo,
		SessionChannel: p.cfg.SessionChannel,
		EncryptBody:    true,
		EncryptQuery:   true,
		DecryptReply:   true,
		InjectSession:  true,
	}

	if v := r.Header.Get("X-Proxy-Service-Code"); v != "" {
		o.ServiceCode = v
	}
	if v := r.Header.Get("X-Proxy-Channel"); v != "" {
		o.Channel = v
	}
	if v := r.Header.Get("X-Proxy-Bank-Code"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			o.BankCode = n
		}
	}
	if v := r.Header.Get("X-Proxy-Branch"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			o.Branch = n
		}
	}
	if v := r.Header.Get("X-Proxy-User-Id"); v != "" {
		o.SessionUserID = v
	}
	if v := r.Header.Get("X-Proxy-User-No"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			o.SessionUserNo = n
		}
	}
	if v := r.Header.Get("X-Proxy-Session-Channel"); v != "" {
		o.SessionChannel = v
	}
	if v := r.Header.Get("X-Proxy-External-Ref"); v != "" {
		o.ExternalRefNo = v
	}

	o.EncryptBody = headerBool(r, "X-Proxy-Encrypt-Body", o.EncryptBody)
	o.EncryptQuery = headerBool(r, "X-Proxy-Encrypt-Query", o.EncryptQuery)
	o.DecryptReply = headerBool(r, "X-Proxy-Decrypt-Response", o.DecryptReply)
	o.InjectSession = headerBool(r, "X-Proxy-Session-Context", o.InjectSession)

	return o
}

func headerBool(r *http.Request, name string, fallback bool) bool {
	if v := r.Header.Get(name); v != "" {
		if parsed, err := strconv.ParseBool(v); err == nil {
			return parsed
		}
	}
	return fallback
}

// isProxyHeader reports whether a header is a directive for the proxy itself
// and must not be forwarded upstream.
func isProxyHeader(key string) bool {
	return strings.HasPrefix(strings.ToLower(key), "x-proxy-")
}

func isHopHeader(key string) bool {
	switch strings.ToLower(key) {
	case "connection", "content-length", "host", "authorization",
		"transfer-encoding", "upgrade", "postman-token", "accept-encoding":
		return true
	}
	return false
}
