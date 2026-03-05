package api

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/rs/zerolog/log"
	"github.com/triangles/polyon-core/internal/httputil"
)

// RegisterStrapi registers Strapi-related API routes.
func RegisterStrapi(r chi.Router, d *Deps) {
	r.Get("/strapi/token", getStrapiToken(d))
}

type strapiLoginReq struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

type strapiLoginResp struct {
	Data struct {
		Token string `json:"token"`
		User  struct {
			ID        int    `json:"id"`
			Email     string `json:"email"`
			Firstname string `json:"firstname"`
			Lastname  string `json:"lastname"`
		} `json:"user"`
	} `json:"data"`
	Error *struct {
		Status  int    `json:"status"`
		Name    string `json:"name"`
		Message string `json:"message"`
	} `json:"error"`
}

// getStrapiToken fetches a Strapi admin JWT token using configured credentials.
// GET /api/v1/strapi/token
func getStrapiToken(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		email := os.Getenv("STRAPI_ADMIN_EMAIL")
		if email == "" {
			email = "admin@polyon.local"
		}
		password := os.Getenv("STRAPI_ADMIN_PASSWORD")
		if password == "" {
			httputil.RespondError(w, http.StatusServiceUnavailable,
				"STRAPI_ADMIN_PASSWORD not configured",
				"strapi admin password not set")
			return
		}

		strapiURL := d.Cfg.StrapiURL

		loginURL := strapiURL + "/admin/login"

		body, err := json.Marshal(strapiLoginReq{Email: email, Password: password})
		if err != nil {
			log.Error().Err(err).Msg("strapi login marshal")
			httputil.RespondError(w, http.StatusInternalServerError, "marshal error", "marshal error")
			return
		}

		client := &http.Client{Timeout: 10 * time.Second}
		req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, loginURL, bytes.NewReader(body))
		if err != nil {
			log.Error().Err(err).Msg("strapi login request create")
			httputil.RespondError(w, http.StatusInternalServerError, "request error", "request error")
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Host = "localhost" // Strapi v5 host-based security filter (must use req.Host, not Header.Set)

		resp, err := client.Do(req)
		if err != nil {
			log.Error().Err(err).Str("url", loginURL).Msg("strapi login request failed")
			httputil.RespondError(w, http.StatusBadGateway,
				fmt.Sprintf("strapi unreachable: %v", err),
				"strapi unreachable")
			return
		}
		defer resp.Body.Close()

		var loginResp strapiLoginResp
		if err := json.NewDecoder(resp.Body).Decode(&loginResp); err != nil {
			log.Error().Err(err).Msg("strapi login response decode")
			httputil.RespondError(w, http.StatusBadGateway, "strapi response decode error", "strapi response decode error")
			return
		}

		if resp.StatusCode != http.StatusOK {
			msg := "strapi login failed"
			if loginResp.Error != nil {
				msg = loginResp.Error.Message
			}
			log.Warn().Int("status", resp.StatusCode).Str("msg", msg).Msg("strapi login error")
			httputil.RespondError(w, http.StatusUnauthorized, msg, msg)
			return
		}

		if loginResp.Data.Token == "" {
			httputil.RespondError(w, http.StatusBadGateway, "empty token from strapi", "empty token from strapi")
			return
		}

		log.Debug().Str("email", email).Msg("strapi token issued")

		httputil.RespondOK(w, map[string]interface{}{
			"token": loginResp.Data.Token,
			"user":  loginResp.Data.User,
		})
	}
}
