package api

import (
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/smtp"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/triangles/polyon-core/internal/httputil"
)

// ── Config helpers ────────────────────────────────────────────────────────────

type smtpConfigFile struct {
	Host        string `json:"host"`
	Port        int    `json:"port"`
	Security    string `json:"security"` // none | starttls | ssl
	Username    string `json:"username"`
	Password    string `json:"password"`
	FromAddress string `json:"from_address"`
	FromName    string `json:"from_name"`
	AlertTo     string `json:"alert_to"`
	Enabled     bool   `json:"enabled"`
}

func smtpConfigPath(d *Deps) string {
	return filepath.Join(d.Cfg.SecretsDir, "smtp.json")
}

func loadSMTPConfig(d *Deps) smtpConfigFile {
	cfg := smtpConfigFile{Port: 587, Security: "starttls", FromName: "PolyON"}
	data, err := os.ReadFile(smtpConfigPath(d))
	if err != nil {
		return cfg
	}
	json.Unmarshal(data, &cfg)
	if cfg.Port == 0 {
		cfg.Port = 587
	}
	if cfg.Security == "" {
		cfg.Security = "starttls"
	}
	return cfg
}

func saveSMTPConfig(d *Deps, cfg smtpConfigFile) error {
	if err := os.MkdirAll(filepath.Dir(smtpConfigPath(d)), 0755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(smtpConfigPath(d), b, 0600)
}

// ── SMTP send helper ──────────────────────────────────────────────────────────

func doSendEmail(cfg smtpConfigFile, to, subject, htmlBody string) error {
	from := fmt.Sprintf("%s <%s>", cfg.FromName, cfg.FromAddress)
	headers := map[string]string{
		"From":         from,
		"To":           to,
		"Subject":      subject,
		"MIME-Version": "1.0",
		"Content-Type": "text/html; charset=UTF-8",
	}
	var msg strings.Builder
	for k, v := range headers {
		msg.WriteString(k + ": " + v + "\r\n")
	}
	msg.WriteString("\r\n")
	msg.WriteString(htmlBody)

	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

	var c *smtp.Client
	var err error

	if cfg.Security == "ssl" {
		tlsCfg := &tls.Config{ServerName: cfg.Host}
		conn, err2 := tls.DialWithDialer(&net.Dialer{Timeout: 15 * time.Second}, "tcp", addr, tlsCfg)
		if err2 != nil {
			return fmt.Errorf("SSL 연결 실패: %w", err2)
		}
		c, err = smtp.NewClient(conn, cfg.Host)
		if err != nil {
			return err
		}
	} else {
		conn, err2 := net.DialTimeout("tcp", addr, 15*time.Second)
		if err2 != nil {
			return fmt.Errorf("연결 실패: %w", err2)
		}
		c, err = smtp.NewClient(conn, cfg.Host)
		if err != nil {
			return err
		}
		if err = c.Hello("polyon"); err != nil {
			return err
		}
		if cfg.Security == "starttls" {
			tlsCfg := &tls.Config{ServerName: cfg.Host}
			if err = c.StartTLS(tlsCfg); err != nil {
				return fmt.Errorf("STARTTLS 실패: %w", err)
			}
		}
	}
	defer c.Close()

	if cfg.Username != "" && cfg.Password != "" {
		auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
		if err = c.Auth(auth); err != nil {
			return fmt.Errorf("인증 실패: %w", err)
		}
	}
	if err = c.Mail(cfg.FromAddress); err != nil {
		return err
	}
	if err = c.Rcpt(to); err != nil {
		return err
	}
	wc, err := c.Data()
	if err != nil {
		return err
	}
	defer wc.Close()
	_, err = wc.Write([]byte(msg.String()))
	return err
}

// ── RegisterSMTP ──────────────────────────────────────────────────────────────

func RegisterSMTP(r chi.Router, d *Deps) {
	r.Route("/smtp", func(r chi.Router) {
		r.Get("/config", smtpGetConfig(d))
		r.Put("/config", smtpPutConfig(d))
		r.Post("/test", smtpTest(d))
		r.Post("/connection-test", smtpConnectionTest(d))
	})
}

// ── GET /smtp/config ──────────────────────────────────────────────────────────

func smtpGetConfig(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := loadSMTPConfig(d)
		masked := cfg
		if masked.Password != "" {
			masked.Password = "••••••••"
		}
		httputil.RespondOK(w, map[string]interface{}{"data": masked})
	}
}

// ── PUT /smtp/config ──────────────────────────────────────────────────────────

func smtpPutConfig(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var incoming smtpConfigFile
		if err := json.NewDecoder(r.Body).Decode(&incoming); err != nil {
			httputil.RespondError(w, 400, "BAD_REQUEST", "Invalid JSON")
			return
		}
		// If password is masked placeholder, keep existing password
		if incoming.Password == "••••••••" {
			existing := loadSMTPConfig(d)
			incoming.Password = existing.Password
		}
		if err := saveSMTPConfig(d, incoming); err != nil {
			httputil.RespondError(w, 500, "SAVE_ERROR", "설정 저장 실패: "+err.Error())
			return
		}
		// Reload SMTP config into memory
		d.Cfg.SMTP.Host = incoming.Host
		d.Cfg.SMTP.Port = incoming.Port
		d.Cfg.SMTP.Security = incoming.Security
		d.Cfg.SMTP.Username = incoming.Username
		d.Cfg.SMTP.Password = incoming.Password
		d.Cfg.SMTP.FromAddress = incoming.FromAddress
		d.Cfg.SMTP.FromName = incoming.FromName
		d.Cfg.SMTP.AlertTo = incoming.AlertTo
		d.Cfg.SMTP.Enabled = incoming.Enabled
		httputil.RespondOK(w, map[string]interface{}{
			"status": "ok", "message": "SMTP 설정이 저장되었습니다.",
		})
	}
}

// ── POST /smtp/test ───────────────────────────────────────────────────────────

func smtpTest(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			To      string `json:"to"`
			Subject string `json:"subject"`
			Body    string `json:"body"`
		}
		req.Subject = "HELIOS 테스트 메일"
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.RespondError(w, 400, "BAD_REQUEST", "Invalid JSON")
			return
		}
		if req.To == "" {
			httputil.RespondError(w, 400, "BAD_REQUEST", "수신자 이메일이 필요합니다")
			return
		}

		cfg := loadSMTPConfig(d)
		if cfg.Host == "" || cfg.FromAddress == "" {
			httputil.RespondError(w, 400, "NOT_CONFIGURED", "SMTP 서버가 설정되지 않았습니다.")
			return
		}

		body := req.Body
		if body == "" {
			body = fmt.Sprintf(`<div style="font-family:'IBM Plex Sans',Arial,sans-serif;max-width:500px;margin:0 auto;">
  <div style="background:#161616;color:#fff;padding:1rem 1.5rem;">
    <h2 style="margin:0;font-size:1rem;">HELIOS</h2>
  </div>
  <div style="padding:1.5rem;border:1px solid #e0e0e0;border-top:none;">
    <p style="margin:0 0 1rem;font-size:0.9375rem;">SMTP 테스트 메일이 정상적으로 발송되었습니다.</p>
    <table style="font-size:0.8125rem;border-collapse:collapse;width:100%%;">
      <tr><td style="padding:4px 8px;font-weight:600;">서버</td><td style="padding:4px 8px;">%s:%d</td></tr>
      <tr><td style="padding:4px 8px;font-weight:600;">보안</td><td style="padding:4px 8px;">%s</td></tr>
      <tr><td style="padding:4px 8px;font-weight:600;">발신자</td><td style="padding:4px 8px;">%s</td></tr>
    </table>
    <p style="margin:1rem 0 0;font-size:0.8125rem;color:#525252;">이 메일은 PolyON 관리 콘솔에서 발송된 테스트 메일입니다.</p>
  </div>
</div>`, cfg.Host, cfg.Port, strings.ToUpper(cfg.Security), cfg.FromAddress)
		}

		if err := doSendEmail(cfg, req.To, req.Subject, body); err != nil {
			code := "SEND_FAILED"
			status := 500
			errMsg := err.Error()
			if strings.Contains(errMsg, "인증 실패") || strings.Contains(strings.ToLower(errMsg), "authentication") {
				code = "AUTH_FAILED"
				status = 401
			} else if strings.Contains(errMsg, "연결 실패") || strings.Contains(strings.ToLower(errMsg), "connection refused") {
				code = "CONNECTION_FAILED"
				status = 502
			}
			httputil.RespondError(w, status, code, "메일 발송 실패: "+errMsg)
			return
		}
		httputil.RespondOK(w, map[string]interface{}{
			"status": "ok",
			"message": fmt.Sprintf("테스트 메일이 %s으로 발송되었습니다.", req.To),
		})
	}
}

// ── POST /smtp/connection-test ────────────────────────────────────────────────

func smtpConnectionTest(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cfg := loadSMTPConfig(d)
		if cfg.Host == "" {
			httputil.RespondError(w, 400, "NOT_CONFIGURED", "SMTP 서버가 설정되지 않았습니다.")
			return
		}

		addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)

		var c *smtp.Client
		var err error

		if cfg.Security == "ssl" {
			tlsCfg := &tls.Config{ServerName: cfg.Host}
			conn, err2 := tls.DialWithDialer(&net.Dialer{Timeout: 10 * time.Second}, "tcp", addr, tlsCfg)
			if err2 != nil {
				httputil.RespondError(w, 502, "CONNECTION_FAILED", "SSL 연결 실패: "+err2.Error())
				return
			}
			c, err = smtp.NewClient(conn, cfg.Host)
		} else {
			conn, err2 := net.DialTimeout("tcp", addr, 10*time.Second)
			if err2 != nil {
				httputil.RespondError(w, 502, "CONNECTION_FAILED", "SMTP 연결 실패: "+err2.Error())
				return
			}
			c, err = smtp.NewClient(conn, cfg.Host)
			if err == nil {
				c.Hello("polyon")
				if cfg.Security == "starttls" {
					tlsCfg := &tls.Config{ServerName: cfg.Host}
					if err2 = c.StartTLS(tlsCfg); err2 != nil {
						c.Close()
						httputil.RespondError(w, 502, "TLS_FAILED", "STARTTLS 실패: "+err2.Error())
						return
					}
				}
			}
		}

		if err != nil {
			httputil.RespondError(w, 502, "CONNECTION_FAILED", "SMTP 클라이언트 생성 실패: "+err.Error())
			return
		}
		defer c.Close()

		if cfg.Username != "" && cfg.Password != "" {
			auth := smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)
			if err = c.Auth(auth); err != nil {
				httputil.RespondError(w, 401, "AUTH_FAILED", "SMTP 인증 실패: "+err.Error())
				return
			}
		}

		c.Quit()
		httputil.RespondOK(w, map[string]interface{}{
			"status": "ok", "message": "SMTP 서버 연결 성공",
			"host": cfg.Host, "port": cfg.Port, "security": cfg.Security,
		})
	}
}
