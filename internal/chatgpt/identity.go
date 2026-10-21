package chatgpt

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"strings"
	"time"
)

type identity struct {
	Subject string `json:"sub"`
	Email   string `json:"email"`
}

// verifyIdentity accepts only RS256/ES256 keys from the fixed issuer's JWKS.
// Keys are fetched once per login, so key rotation requires no persistent cache.
func (c Client) verifyIdentity(ctx context.Context, token, audience, nonce string) (identity, error) {
	invalid := errors.New("ChatGPT ID token validation failed")
	var id identity
	parts := strings.Split(token, ".")
	if len(parts) != 3 || len(token) > 65536 {
		return id, invalid
	}
	var header struct {
		Alg  string   `json:"alg"`
		Kid  string   `json:"kid"`
		Crit []string `json:"crit"`
	}
	h, e := base64.RawURLEncoding.DecodeString(parts[0])
	if e != nil || json.Unmarshal(h, &header) != nil || header.Kid == "" || len(header.Crit) != 0 || (header.Alg != "RS256" && header.Alg != "ES256") {
		return id, invalid
	}
	sig, e := base64.RawURLEncoding.DecodeString(parts[2])
	if e != nil {
		return id, invalid
	}
	var jwks struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			Alg string `json:"alg"`
			Use string `json:"use"`
			N   string `json:"n"`
			E   string `json:"e"`
			Crv string `json:"crv"`
			X   string `json:"x"`
			Y   string `json:"y"`
		} `json:"keys"`
	}
	if e = c.json(ctx, "GET", endpoint(c.jwksURL, issuer+"/.well-known/jwks.json"), nil, &jwks); e != nil {
		return id, e
	}
	hash := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	verified := false
	decode := func(s string) []byte { b, _ := base64.RawURLEncoding.DecodeString(s); return b }
	for _, k := range jwks.Keys {
		if k.Kid != header.Kid || (k.Alg != "" && k.Alg != header.Alg) || (k.Use != "" && k.Use != "sig") {
			continue
		}
		if header.Alg == "RS256" && k.Kty == "RSA" {
			n := new(big.Int).SetBytes(decode(k.N))
			eb := decode(k.E)
			if n.BitLen() < 2048 || len(eb) == 0 || len(eb) > 4 {
				continue
			}
			exp := new(big.Int).SetBytes(eb).Int64()
			if exp < 3 || exp > 2147483647 {
				continue
			}
			verified = rsa.VerifyPKCS1v15(&rsa.PublicKey{N: n, E: int(exp)}, crypto.SHA256, hash[:], sig) == nil
		}
		if header.Alg == "ES256" && k.Kty == "EC" && k.Crv == "P-256" && len(sig) == 64 {
			x, y := new(big.Int).SetBytes(decode(k.X)), new(big.Int).SetBytes(decode(k.Y))
			curve := elliptic.P256()
			if curve.IsOnCurve(x, y) {
				verified = ecdsa.Verify(&ecdsa.PublicKey{Curve: curve, X: x, Y: y}, hash[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:]))
			}
		}
		if verified {
			break
		}
	}
	if !verified {
		return id, invalid
	}
	payload, e := base64.RawURLEncoding.DecodeString(parts[1])
	if e != nil {
		return id, invalid
	}
	var claims struct {
		identity
		Issuer          string          `json:"iss"`
		Audience        json.RawMessage `json:"aud"`
		Nonce           string          `json:"nonce"`
		Expiry          int64           `json:"exp"`
		Issued          int64           `json:"iat"`
		NotBefore       int64           `json:"nbf"`
		AuthorizedParty string          `json:"azp"`
	}
	if json.Unmarshal(payload, &claims) != nil {
		return id, invalid
	}
	now := time.Now().Unix()
	if claims.Issuer != issuer || claims.Subject == "" || claims.Nonce != nonce || nonce == "" || claims.Expiry <= now-5 || claims.Issued <= 0 || claims.Issued > now+5 || claims.NotBefore > now+5 {
		return id, invalid
	}
	var aud string
	var audiences []string
	if json.Unmarshal(claims.Audience, &aud) == nil {
		audiences = []string{aud}
	} else if json.Unmarshal(claims.Audience, &audiences) != nil {
		return id, invalid
	}
	match := false
	for _, a := range audiences {
		if a == audience {
			match = true
		}
	}
	if !match || (len(audiences) > 1 && claims.AuthorizedParty != audience) || (claims.AuthorizedParty != "" && claims.AuthorizedParty != audience) {
		return id, invalid
	}
	return claims.identity, nil
}
