package prc

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/rs/zerolog/log"
)

// AuthProvider provisions Keycloak OIDC clients for modules.
// PP 제1원칙: 모든 서비스는 Keycloak SSO를 통해 인증한다.
type AuthProvider struct {
	AdminURL      string // e.g., "http://polyon-auth:8080"
	Realm         string // e.g., "polyon"
	AdminUser     string // Keycloak admin username
	AdminPassword string // Keycloak admin password
	BaseDomain    string // e.g., "cmars.com"
	AuthDomain    string // e.g., "sso.cmars.com" (Keycloak 외부 도메인)
}

func (p *AuthProvider) Type() string        { return "auth" }
func (p *AuthProvider) DependsOn() []string { return nil }

func (p *AuthProvider) Provision(ctx context.Context, claim Claim) (Credentials, error) {
	clientID := claim.ConfigString("clientId", claim.ModuleID)
	accessType := claim.ConfigString("accessType", "confidential") // 서버앱이 대다수

	// 1. Admin 토큰 획득
	token, err := p.getAdminToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("Keycloak admin 토큰 획득 실패: %w", err)
	}

	// 2. 기존 클라이언트 확인
	existing, _ := p.getClient(ctx, token, clientID)
	if existing != "" {
		log.Info().Str("clientId", clientID).Msg("PRC: Keycloak client already exists, reusing")
	} else {
		// 3. 클라이언트 생성
		if err := p.createClient(ctx, token, clientID, accessType, claim); err != nil {
			return nil, fmt.Errorf("Keycloak 클라이언트 생성 실패: %w", err)
		}
		log.Info().Str("clientId", clientID).Str("accessType", accessType).Msg("PRC: Keycloak OIDC client provisioned")
	}

	// 4. Credential 반환 — 내부/외부 URL 분리
	// authEndpoint: 브라우저 리다이렉트 → 외부 URL
	// tokenEndpoint, jwksUri: 서버 백채널 → 내부 URL (Pod→KC)
	authHost := p.AuthDomain
	if authHost == "" {
		authHost = "sso." + p.BaseDomain
	}
	externalIssuer := fmt.Sprintf("https://%s/realms/%s", authHost, p.Realm)
	externalOIDC := externalIssuer + "/protocol/openid-connect"
	internalOIDC := fmt.Sprintf("%s/realms/%s/protocol/openid-connect", p.AdminURL, p.Realm)

	creds := Credentials{
		"issuer":                 externalIssuer,
		"clientId":               clientID,
		"authEndpoint":           externalOIDC + "/auth",           // 외부 (브라우저용)
		"tokenEndpoint":          internalOIDC + "/token",          // 내부 (서버용)
		"tokenEndpointExternal":  externalOIDC + "/token",          // 외부 (참고용)
		"jwksUri":                internalOIDC + "/certs",           // 내부 (서버용)
		"jwksUriExternal":        externalOIDC + "/certs",           // 외부 (참고용)
	}

	// confidential 타입이면 client secret 가져오기
	if accessType == "confidential" {
		secret, err := p.getClientSecret(ctx, token, clientID)
		if err == nil && secret != "" {
			creds["clientSecret"] = secret
		}
	}

	return creds, nil
}

func (p *AuthProvider) Deprovision(ctx context.Context, claim Claim) error {
	clientID := claim.ConfigString("clientId", claim.ModuleID)

	token, err := p.getAdminToken(ctx)
	if err != nil {
		return fmt.Errorf("Keycloak admin 토큰 획득 실패: %w", err)
	}

	internalID, err := p.getClient(ctx, token, clientID)
	if err != nil || internalID == "" {
		return nil // 이미 없음
	}

	// DELETE /admin/realms/{realm}/clients/{id}
	reqURL := fmt.Sprintf("%s/admin/realms/%s/clients/%s", p.AdminURL, p.Realm, internalID)
	req, _ := http.NewRequestWithContext(ctx, "DELETE", reqURL, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	log.Info().Str("clientId", clientID).Msg("PRC: Keycloak OIDC client deprovisioned")
	return nil
}

func (p *AuthProvider) Status(ctx context.Context, claim Claim) (ResourceStatus, error) {
	clientID := claim.ConfigString("clientId", claim.ModuleID)
	token, err := p.getAdminToken(ctx)
	if err != nil {
		return StatusError, err
	}
	id, err := p.getClient(ctx, token, clientID)
	if err != nil || id == "" {
		return StatusNotFound, nil
	}
	return StatusProvisioned, nil
}

// ── internal helpers ──

func (p *AuthProvider) getAdminToken(ctx context.Context) (string, error) {
	tokenURL := fmt.Sprintf("%s/realms/master/protocol/openid-connect/token", p.AdminURL)
	data := url.Values{
		"grant_type": {"password"},
		"client_id":  {"admin-cli"},
		"username":   {p.AdminUser},
		"password":   {p.AdminPassword},
	}

	req, _ := http.NewRequestWithContext(ctx, "POST", tokenURL, strings.NewReader(data.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("token request failed: %d %s", resp.StatusCode, string(body))
	}

	var result struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}
	return result.AccessToken, nil
}

func (p *AuthProvider) getClient(ctx context.Context, token, clientID string) (string, error) {
	reqURL := fmt.Sprintf("%s/admin/realms/%s/clients?clientId=%s", p.AdminURL, p.Realm, clientID)
	req, _ := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var clients []struct {
		ID       string `json:"id"`
		ClientID string `json:"clientId"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&clients); err != nil {
		return "", err
	}
	for _, c := range clients {
		if c.ClientID == clientID {
			return c.ID, nil
		}
	}
	return "", nil
}

func (p *AuthProvider) createClient(ctx context.Context, token, clientID, accessType string, claim Claim) error {
	// redirectUris 추출
	redirectUris := []string{}
	if rawList, ok := claim.Config["redirectUris"]; ok {
		if list, ok := rawList.([]any); ok {
			for _, item := range list {
				if s, ok := item.(string); ok {
					// 템플릿 치환 (baseDomain)
					s = strings.ReplaceAll(s, "{{ baseDomain }}", p.BaseDomain)
					redirectUris = append(redirectUris, s)
				}
			}
		}
	}
	if len(redirectUris) == 0 {
		redirectUris = []string{fmt.Sprintf("https://%s.%s/*", clientID, p.BaseDomain)}
	}

	// webOrigins
	webOrigins := []string{
		fmt.Sprintf("https://console.%s", p.BaseDomain),
		fmt.Sprintf("https://portal.%s", p.BaseDomain),
	}

	clientBody := map[string]any{
		"clientId":                  clientID,
		"name":                     clientID,
		"enabled":                  true,
		"protocol":                 "openid-connect",
		"publicClient":             accessType == "public",
		"directAccessGrantsEnabled": true,
		"standardFlowEnabled":      true,
		"redirectUris":             redirectUris,
		"webOrigins":               webOrigins,
	}

	// PKCE: public → S256 강제, confidential → 해제
	if accessType == "public" {
		clientBody["attributes"] = map[string]string{
			"pkce.code.challenge.method": "S256",
		}
	} else {
		clientBody["attributes"] = map[string]string{
			"pkce.code.challenge.method": "", // 해제 — 서버가 client_secret으로 인증
		}
	}

	bodyJSON, _ := json.Marshal(clientBody)
	reqURL := fmt.Sprintf("%s/admin/realms/%s/clients", p.AdminURL, p.Realm)
	req, _ := http.NewRequestWithContext(ctx, "POST", reqURL, bytes.NewReader(bodyJSON))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpClient().Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 201 && resp.StatusCode != 409 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("create client failed: %d %s", resp.StatusCode, string(body))
	}
	return nil
}

func (p *AuthProvider) getClientSecret(ctx context.Context, token, clientID string) (string, error) {
	internalID, err := p.getClient(ctx, token, clientID)
	if err != nil || internalID == "" {
		return "", err
	}

	reqURL := fmt.Sprintf("%s/admin/realms/%s/clients/%s/client-secret", p.AdminURL, p.Realm, internalID)
	req, _ := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := httpClient().Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var result struct {
		Value string `json:"value"`
	}
	json.NewDecoder(resp.Body).Decode(&result)
	return result.Value, nil
}

func httpClient() *http.Client {
	return &http.Client{Timeout: 15 * time.Second}
}
