package api

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	minio "github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/triangles/polyon-core/internal/httputil"
)

// ── Stalwart HTTP helper ──────────────────────────────────────────────────────

func stalwartDo(d *Deps, method, path string, body interface{}) (*http.Response, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		reqBody = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, d.Cfg.StalwartURL+path, reqBody)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(d.Cfg.StalwartAdminUser, d.Cfg.StalwartAdminPassword)
	req.Header.Set("Content-Type", "application/json")
	return (&http.Client{Timeout: 15 * time.Second}).Do(req)
}

// ── AWS Sig V4 bucket creation (no external deps) ─────────────────────────────

func hmacSHA256bytes(key []byte, data string) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write([]byte(data))
	return mac.Sum(nil)
}

func createS3Bucket(endpoint, accessKey, secretKey, bucket string) (status, detail string) {
	method := "PUT"
	rawURL := strings.TrimRight(endpoint, "/") + "/" + bucket
	now := time.Now().UTC()
	date := now.Format("20060102")
	datetime := now.Format("20060102T150405Z")
	const region = "us-east-1"
	const svc = "s3"
	const emptyHash = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

	req, err := http.NewRequest(method, rawURL, nil)
	if err != nil {
		return "error", err.Error()
	}
	host := req.URL.Host
	req.Header.Set("Host", host)
	req.Header.Set("x-amz-date", datetime)
	req.Header.Set("x-amz-content-sha256", emptyHash)

	canonicalHeaders := "host:" + host + "\n" +
		"x-amz-content-sha256:" + emptyHash + "\n" +
		"x-amz-date:" + datetime + "\n"
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalReq := strings.Join([]string{method, "/" + bucket, "", canonicalHeaders, signedHeaders, emptyHash}, "\n")
	reqHash := fmt.Sprintf("%x", sha256.Sum256([]byte(canonicalReq)))
	scope := date + "/" + region + "/" + svc + "/aws4_request"
	stringToSign := "AWS4-HMAC-SHA256\n" + datetime + "\n" + scope + "\n" + reqHash

	kDate := hmacSHA256bytes([]byte("AWS4"+secretKey), date)
	kRegion := hmacSHA256bytes(kDate, region)
	kService := hmacSHA256bytes(kRegion, svc)
	kSigning := hmacSHA256bytes(kService, "aws4_request")
	signature := fmt.Sprintf("%x", hmacSHA256bytes(kSigning, stringToSign))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		accessKey, scope, signedHeaders, signature))

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "error", err.Error()
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case 200, 201:
		return "ok", fmt.Sprintf("RustFS 버킷 '%s' 생성 완료", bucket)
	case 409:
		return "skip", fmt.Sprintf("RustFS 버킷 '%s' 이미 존재", bucket)
	default:
		b, _ := io.ReadAll(resp.Body)
		return "error", fmt.Sprintf("버킷 생성 실패: HTTP %d %s", resp.StatusCode, string(b))
	}
}

// ── Stalwart CLI via Docker exec ──────────────────────────────────────────────

func runStalwartCLI(d *Deps, args ...string) error {
	stalwartPW := d.Cfg.StalwartAdminPassword
	cmdArgs := append([]string{
		"stalwart-cli",
		"-u", "https://localhost:443",
		"-c", "admin:" + stalwartPW,
	}, args...)
	_, err := d.Docker.ExecCommand("polyon-mail", cmdArgs...)
	return err
}

// ── Setup JSON helpers ────────────────────────────────────────────────────────

func readSetupJSON(d *Deps) map[string]interface{} {
	data, err := os.ReadFile(d.Cfg.SetupJSONPath())
	if err != nil {
		return map[string]interface{}{}
	}
	var m map[string]interface{}
	json.Unmarshal(data, &m)
	if m == nil {
		m = map[string]interface{}{}
	}
	return m
}

func writeSetupJSON(d *Deps, m map[string]interface{}) {
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return
	}
	os.WriteFile(d.Cfg.SetupJSONPath(), b, 0644)
}

func getContainerIP(host string) string {
	addrs, err := net.LookupHost(host)
	if err != nil || len(addrs) == 0 {
		return "172.20.0.100"
	}
	return addrs[0]
}

// ── Stalwart storage backend configuration ────────────────────────────────────

func configureStorage(d *Deps, realm string) []map[string]interface{} {
	steps := []map[string]interface{}{}

	// Step 1: Create S3 bucket
	s, det := createS3Bucket(d.Cfg.RustFSEndpoint, d.Cfg.RustFSAccessKey, d.Cfg.RustFSSecretKey, "stalwart-blobs")
	steps = append(steps, map[string]interface{}{"name": "rustfs_bucket", "status": s, "detail": det})

	// Step 2: Build base DN
	realmLower := strings.ToLower(realm)
	realmUpper := strings.ToUpper(realm)
	parts := strings.Split(realmLower, ".")
	dcs := make([]string, len(parts))
	for i, p := range parts {
		dcs[i] = "DC=" + p
	}
	baseDN := strings.Join(dcs, ",")

	// Step 3: Delete old storage config and set new
	runStalwartCLI(d, "server", "delete-config", "storage")

	settings := [][2]string{
		{"storage.data", "rocksdb"}, {"storage.blob", "s3"},
		{"storage.fts", "elasticsearch"}, {"storage.lookup", "rocksdb"},
		{"storage.directory", "ldap"},
		{"store.s3.type", "s3"}, {"store.s3.bucket", "stalwart-blobs"},
		{"store.s3.endpoint", d.Cfg.RustFSEndpoint},
		{"store.s3.access-key", d.Cfg.RustFSAccessKey},
		{"store.s3.secret-key", d.Cfg.RustFSSecretKey},
		{"store.s3.region", "us-east-1"},
		{"store.elasticsearch.type", "elasticsearch"},
		{"store.elasticsearch.url", d.Cfg.ElasticURL},
		{"store.elasticsearch.user", "elastic"},
		{"store.elasticsearch.password", d.Cfg.ElasticPassword},
		{"store.elasticsearch.index", "stalwart"},
		{"directory.ldap.type", "ldap"},
		{"directory.ldap.url", "ldap://polyon-dc:389"},
		{"directory.ldap.base-dn", baseDN},
		{"directory.ldap.timeout", "10s"},
		{"directory.ldap.tls.enable", "false"},
		{"directory.ldap.tls.allow-invalid-certs", "true"},
		{"directory.ldap.bind.dn", "Administrator@" + realmUpper},
		{"directory.ldap.bind.secret", d.Cfg.DCAdminPassword},
		{"directory.ldap.bind.auth.method", "template"},
		{"directory.ldap.bind.auth.template", "{local}@" + realmUpper},
		{"directory.ldap.bind.auth.search", "true"},
		{"directory.ldap.filter.name", "(&(objectClass=user)(sAMAccountName={local})(!(objectClass=computer)))"},
		{"directory.ldap.filter.email", "(&(objectClass=user)(|(userPrincipalName=?)(mail=?)(proxyAddresses=smtp:?))(!(objectClass=computer)))"},
		{"directory.ldap.filter.verify", "(&(objectClass=user)(|(userPrincipalName=*@?)(mail=*@?)))"},
		{"directory.ldap.filter.expand", "(&(objectClass=group)(mail=?))"},
		{"directory.ldap.filter.domains", "(&(objectClass=user)(userPrincipalName=*@?))"},
		{"directory.ldap.attributes.name", "sAMAccountName"},
		{"directory.ldap.attributes.class", "objectClass"},
		{"directory.ldap.attributes.description", "displayName"},
		{"directory.ldap.attributes.groups", "memberOf"},
		{"directory.ldap.attributes.email", "userPrincipalName"},
		{"directory.ldap.attributes.email-alias", "proxyAddresses"},
		{"directory.ldap.attributes.quota", "mailQuota"},
	}

	var failed []string
	for _, kv := range settings {
		// Use "--" separator before value to prevent CLI from interpreting
		// values starting with "-" as flags (e.g. passwords like "-ODN-...")
		if err := runStalwartCLI(d, "server", "add-config", kv[0], "--", kv[1]); err != nil {
			failed = append(failed, kv[0])
		}
	}
	if len(failed) > 0 {
		n := len(failed)
		if n > 5 {
			n = 5
		}
		steps = append(steps, map[string]interface{}{
			"name": "stalwart_config", "status": "error",
			"detail": "설정 실패 키: " + strings.Join(failed[:n], ", "),
		})
	} else {
		steps = append(steps, map[string]interface{}{
			"name": "stalwart_config", "status": "ok",
			"detail": fmt.Sprintf("Stalwart storage 설정 완료 (%d개 키)", len(settings)),
		})
	}

	// Step 4: Reload
	if err := runStalwartCLI(d, "server", "reload-config"); err != nil {
		steps = append(steps, map[string]interface{}{
			"name": "stalwart_reload", "status": "error",
			"detail": "Reload 오류: " + err.Error(),
		})
	} else {
		steps = append(steps, map[string]interface{}{
			"name": "stalwart_reload", "status": "ok",
			"detail": "Stalwart 설정 리로드 완료",
		})
	}

	return steps
}

// ── RegisterMail ──────────────────────────────────────────────────────────────

func RegisterMail(r chi.Router, d *Deps) {
	r.Route("/mail", func(r chi.Router) {
		r.Post("/provision", mailProvision(d))
		r.Get("/status", mailStatus(d))
		r.Get("/service-check", mailServiceCheck(d))
		r.Get("/dns-checklist", mailDNSChecklist(d))
		r.Get("/storage/overview", mailStorageOverview(d))
	})
}

// ── POST /mail/provision ──────────────────────────────────────────────────────

func mailProvision(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Domain           string `json:"domain"`
			MailHostname     string `json:"mail_hostname"`
			MailServerIP     string `json:"mail_server_ip"`
			CreatePostmaster bool   `json:"create_postmaster"`
			PostmasterPass   string `json:"postmaster_password"`
		}
		req.CreatePostmaster = true
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httputil.RespondError(w, 400, "BAD_REQUEST", "Invalid JSON")
			return
		}
		domain := strings.ToLower(strings.TrimSpace(req.Domain))
		if domain == "" {
			httputil.RespondError(w, 400, "BAD_REQUEST", "domain is required")
			return
		}
		mailHost := req.MailHostname
		if mailHost == "" {
			mailHost = "mail." + domain
		}
		mailHost = strings.ToLower(strings.TrimSpace(mailHost))
		mailHostShort := mailHost
		if strings.HasSuffix(mailHost, "."+domain) {
			mailHostShort = mailHost[:len(mailHost)-len("."+domain)]
		}

		steps := []map[string]interface{}{}

		// ── Step 0: Configure Stalwart storage ──
		steps = append(steps, configureStorage(d, domain)...)

		// ── Step 1: Register domain in Stalwart ──
		domainExists := false
		if resp, err := stalwartDo(d, "GET", "/api/principal/"+domain, nil); err == nil {
			if resp.StatusCode == 200 {
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				var cd map[string]interface{}
				json.Unmarshal(body, &cd)
				domainExists = cd["error"] == nil
			} else {
				resp.Body.Close()
			}
		}

		if domainExists {
			steps = append(steps, map[string]interface{}{
				"name": "stalwart_domain", "status": "skip",
				"detail": "도메인이 이미 등록되어 있습니다",
			})
		} else {
			resp, err := stalwartDo(d, "POST", "/api/principal", map[string]interface{}{
				"type": "domain", "name": domain,
			})
			if err != nil {
				steps = append(steps, map[string]interface{}{
					"name": "stalwart_domain", "status": "error",
					"detail": "Stalwart 연결 실패: " + err.Error(),
				})
			} else {
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode == 200 || resp.StatusCode == 201 {
					steps = append(steps, map[string]interface{}{
						"name": "stalwart_domain", "status": "ok",
						"detail": domain + " 도메인 등록 완료",
					})
				} else {
					steps = append(steps, map[string]interface{}{
						"name": "stalwart_domain", "status": "error",
						"detail": fmt.Sprintf("Stalwart 도메인 등록 실패: %d %s", resp.StatusCode, string(body)),
					})
				}
			}
		}

		// ── Step 2: Generate DKIM keys ──
		for _, algo := range []string{"Ed25519", "Rsa"} {
			resp, err := stalwartDo(d, "POST", "/api/dkim", map[string]interface{}{
				"id": nil, "algorithm": algo, "domain": domain, "selector": nil,
			})
			algoLow := strings.ToLower(algo)
			if err != nil {
				steps = append(steps, map[string]interface{}{
					"name": "dkim_" + algoLow, "status": "error",
					"detail": "DKIM 생성 오류: " + err.Error(),
				})
				continue
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode == 200 || resp.StatusCode == 201 {
				steps = append(steps, map[string]interface{}{
					"name": "dkim_" + algoLow, "status": "ok",
					"detail": "DKIM " + algo + " 키 생성 완료",
				})
				if dkimTXT := extractDKIMDNS(body, algo, domain); dkimTXT != nil {
					dnsResult := d.Samba.AddDNSRecord(domain, dkimTXT["name"], "TXT", dkimTXT["value"])
					dnsStatus := "ok"
					dnsDetail := "DKIM DNS TXT 등록: " + dkimTXT["name"] + "." + domain
					if !dnsResult.Success {
						if strings.Contains(strings.ToLower(dnsResult.Error), "already exists") {
							dnsStatus = "skip"
							dnsDetail = "DKIM DNS 이미 존재"
						} else {
							dnsStatus = "error"
							dnsDetail = "DKIM DNS 등록 실패: " + dnsResult.Error
						}
					}
					steps = append(steps, map[string]interface{}{
						"name": "dns_dkim_" + algoLow, "status": dnsStatus, "detail": dnsDetail,
					})
				}
			} else {
				steps = append(steps, map[string]interface{}{
					"name": "dkim_" + algoLow, "status": "error",
					"detail": fmt.Sprintf("DKIM %s 생성 실패: %d", algo, resp.StatusCode),
				})
			}
		}

		// ── Step 3: Add DNS records ──
		serverIP := req.MailServerIP
		if serverIP == "" {
			serverIP = getContainerIP("polyon-mail")
		}
		type dnsRec struct{ name, rtype, data string }
		dnsRecords := []dnsRec{
			{"@", "MX", mailHost + " 10"},
			{"@", "TXT", "v=spf1 mx a:" + mailHost + " -all"},
			{"_dmarc", "TXT", "v=DMARC1; p=quarantine; rua=mailto:postmaster@" + domain + "; ruf=mailto:postmaster@" + domain},
			{mailHostShort, "A", serverIP},
			{"_imaps._tcp", "SRV", mailHost + " 993 0 1"},
			{"_submission._tcp", "SRV", mailHost + " 587 0 1"},
			{"_submissions._tcp", "SRV", mailHost + " 465 0 1"},
			{"autoconfig", "CNAME", mailHost},
			{"autodiscover", "CNAME", mailHost},
			{"mta-sts", "CNAME", mailHost},
			{"_mta-sts", "TXT", "v=STSv1; id=polyon001"},
			{"_smtp._tls", "TXT", "v=TLSRPTv1; rua=mailto:postmaster@" + domain},
		}
		for _, rec := range dnsRecords {
			result := d.Samba.AddDNSRecord(domain, rec.name, rec.rtype, rec.data)
			stepName := "dns_" + strings.ToLower(rec.rtype) + "_" + strings.ReplaceAll(rec.name, ".", "_")
			if result.Success {
				steps = append(steps, map[string]interface{}{
					"name": stepName, "status": "ok",
					"detail": rec.rtype + " " + rec.name + " → " + rec.data,
				})
			} else {
				s := "error"
				det := rec.rtype + " " + rec.name + " 추가 실패: " + result.Error
				if strings.Contains(strings.ToLower(result.Error), "already exists") ||
					strings.Contains(result.Error, "ALREADY_EXISTS") {
					s = "skip"
					det = rec.rtype + " " + rec.name + " 이미 존재"
				}
				steps = append(steps, map[string]interface{}{"name": stepName, "status": s, "detail": det})
			}
		}

		// ── Step 4: Create postmaster account ──
		if req.CreatePostmaster {
			postmasterPass := req.PostmasterPass
			if postmasterPass == "" {
				if setup := readSetupJSON(d); setup != nil {
					if p, ok := setup["admin_password"].(string); ok {
						postmasterPass = p
					}
				}
			}
			if postmasterPass != "" {
				pmExists := false
				if resp, err := stalwartDo(d, "GET", "/api/principal/postmaster@"+domain, nil); err == nil {
					if resp.StatusCode == 200 {
						body, _ := io.ReadAll(resp.Body)
						resp.Body.Close()
						var cd map[string]interface{}
						json.Unmarshal(body, &cd)
						pmExists = cd["error"] == nil
					} else {
						resp.Body.Close()
					}
				}
				if pmExists {
					steps = append(steps, map[string]interface{}{
						"name": "postmaster", "status": "skip",
						"detail": "postmaster@" + domain + " 이미 존재",
					})
				} else {
					resp, err := stalwartDo(d, "POST", "/api/principal", map[string]interface{}{
						"type":    "individual",
						"name":    "postmaster@" + domain,
						"secrets": []string{postmasterPass},
						"emails":  []string{"postmaster@" + domain},
					})
					if err != nil {
						steps = append(steps, map[string]interface{}{
							"name": "postmaster", "status": "error",
							"detail": "postmaster 생성 오류: " + err.Error(),
						})
					} else {
						body, _ := io.ReadAll(resp.Body)
						resp.Body.Close()
						if resp.StatusCode == 200 || resp.StatusCode == 201 {
							steps = append(steps, map[string]interface{}{
								"name": "postmaster", "status": "ok",
								"detail": "postmaster@" + domain + " 계정 생성 완료",
							})
						} else {
							steps = append(steps, map[string]interface{}{
								"name": "postmaster", "status": "error",
								"detail": fmt.Sprintf("postmaster 생성 실패: %d %s", resp.StatusCode, string(body)),
							})
						}
					}
				}
			} else {
				steps = append(steps, map[string]interface{}{
					"name": "postmaster", "status": "skip",
					"detail": "비밀번호 없음 — 수동 생성 필요",
				})
			}
		}

		// ── Step 5: Register AD users as mail accounts ──
		adUsers, err := d.Samba.ListUsers()
		if err == nil {
			registered := 0
			for _, user := range adUsers {
				un := user.Username
				if strings.EqualFold(un, "krbtgt") || strings.EqualFold(un, "guest") {
					continue
				}
				// Check if account already exists
				if resp, err2 := stalwartDo(d, "GET", "/api/principal/"+un, nil); err2 == nil {
					if resp.StatusCode == 200 {
						body, _ := io.ReadAll(resp.Body)
						resp.Body.Close()
						var cd map[string]interface{}
						json.Unmarshal(body, &cd)
						if cd["error"] == nil {
							continue
						}
					} else {
						resp.Body.Close()
					}
				}
				if resp, err2 := stalwartDo(d, "POST", "/api/principal", map[string]interface{}{
					"type": "individual", "name": un,
					"emails": []string{un + "@" + domain},
				}); err2 == nil {
					resp.Body.Close()
					registered++
				}
			}
			steps = append(steps, map[string]interface{}{
				"name": "mail_accounts", "status": "ok",
				"detail": fmt.Sprintf("AD 사용자 %d명 메일 계정 등록 (전체 %d명)", registered, len(adUsers)),
			})
		} else {
			steps = append(steps, map[string]interface{}{
				"name": "mail_accounts", "status": "error",
				"detail": "AD 사용자 목록 조회 실패: " + err.Error(),
			})
		}

		// ── Save mail config to setup.json ──
		setup := readSetupJSON(d)
		setup["mail_provisioned"] = true
		setup["mail_hostname"] = mailHost
		setup["mail_domain"] = domain
		writeSetupJSON(d, setup)

		allOK := true
		for _, s := range steps {
			if st, ok := s["status"].(string); ok && st == "error" {
				allOK = false
				break
			}
		}
		httputil.RespondOK(w, map[string]interface{}{
			"domain": domain, "mail_hostname": mailHost,
			"steps": steps, "success": allOK,
		})
	}
}

// extractDKIMDNS parses the DKIM DNS record from Stalwart API response.
func extractDKIMDNS(data []byte, algo, domain string) map[string]string {
	_ = domain
	var parsed interface{}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil
	}
	switch v := parsed.(type) {
	case string:
		return parseDKIMText(v, algo)
	case map[string]interface{}:
		if selector, ok := v["selector"].(string); ok {
			record := fmt.Sprintf("%v", v["record"])
			if strings.Contains(record, "v=DKIM1") {
				return map[string]string{"name": selector + "._domainkey", "value": record}
			}
		}
		if d2, ok := v["data"].(string); ok {
			return parseDKIMText(d2, algo)
		}
	}
	return nil
}

func parseDKIMText(text, algo string) map[string]string {
	selIdx := strings.Index(text, "._domainkey")
	if selIdx > 0 {
		start := selIdx - 1
		for start > 0 && isWordChar(text[start-1]) {
			start--
		}
		selector := text[start:selIdx]
		if di := strings.Index(text, "v=DKIM1"); di >= 0 {
			return map[string]string{"name": selector + "._domainkey", "value": text[di:]}
		}
	}
	if di := strings.Index(text, "v=DKIM1"); di >= 0 {
		return map[string]string{
			"name":  "polyon-" + strings.ToLower(algo) + "._domainkey",
			"value": text[di:],
		}
	}
	return nil
}

func isWordChar(c byte) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '_'
}

// ── GET /mail/status ──────────────────────────────────────────────────────────

func mailStatus(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setup := readSetupJSON(d)
		provisioned, _ := setup["mail_provisioned"].(bool)
		domain, _ := setup["mail_domain"].(string)
		hostname, _ := setup["mail_hostname"].(string)
		httputil.RespondOK(w, map[string]interface{}{
			"provisioned": provisioned,
			"domain":      domain,
			"hostname":    hostname,
		})
	}
}

// ── GET /mail/service-check ───────────────────────────────────────────────────

func mailServiceCheck(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		const mailHost = "polyon-mail"
		results := map[string]bool{}

		tcpServices := map[string]int{
			"smtp": 25, "submission": 587, "imap": 993, "pop3": 995, "sieve": 4190,
		}
		for name, port := range tcpServices {
			conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", mailHost, port), 3*time.Second)
			if err == nil {
				conn.Close()
				results[name] = true
			} else {
				results[name] = false
			}
		}

		httpChecks := []struct {
			name, method, path string
			codes              []int
		}{
			{"jmap", "GET", "/.well-known/jmap", []int{200, 307}},
			{"caldav", "PROPFIND", "/dav/cal", []int{200, 207, 401, 307}},
			{"carddav", "PROPFIND", "/dav/card", []int{200, 207, 401, 307}},
		}
		httpClient := &http.Client{
			Timeout: 5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		}
		for _, check := range httpChecks {
			req, err := http.NewRequest(check.method, d.Cfg.StalwartURL+check.path, nil)
			if err != nil {
				results[check.name] = false
				continue
			}
			if check.method == "PROPFIND" {
				req.Header.Set("Depth", "0")
			}
			resp, err := httpClient.Do(req)
			if err != nil {
				results[check.name] = false
				continue
			}
			resp.Body.Close()
			ok := false
			for _, code := range check.codes {
				if resp.StatusCode == code {
					ok = true
					break
				}
			}
			results[check.name] = ok
		}

		httputil.RespondOK(w, map[string]interface{}{"services": results})
	}
}

// ── GET /mail/dns-checklist ───────────────────────────────────────────────────

func mailDNSChecklist(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		setup := readSetupJSON(d)
		domain, _ := setup["mail_domain"].(string)
		if domain == "" {
			if realm, ok := setup["realm"].(string); ok {
				domain = strings.ToLower(realm)
			}
		}
		if domain == "" {
			httputil.RespondOK(w, map[string]interface{}{"records": []interface{}{}, "domain": ""})
			return
		}
		mailHost, _ := setup["mail_hostname"].(string)
		if mailHost == "" {
			mailHost = "mail." + domain
		}
		mailHostShort := mailHost
		if strings.HasSuffix(mailHost, "."+domain) {
			mailHostShort = mailHost[:len(mailHost)-len("."+domain)]
		}

		zoneResult := d.Samba.ListDNSRecords(domain)
		zoneOutput := strings.ToLower(zoneResult.Output)

		required := []map[string]interface{}{
			{"type": "MX", "name": "@", "desc": "메일 수신 서버"},
			{"type": "TXT (SPF)", "name": "@", "desc": "발신 서버 인증"},
			{"type": "TXT (DMARC)", "name": "_dmarc", "desc": "메일 인증 정책"},
			{"type": "A", "name": mailHostShort, "desc": "메일 서버 IP"},
			{"type": "SRV", "name": "_imaps._tcp", "desc": "IMAP 클라이언트 자동 설정"},
			{"type": "SRV", "name": "_submission._tcp", "desc": "SMTP 클라이언트 자동 설정"},
			{"type": "CNAME", "name": "autoconfig", "desc": "Thunderbird 자동 구성"},
			{"type": "CNAME", "name": "autodiscover", "desc": "Outlook 자동 구성"},
		}
		for _, rec := range required {
			name, _ := rec["name"].(string)
			rec["exists"] = strings.Contains(zoneOutput, strings.ToLower(name))
		}

		httputil.RespondOK(w, map[string]interface{}{
			"records": required, "domain": domain, "mail_hostname": mailHost,
		})
	}
}

// ── Mail account helpers used by users.go ─────────────────────────────────────

// stalwartProvisionAccount creates a Stalwart account for an AD-synced user (no password — LDAP auth only).
func stalwartProvisionAccount(d *Deps, username, domain string) bool {
	resp, err := stalwartDo(d, "POST", "/api/principal", map[string]interface{}{
		"type":    "individual",
		"name":    username,
		"emails":  []string{username + "@" + domain},
		"secrets": []string{}, // no password — authenticate via LDAP (Option C)
	})
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200 || resp.StatusCode == 201
}

// stalwartProvisionStandaloneAccount creates a Stalwart account for a standalone (non-LDAP) user with a password.
// Used for project/service accounts managed directly in Console.
func stalwartProvisionStandaloneAccount(d *Deps, username, domain, password string) bool {
	body := map[string]interface{}{
		"type":   "individual",
		"name":   username,
		"emails": []string{username + "@" + domain},
	}
	if password != "" {
		body["secrets"] = []string{password}
	}
	resp, err := stalwartDo(d, "POST", "/api/principal", body)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200 || resp.StatusCode == 201
}

func stalwartDeleteAccount(d *Deps, username string) {
	if resp, err := stalwartDo(d, "DELETE", "/api/principal/"+username, nil); err == nil && resp != nil {
		resp.Body.Close()
	}
}

// ── GET /mail/storage/overview ────────────────────────────────────────────────

func mailStorageOverview(d *Deps) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		type storeEntry struct {
			Name    string `json:"name"`
			Backend string `json:"backend"`
			Status  string `json:"status"`
			Details string `json:"details"`
		}
		type bucketInfo struct {
			Name        string `json:"name"`
			Endpoint    string `json:"endpoint"`
			ObjectCount int64  `json:"object_count"`
			TotalBytes  int64  `json:"total_bytes"`
			Status      string `json:"status"`
		}

		// 1. Stalwart storage 설정 조회
		stores := []storeEntry{}
		type settingResp struct {
			Data struct {
				Items map[string]string `json:"items"`
			} `json:"data"`
		}
		resp, err := stalwartDo(d, "GET", "/api/settings/list?prefix=storage", nil)
		if err == nil && resp != nil {
			var sr settingResp
			if json.NewDecoder(resp.Body).Decode(&sr) == nil {
				resp.Body.Close()
				labelMap := map[string]string{
					"data":      "메일 메타데이터",
					"blob":      "첨부파일(Blob)",
					"fts":       "전문검색(FTS)",
					"lookup":    "조회 캐시",
					"directory": "사용자 디렉터리",
				}
				backendMap := map[string]string{
					"rocksdb":       "RocksDB (내부)",
					"s3":            "S3 / RustFS",
					"elasticsearch": "OpenSearch",
					"opensearch":    "OpenSearch",
					"redis":         "Redis",
					"postgresql":    "PostgreSQL",
				}
				for key, backend := range sr.Data.Items {
					label := labelMap[key]
					if label == "" {
						label = key
					}
					bLabel := backendMap[backend]
					if bLabel == "" {
						bLabel = backend
					}
					status := "ok"
					if backend == "rocksdb" && key == "blob" {
						status = "warn" // blob은 S3 권장
					}
					stores = append(stores, storeEntry{
						Name:    label,
						Backend: bLabel,
						Status:  status,
						Details: key + " = " + backend,
					})
				}
			} else if resp != nil {
				resp.Body.Close()
			}
		}

		// 2. S3 버킷 현황 (stalwart-blobs)
		bucket := bucketInfo{
			Name:     "stalwart-blobs",
			Endpoint: d.Cfg.RustFSEndpoint,
			Status:   "unknown",
		}
		if d.Cfg.RustFSEndpoint != "" && d.Cfg.RustFSAccessKey != "" {
			ep := strings.TrimPrefix(d.Cfg.RustFSEndpoint, "https://")
			ep = strings.TrimPrefix(ep, "http://")
			secure := strings.HasPrefix(d.Cfg.RustFSEndpoint, "https://")
			mc, merr := minio.New(ep, &minio.Options{
				Creds:  credentials.NewStaticV4(d.Cfg.RustFSAccessKey, d.Cfg.RustFSSecretKey, ""),
				Secure: secure,
			})
			if merr == nil {
				objs := mc.ListObjects(ctx, "stalwart-blobs", minio.ListObjectsOptions{Recursive: true})
				var count int64
				var totalBytes int64
				for obj := range objs {
					if obj.Err == nil {
						count++
						totalBytes += obj.Size
					}
				}
				bucket.ObjectCount = count
				bucket.TotalBytes = totalBytes
				bucket.Status = "ok"
			} else {
				bucket.Status = "error"
			}
		}

		// 3. PRC claim 상태
		type claimInfo struct {
			Type    string `json:"type"`
			Status  string `json:"status"`
			Details string `json:"details"`
		}
		claims := []claimInfo{}
		if d.PRC != nil {
			if items, err := d.PRC.ListClaims(ctx, "", "", "mail"); err == nil {
				for _, item := range items {
					claims = append(claims, claimInfo{
						Type:   item.ClaimType,
						Status: item.Status,
					})
				}
			}
		}

		// mail 모듈은 Wizard 배포 → PRC 없음 안내
		prcNote := ""
		if len(claims) == 0 {
			prcNote = "mail 모듈은 Wizard 직접 배포 (PRC 미실행)"
		}

		httputil.RespondOK(w, map[string]interface{}{
			"stores":   stores,
			"bucket":   bucket,
			"claims":   claims,
			"prc_note": prcNote,
		})
	}
}
