package chatgpt

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func tokenJWT(t *testing.T, key *rsa.PrivateKey, claims map[string]any) string {
	t.Helper()
	h, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "test"})
	b, _ := json.Marshal(claims)
	raw := base64.RawURLEncoding.EncodeToString(h) + "." + base64.RawURLEncoding.EncodeToString(b)
	hash := sha256.Sum256([]byte(raw))
	sig, e := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	require.NoError(t, e)
	return raw + "." + base64.RawURLEncoding.EncodeToString(sig)
}
func testAuth(t *testing.T) (Client, *rsa.PrivateKey, *atomic.Int32) {
	t.Helper()
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, e)
	count := &atomic.Int32{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/jwks" {
			json.NewEncoder(w).Encode(map[string]any{"keys": []any{map[string]string{"kty": "RSA", "kid": "test", "alg": "RS256", "n": base64.RawURLEncoding.EncodeToString(key.N.Bytes()), "e": base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes())}}})
			return
		}
		require.NoError(t, r.ParseForm())
		count.Add(1)
		require.Equal(t, BaseURL, r.Form.Get("resource"))
		require.NotEqual(t, dynamicClient, r.Form.Get("client_id"))
		response := tokenResponse{AccessToken: "new-access", RefreshToken: "new-refresh", TokenType: "Bearer", Scope: planScope, ExpiresIn: 3600}
		if r.Form.Get("grant_type") == "authorization_code" {
			// The synthetic code carries the nonce and challenge so exchange can verify both.
			parts := strings.Split(r.Form.Get("code"), ".")
			require.Len(t, parts, 2)
			hash := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
			require.Equal(t, parts[1], base64.RawURLEncoding.EncodeToString(hash[:]))
			require.Contains(t, r.Form.Get("redirect_uri"), "http://127.0.0.1:")
			response.IDToken = tokenJWT(t, key, map[string]any{"iss": issuer, "sub": "account-1", "aud": r.Form.Get("client_id"), "nonce": parts[0], "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix(), "email": "test@example.com"})
		} else {
			require.Equal(t, "refresh_token", r.Form.Get("grant_type"))
			require.Equal(t, "old-refresh", r.Form.Get("refresh_token"))
		}
		json.NewEncoder(w).Encode(response)
	}))
	t.Cleanup(server.Close)
	return Client{Store: Store{Dir: t.TempDir()}, jwksURL: server.URL + "/jwks", tokenURL: server.URL + "/token"}, key, count
}
func completeLogin(t *testing.T, u string, wrongState bool) error {
	t.Helper()
	a, e := url.Parse(u)
	require.NoError(t, e)
	q := a.Query()
	require.Equal(t, "S256", q.Get("code_challenge_method"))
	require.Equal(t, BaseURL, q.Get("resource"))
	require.NotEmpty(t, q.Get("ext_agent_host_id"))
	cb, _ := url.Parse(q.Get("redirect_uri"))
	values := url.Values{"state": {q.Get("state")}, "client_id": {"oaiapp_test"}, "code": {q.Get("nonce") + "." + q.Get("code_challenge")}}
	if wrongState {
		values.Set("state", "wrong")
		cb.RawQuery = values.Encode()
		resp, e := http.Get(cb.String())
		require.NoError(t, e)
		resp.Body.Close()
		require.Equal(t, 400, resp.StatusCode)
		values.Set("state", q.Get("state"))
	}
	cb.RawQuery = values.Encode()
	resp, e := http.Get(cb.String())
	if e != nil {
		return e
	}
	defer resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
	return nil
}
func TestLoginAndReauthorization(t *testing.T) {
	c, _, calls := testAuth(t)
	ctx := context.Background()
	require.NoError(t, c.Login(ctx, "", func(u string) error {
		parsed, _ := url.Parse(u)
		require.Equal(t, dynamicClient, parsed.Query().Get("client_id"))
		require.Equal(t, "mods", parsed.Query().Get("agent_name_hint"))
		return completeLogin(t, u, true)
	}))
	st, e := c.Store.Load()
	require.NoError(t, e)
	a, ok := st.Selected()
	require.True(t, ok)
	require.Equal(t, "account-1", a.Subject)
	require.True(t, c.Store.SignedIn())
	host := st.HostID
	require.NoError(t, c.Login(ctx, a.ClientID, func(u string) error {
		parsed, _ := url.Parse(u)
		require.Equal(t, a.ClientID, parsed.Query().Get("client_id"))
		require.Empty(t, parsed.Query().Get("agent_name_hint"))
		return completeLogin(t, u, false)
	}))
	st, e = c.Store.Load()
	require.NoError(t, e)
	require.Equal(t, host, st.HostID)
	require.Len(t, st.Accounts, 1)
	require.EqualValues(t, 2, calls.Load())
	if runtime.GOOS != "windows" {
		info, e := os.Stat(filepath.Join(c.Store.Dir, "accounts.json"))
		require.NoError(t, e)
		require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	}
}
func TestLoginDeniedAndCanceledPreservesCredentials(t *testing.T) {
	c, _, count := testAuth(t)
	ctx := context.Background()
	require.NoError(t, c.Login(ctx, "", func(u string) error { return completeLogin(t, u, false) }))
	before, _ := c.Store.Load()
	err := c.Login(ctx, "oaiapp_test", func(u string) error {
		a, _ := url.Parse(u)
		q := a.Query()
		resp, e := http.Get(q.Get("redirect_uri") + "?" + url.Values{"state": {q.Get("state")}, "error": {"access_denied"}}.Encode())
		if e == nil {
			resp.Body.Close()
		}
		return e
	})
	require.ErrorContains(t, err, "declined")
	after, _ := c.Store.Load()
	require.Equal(t, before, after)
	require.EqualValues(t, 1, count.Load())
	ctx, cancel := context.WithCancel(ctx)
	cancel()
	require.ErrorIs(t, c.Login(ctx, "", func(string) error { return nil }), context.Canceled)
}
func TestRefreshSerializedAndIdentityRetained(t *testing.T) {
	c, _, calls := testAuth(t)
	ctx := context.Background()
	require.NoError(t, c.Store.update(ctx, func(st *State) error {
		st.Active = "client"
		st.Accounts = []Account{{ClientID: "client", Subject: "original", AccessToken: "old", RefreshToken: "old-refresh", Scope: planScope, ExpiresAt: time.Now().Add(-time.Minute)}}
		return nil
	}))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			v, e := c.AccessToken(ctx)
			require.NoError(t, e)
			require.Equal(t, "new-access", v)
		}()
	}
	wg.Wait()
	require.EqualValues(t, 1, calls.Load())
	st, e := c.Store.Load()
	require.NoError(t, e)
	a, _ := st.Selected()
	require.Equal(t, "new-refresh", a.RefreshToken)
	require.Equal(t, "original", a.Subject)
}
func TestIdentityValidation(t *testing.T) {
	c, key, _ := testAuth(t)
	base := func() map[string]any {
		return map[string]any{"iss": issuer, "sub": "subject", "aud": "client", "nonce": "nonce", "exp": time.Now().Add(time.Hour).Unix(), "iat": time.Now().Unix()}
	}
	for _, field := range []string{"iss", "sub", "aud", "nonce", "exp", "iat"} {
		t.Run(field, func(t *testing.T) {
			claims := base()
			delete(claims, field)
			_, e := c.verifyIdentity(context.Background(), tokenJWT(t, key, claims), "client", "nonce")
			require.Error(t, e)
		})
	}
	_, e := c.verifyIdentity(context.Background(), tokenJWT(t, key, base()), "client", "nonce")
	require.NoError(t, e)
	bad := tokenJWT(t, key, base())
	parts := strings.Split(bad, ".")
	parts[1] = base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"attacker"}`))
	_, e = c.verifyIdentity(context.Background(), strings.Join(parts, "."), "client", "nonce")
	require.Error(t, e)
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func TestTransportOriginAndModelDiscovery(t *testing.T) {
	c, _, _ := testAuth(t)
	ctx := context.Background()
	require.NoError(t, c.Login(ctx, "", func(u string) error { return completeLogin(t, u, false) }))
	c.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		require.Equal(t, "Bearer new-access", r.Header.Get("Authorization"))
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"models":[{"slug":"visible","visibility":"list"},{"slug":"hidden","visibility":"hide"}]}`))}, nil
	})}
	models, e := c.Models(ctx)
	require.NoError(t, e)
	require.Equal(t, []string{"visible"}, models)
	for _, u := range []string{"https://evil.example/v1/responses", "http://api.openai.com/v1/responses", "https://api.openai.com.evil/v1/responses"} {
		_, e := c.HTTPClient().Get(u)
		require.ErrorContains(t, e, "only be sent")
	}
}
func TestLogoutClearsTokensAndKeepsRegistration(t *testing.T) {
	c, _, _ := testAuth(t)
	ctx := context.Background()
	require.NoError(t, c.Login(ctx, "", func(u string) error { return completeLogin(t, u, false) }))
	before, _ := c.Store.Load()
	c.HTTP = &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body := `{"revocation_endpoint":"https://auth.openai.com/revoke"}`
		if r.Method == "POST" {
			require.NoError(t, r.ParseForm())
			require.Equal(t, "new-refresh", r.Form.Get("token"))
			body = ""
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})}
	require.NoError(t, c.Logout(ctx))
	after, e := c.Store.Load()
	require.NoError(t, e)
	require.Equal(t, before.HostID, after.HostID)
	require.Equal(t, before.Active, after.Active)
	a, _ := after.Selected()
	require.Empty(t, a.AccessToken)
	require.Empty(t, a.IDToken)
	require.Empty(t, a.RefreshToken)
	require.Equal(t, "account-1", a.Subject)
	_, e = c.AccessToken(ctx)
	require.ErrorContains(t, e, "not signed in")
}
func TestRefreshFailureDoesNotLeakSecrets(t *testing.T) {
	c, _, _ := testAuth(t)
	ctx := context.Background()
	require.NoError(t, c.Store.update(ctx, func(st *State) error {
		st.Active = "client"
		st.Accounts = []Account{{ClientID: "client", AccessToken: "secret-access", RefreshToken: "secret-refresh", Scope: planScope}}
		return nil
	}))
	c.HTTP = &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 401, Body: io.NopCloser(strings.NewReader("secret-refresh"))}, nil
	})}
	_, err := c.AccessToken(ctx)
	require.ErrorContains(t, err, "sign in again")
	require.NotContains(t, fmt.Sprint(err), "secret-refresh")
}
