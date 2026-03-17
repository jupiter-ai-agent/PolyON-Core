package proxy

import (
	"io"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/config"
)

// RegisterJMAP registers JMAP proxy routes.
// Bearer JWT는 그대로 Stalwart에 전달 — Stalwart가 Keycloak으로 검증.
// admin Basic Auth는 절대 사용하지 않음 (관리 작업 전용).
func RegisterJMAP(r chi.Router, cfg *config.Config) {
	// JMAP Session, API, Upload, Download, EventSource 모두 커버
	r.HandleFunc("/*", func(w http.ResponseWriter, r *http.Request) {
		path := chi.URLParam(r, "*")
		targetURL := cfg.StalwartURL + "/" + path

		var body io.Reader
		if r.Body != nil {
			body = r.Body
		}

		proxyReq, err := http.NewRequest(r.Method, targetURL, body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		// Query string 복사
		proxyReq.URL.RawQuery = r.URL.RawQuery

		// Bearer JWT 투명 전달 (핵심 원칙)
		// Stalwart가 Keycloak token introspection으로 직접 검증
		if auth := r.Header.Get("Authorization"); auth != "" {
			proxyReq.Header.Set("Authorization", auth)
		}

		// Content-Type 복사
		if ct := r.Header.Get("Content-Type"); ct != "" {
			proxyReq.Header.Set("Content-Type", ct)
		}

		// Accept 복사
		if acc := r.Header.Get("Accept"); acc != "" {
			proxyReq.Header.Set("Accept", acc)
		}

		// EventSource(SSE) 지원
		if strings.Contains(r.Header.Get("Accept"), "text/event-stream") {
			proxyReq.Header.Set("Accept", "text/event-stream")
			proxyReq.Header.Set("Cache-Control", "no-cache")
		}

		client := &http.Client{}
		resp, err := client.Do(proxyReq)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		// 응답 헤더 복사
		for k, vv := range resp.Header {
			for _, v := range vv {
				w.Header().Add(k, v)
			}
		}
		w.WriteHeader(resp.StatusCode)
		io.Copy(w, resp.Body) //nolint:errcheck
	})
}
