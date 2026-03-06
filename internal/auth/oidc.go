package auth

import (
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/triangles/polyon-core/internal/config"
)

// jwksCache caches the JWKS keys to avoid repeated fetches.
// The outer map key is the realm name; inner map key is the kid.
var (
	jwksMu      sync.RWMutex
	jwksRealmCache map[string]map[string]*rsa.PublicKey // realm -> kid -> key
	jwksRealmExpiry map[string]time.Time                // realm -> expiry
	jwksCacheTTL = 10 * time.Minute

	// Legacy single-realm cache aliases (kept for backward compat)
	jwksCache  map[string]*rsa.PublicKey
	jwksExpiry time.Time
)

// tlsClient is an HTTP client that skips TLS verification (for self-signed certs).
var tlsClient = &http.Client{
	Timeout: 5 * time.Second,
	Transport: &http.Transport{
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec
	},
}

// verifyToken verifies a Keycloak JWT token locally using JWKS public keys.
// Falls back to userinfo endpoint if JWKS verification fails.
// Uses the "admin" realm (Console auth).
func verifyToken(cfg *config.Config, token string) (string, error) {
	// First, try local JWT verification via JWKS
	username, err := verifyJWTLocalWithRealm(cfg, token, "admin")
	if err == nil {
		return username, nil
	}

	// Fallback: userinfo endpoint (handles edge cases)
	return verifyTokenUserinfo(cfg, token)
}

// verifyJWTLocal verifies the JWT signature for the "admin" realm.
// Kept for backward compatibility; delegates to verifyJWTLocalWithRealm.
func verifyJWTLocal(cfg *config.Config, token string) (string, error) {
	return verifyJWTLocalWithRealm(cfg, token, "admin")
}

// verifyJWTLocalWithRealm verifies the JWT signature using cached JWKS public keys
// for the specified realm.
func verifyJWTLocalWithRealm(cfg *config.Config, token, realm string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("not a valid JWT")
	}

	// Parse header to get kid and alg
	headerJSON, err := base64URLDecode(parts[0])
	if err != nil {
		return "", fmt.Errorf("header decode: %w", err)
	}
	var header struct {
		Kid string `json:"kid"`
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return "", fmt.Errorf("header parse: %w", err)
	}
	if header.Alg != "RS256" {
		return "", fmt.Errorf("unsupported alg: %s", header.Alg)
	}

	// Get public key for this realm
	pubKey, err := getPublicKeyForRealm(cfg, realm, header.Kid)
	if err != nil {
		return "", fmt.Errorf("get public key: %w", err)
	}

	// Verify signature
	sigInput := []byte(parts[0] + "." + parts[1])
	digest := sha256.Sum256(sigInput)

	sig, err := base64URLDecode(parts[2])
	if err != nil {
		return "", fmt.Errorf("sig decode: %w", err)
	}

	if err := rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, digest[:], sig); err != nil {
		return "", fmt.Errorf("signature invalid: %w", err)
	}

	// Parse claims
	payloadJSON, err := base64URLDecode(parts[1])
	if err != nil {
		return "", fmt.Errorf("payload decode: %w", err)
	}
	var claims struct {
		Sub               string  `json:"sub"`
		PreferredUsername string  `json:"preferred_username"`
		Exp               float64 `json:"exp"`
	}
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return "", fmt.Errorf("claims parse: %w", err)
	}

	// Check expiry
	if time.Now().Unix() > int64(claims.Exp) {
		return "", fmt.Errorf("token expired")
	}

	if claims.PreferredUsername != "" {
		return claims.PreferredUsername, nil
	}
	return claims.Sub, nil
}

// getPublicKey returns the RSA public key for the given kid from the "admin" realm JWKS.
// Kept for backward compatibility.
func getPublicKey(cfg *config.Config, kid string) (*rsa.PublicKey, error) {
	return getPublicKeyForRealm(cfg, "admin", kid)
}

// getPublicKeyForRealm returns the RSA public key for the given kid and realm.
func getPublicKeyForRealm(cfg *config.Config, realm, kid string) (*rsa.PublicKey, error) {
	jwksMu.RLock()
	if jwksRealmCache != nil && jwksRealmExpiry != nil {
		if exp, ok := jwksRealmExpiry[realm]; ok && time.Now().Before(exp) {
			if realmKeys, ok := jwksRealmCache[realm]; ok {
				if key, ok := realmKeys[kid]; ok {
					jwksMu.RUnlock()
					return key, nil
				}
			}
		}
	}
	jwksMu.RUnlock()

	// Refresh cache for this realm
	return refreshJWKSForRealm(cfg, realm, kid)
}

// refreshJWKS fetches the JWKS for the "admin" realm and updates the cache.
// Kept for backward compatibility.
func refreshJWKS(cfg *config.Config, kid string) (*rsa.PublicKey, error) {
	return refreshJWKSForRealm(cfg, "admin", kid)
}

// refreshJWKSForRealm fetches the JWKS for a specific realm and updates the per-realm cache.
func refreshJWKSForRealm(cfg *config.Config, realm, kid string) (*rsa.PublicKey, error) {
	jwksMu.Lock()
	defer jwksMu.Unlock()

	// Build JWKS URL for the requested realm
	jwksURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/certs", cfg.KeycloakURL, realm)

	resp, err := tlsClient.Get(jwksURL)
	if err != nil {
		return nil, fmt.Errorf("JWKS fetch failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("JWKS fetch status %d", resp.StatusCode)
	}

	var jwks struct {
		Keys []struct {
			Kid string `json:"kid"`
			Kty string `json:"kty"`
			Alg string `json:"alg"`
			Use string `json:"use"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return nil, fmt.Errorf("JWKS decode: %w", err)
	}

	newCache := make(map[string]*rsa.PublicKey)
	for _, k := range jwks.Keys {
		if k.Alg != "RS256" || k.Use != "sig" {
			continue
		}
		nBytes, err := base64URLDecode(k.N)
		if err != nil {
			continue
		}
		eBytes, err := base64URLDecode(k.E)
		if err != nil {
			continue
		}
		n := new(big.Int).SetBytes(nBytes)
		eInt := 0
		for _, b := range eBytes {
			eInt = eInt<<8 | int(b)
		}
		pub := &rsa.PublicKey{N: n, E: eInt}
		newCache[k.Kid] = pub
	}

	// Update per-realm caches
	if jwksRealmCache == nil {
		jwksRealmCache = make(map[string]map[string]*rsa.PublicKey)
	}
	if jwksRealmExpiry == nil {
		jwksRealmExpiry = make(map[string]time.Time)
	}
	jwksRealmCache[realm] = newCache
	jwksRealmExpiry[realm] = time.Now().Add(jwksCacheTTL)

	// Keep legacy admin realm aliases in sync
	if realm == "admin" {
		jwksCache = newCache
		jwksExpiry = time.Now().Add(jwksCacheTTL)
	}

	if key, ok := newCache[kid]; ok {
		return key, nil
	}
	return nil, fmt.Errorf("kid %q not found in JWKS for realm %q", kid, realm)
}

// verifyTokenUserinfo is the fallback using the Keycloak userinfo endpoint.
func verifyTokenUserinfo(cfg *config.Config, token string) (string, error) {
	// Use internal Keycloak URL directly (avoids Traefik proxy loop)
	url := fmt.Sprintf("%s/realms/admin/protocol/openid-connect/userinfo", cfg.KeycloakURL)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := tlsClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("keycloak unreachable: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("token invalid (status %d)", resp.StatusCode)
	}

	var userInfo struct {
		PreferredUsername string `json:"preferred_username"`
		Sub               string `json:"sub"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&userInfo); err != nil {
		return "", err
	}

	if userInfo.PreferredUsername != "" {
		return userInfo.PreferredUsername, nil
	}
	return userInfo.Sub, nil
}

// base64URLDecode decodes a base64url-encoded string (no padding).
func base64URLDecode(s string) ([]byte, error) {
	// Add padding
	switch len(s) % 4 {
	case 2:
		s += "=="
	case 3:
		s += "="
	}
	return base64.URLEncoding.DecodeString(s)
}

// jwtIssuer extracts the 'iss' claim from a JWT without full verification.
func jwtIssuer(token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", fmt.Errorf("not a JWT")
	}
	payload, err := base64URLDecode(parts[1])
	if err != nil {
		return "", fmt.Errorf("base64 decode: %w", err)
	}
	var claims struct {
		Iss string `json:"iss"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", fmt.Errorf("json unmarshal: %w", err)
	}
	if claims.Iss == "" {
		return "", fmt.Errorf("iss claim empty")
	}
	return claims.Iss, nil
}
