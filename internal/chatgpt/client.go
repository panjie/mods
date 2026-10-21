package chatgpt

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const BaseURL = "https://api.openai.com/v1"
const issuer = "https://auth.openai.com"
const planScope = "chatgpt.tokens.use.direct"
const dynamicClient = "dynamic_agent_client"

type Client struct {
	Store Store
	HTTP  *http.Client
	// Endpoints are unexported; production credentials always use OpenAI origins.
	authURL, tokenURL, jwksURL, discoveryURL string
}

func NewClient() Client { return Client{Store: DefaultStore()} }
func (c Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}
func endpoint(v, fallback string) string {
	if v != "" {
		return v
	}
	return fallback
}
func (c Client) json(ctx context.Context, method, target string, form url.Values, out any) error {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return err
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.http().Do(req)
	if err != nil {
		return errors.New("ChatGPT authentication network request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("ChatGPT authentication failed (HTTP %d); sign in again if the session expired or was revoked", resp.StatusCode)
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(out); err != nil {
		return errors.New("invalid ChatGPT authentication response")
	}
	return nil
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	IDToken      string `json:"id_token"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
	ExpiresIn    int64  `json:"expires_in"`
}

func hasScope(scope string) bool {
	for _, s := range strings.Fields(scope) {
		if s == planScope {
			return true
		}
	}
	return false
}
func (c Client) token(ctx context.Context, form url.Values) (tokenResponse, error) {
	var t tokenResponse
	err := c.json(ctx, "POST", endpoint(c.tokenURL, issuer+"/api/accounts/oauth/token"), form, &t)
	if err != nil {
		return t, err
	}
	if t.AccessToken == "" || !strings.EqualFold(t.TokenType, "bearer") || t.ExpiresIn <= 0 || t.ExpiresIn > 86400*30 {
		return t, errors.New("invalid ChatGPT token response")
	}
	return t, nil
}
func randomValue() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// Login starts a loopback callback before presenting the authorization URL.
// clientID is empty for a new registration, otherwise an existing account ID.
func (c Client) Login(ctx context.Context, clientID string, present func(string) error) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var host string
	var previous Account
	err := c.Store.update(ctx, func(st *State) error {
		host = st.HostID
		if clientID != "" {
			for _, a := range st.Accounts {
				if a.ClientID == clientID {
					previous = a
					return nil
				}
			}
			return errors.New("unknown ChatGPT account registration")
		}
		return nil
	})
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		return err
	}
	defer listener.Close()
	redirect := "http://" + listener.Addr().String() + "/auth/callback"
	state, nonce, verifier := randomValue(), randomValue(), randomValue()
	challenge := sha256.Sum256([]byte(verifier))
	id := clientID
	if id == "" {
		id = dynamicClient
	}
	q := url.Values{"client_id": {id}, "ext_agent_host_id": {host}, "response_type": {"code"}, "redirect_uri": {redirect}, "scope": {"openid profile email offline_access resource.invoke " + planScope}, "resource": {BaseURL}, "state": {state}, "nonce": {nonce}, "code_challenge_method": {"S256"}, "code_challenge": {base64.RawURLEncoding.EncodeToString(challenge[:])}}
	if clientID == "" {
		q.Set("agent_name_hint", "mods")
	} else if previous.Email != "" {
		q.Set("login_hint", previous.Email)
	}
	// Do not put retained ID tokens in the URL: URLs may be displayed in the terminal.
	type callback struct {
		code, id string
		err      error
	}
	result := make(chan callback, 1)
	var once sync.Once
	server := &http.Server{ReadHeaderTimeout: 5 * time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.URL.Path != "/auth/callback" || r.Method != "GET" {
			http.NotFound(w, r)
			return
		}
		values := r.URL.Query()
		if values.Get("state") != state {
			http.Error(w, "Invalid authorization state", 400)
			return
		}
		cb := callback{code: values.Get("code"), id: values.Get("client_id")}
		if values.Get("iss") != "" && values.Get("iss") != issuer {
			cb.err = errors.New("ChatGPT callback issuer mismatch")
		}
		if values.Get("error") != "" {
			cb.err = errors.New("ChatGPT authorization was declined; no credentials changed")
		}
		if cb.err == nil {
			if clientID != "" {
				if cb.id != "" && cb.id != clientID {
					cb.err = errors.New("ChatGPT callback client mismatch")
				}
				cb.id = clientID
			}
			if cb.code == "" || cb.id == "" || cb.id == dynamicClient {
				cb.err = errors.New("incomplete ChatGPT registration callback")
			}
		}
		accepted := false
		once.Do(func() { accepted = true; result <- cb })
		if !accepted {
			http.Error(w, "Authorization already received", 409)
			return
		}
		_, _ = io.WriteString(w, "Authorization received. Return to mods to see the result.")
	})}
	go func() { _ = server.Serve(listener) }()
	defer server.Close()
	if err = present(endpoint(c.authURL, issuer+"/api/accounts/authorize") + "?" + q.Encode()); err != nil {
		return err
	}
	var cb callback
	select {
	case <-ctx.Done():
		return ctx.Err()
	case cb = <-result:
	}
	if cb.err != nil {
		return cb.err
	}
	t, err := c.token(ctx, url.Values{"grant_type": {"authorization_code"}, "client_id": {cb.id}, "code": {cb.code}, "code_verifier": {verifier}, "redirect_uri": {redirect}, "resource": {BaseURL}})
	if err != nil {
		return err
	}
	identity, err := c.verifyIdentity(ctx, t.IDToken, cb.id, nonce)
	if err != nil {
		return err
	}
	if previous.Subject != "" && previous.Subject != identity.Subject {
		return errors.New("ChatGPT account identity changed; credentials were not replaced")
	}
	if !hasScope(t.Scope) {
		return errors.New("ChatGPT plan usage permission was not granted; authorize plan usage and try again")
	}
	if t.RefreshToken == "" {
		return errors.New("ChatGPT did not grant a renewable session")
	}
	a := Account{ClientID: cb.id, Subject: identity.Subject, Email: identity.Email, AccessToken: t.AccessToken, RefreshToken: t.RefreshToken, IDToken: t.IDToken, Scope: t.Scope, ExpiresAt: time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)}
	return c.Store.update(ctx, func(st *State) error {
		for i, old := range st.Accounts {
			if old.ClientID == a.ClientID {
				if old.Subject != a.Subject {
					return errors.New("ChatGPT account identity mismatch")
				}
				st.Accounts[i] = a
				st.Active = a.ClientID
				return nil
			}
		}
		st.Accounts = append(st.Accounts, a)
		st.Active = a.ClientID
		return nil
	})
}
func (c Client) AccessToken(ctx context.Context) (string, error) {
	var token string
	err := c.Store.update(ctx, func(st *State) error {
		for i := range st.Accounts {
			a := &st.Accounts[i]
			if a.ClientID != st.Active {
				continue
			}
			if a.AccessToken == "" || a.RefreshToken == "" {
				break
			}
			if !hasScope(a.Scope) {
				return errors.New("ChatGPT plan usage permission is missing")
			}
			if time.Until(a.ExpiresAt) > time.Minute {
				token = a.AccessToken
				return nil
			}
			t, err := c.token(ctx, url.Values{"grant_type": {"refresh_token"}, "client_id": {a.ClientID}, "refresh_token": {a.RefreshToken}, "resource": {BaseURL}})
			if err != nil {
				return err
			}
			if t.Scope != "" && !hasScope(t.Scope) {
				return errors.New("ChatGPT plan usage permission was revoked; sign in again")
			}
			a.AccessToken = t.AccessToken
			if t.RefreshToken != "" {
				a.RefreshToken = t.RefreshToken
			}
			if t.Scope != "" {
				a.Scope = t.Scope
			}
			// The validated ID token is retained only as identity metadata, never used for authorization.
			a.ExpiresAt = time.Now().Add(time.Duration(t.ExpiresIn) * time.Second)
			token = a.AccessToken
			return nil
		}
		return errors.New("ChatGPT is not signed in; run mods --config or mods --chatgpt-auth login")
	})
	return token, err
}

// HTTPClient refreshes before every inference/tool round and confines credentials to OpenAI.
func (c Client) HTTPClient() *http.Client {
	return &http.Client{Transport: authTransport{client: c}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

type authTransport struct{ client Client }

func (t authTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if req.URL.Scheme != "https" || req.URL.Host != "api.openai.com" || req.URL.User != nil {
		return nil, errors.New("ChatGPT credentials can only be sent to https://api.openai.com")
	}
	token, err := t.client.AccessToken(req.Context())
	if err != nil {
		return nil, err
	}
	r := req.Clone(req.Context())
	r.Header.Set("Authorization", "Bearer "+token)
	transport := t.client.http().Transport
	if transport == nil {
		transport = http.DefaultTransport
	}
	return transport.RoundTrip(r)
}
func (c Client) Models(ctx context.Context) ([]string, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", BaseURL+"/models", nil)
	resp, err := c.HTTPClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("ChatGPT model discovery failed (HTTP %d)", resp.StatusCode)
	}
	var body struct {
		Models []struct {
			Slug       string `json:"slug"`
			Visibility string `json:"visibility"`
		} `json:"models"`
	}
	if err = json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&body); err != nil {
		return nil, err
	}
	var names []string
	for _, m := range body.Models {
		if m.Visibility == "list" && m.Slug != "" {
			names = append(names, m.Slug)
		}
	}
	return names, nil
}
func (c Client) Logout(ctx context.Context) error {
	var remoteErr error
	err := c.Store.update(ctx, func(st *State) error {
		for i := range st.Accounts {
			a := &st.Accounts[i]
			if a.ClientID != st.Active {
				continue
			}
			if a.RefreshToken != "" {
				var discovery struct {
					RevocationEndpoint string `json:"revocation_endpoint"`
				}
				remoteErr = c.json(ctx, "GET", endpoint(c.discoveryURL, issuer+"/.well-known/openid-configuration"), nil, &discovery)
				if remoteErr == nil {
					u, e := url.Parse(discovery.RevocationEndpoint)
					if e != nil || u.Scheme != "https" || u.Host != "auth.openai.com" || u.User != nil {
						remoteErr = errors.New("invalid OpenAI revocation endpoint")
					} else {
						for attempt := 0; attempt < 3; attempt++ {
							remoteErr = c.json(ctx, "POST", u.String(), url.Values{"token": {a.RefreshToken}, "token_type_hint": {"refresh_token"}, "client_id": {a.ClientID}}, nil)
							if remoteErr == nil {
								break
							}
							select {
							case <-ctx.Done():
							case <-time.After(time.Duration(attempt+1) * 200 * time.Millisecond):
							}
						}
					}
				}
			}
			a.AccessToken = ""
			a.RefreshToken = ""
			a.IDToken = ""
			a.ExpiresAt = time.Time{}
		}
		return nil
	})
	if err != nil {
		return err
	}
	if remoteErr != nil {
		return errors.New("signed out locally; remote revocation was not confirmed. Disconnect mods in ChatGPT Settings")
	}
	return nil
}
