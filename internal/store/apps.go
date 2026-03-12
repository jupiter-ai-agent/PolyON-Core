package store

import (
	"context"
	"time"

	"github.com/rs/zerolog/log"
)

// AppMeta mirrors the app catalog schema stored in polyon_apps table.
type AppMeta struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Desc       string   `json:"desc"`
	Base       string   `json:"base"`
	Icon       string   `json:"icon"`
	Category   string   `json:"category"`
	Subdomain  string   `json:"subdomain"`
	Containers []string `json:"containers"`
	BaseStatus string   `json:"baseStatus"` // "available" | "coming-soon" | "active" | "requires-setup"
	Details    string   `json:"details"`
	BackendURL string   `json:"backendUrl"`    // Internal backend URL for Traefik reverse proxy
	AccessMode string   `json:"accessMode"`    // "subdomain" | "url"
	PathPrefix string   `json:"pathPrefix"`    // URL pattern path (e.g. "/chat")
}

// seedApps is the canonical app catalog seeded into the DB on first run.
var seedApps = []AppMeta{
	{ID: "sso", Name: "Single Sign-On", Desc: "통합 인증 · OIDC · LDAP Federation", Base: "Keycloak", Icon: "key", Category: "platform", Subdomain: "sso", Containers: []string{"polyon-auth"}, BaseStatus: "requires-setup", Details: "Keycloak 기반 SSO. 활성화 시 services realm 자동 생성.", BackendURL: "http://polyon-auth:8080"},
	{ID: "portal", Name: "Portal", Desc: "사용자 홈 · 앱 런처 · 대시보드", Base: "Custom", Icon: "home", Category: "platform", Subdomain: "", Containers: []string{"polyon-portal"}, BaseStatus: "coming-soon", Details: "직원이 로그인하면 처음 보는 화면.", BackendURL: "http://polyon-portal:3000"},
	{ID: "notification", Name: "Notification", Desc: "통합 알림 · 크로스앱 이벤트 · 푸시", Base: "Custom (Redis)", Icon: "notification", Category: "platform", Subdomain: "", Containers: []string{"polyon-notification"}, BaseStatus: "coming-soon", Details: "모든 앱의 알림을 한 곳에서.", BackendURL: ""},
	{ID: "search", Name: "Search", Desc: "통합 검색 · 크로스앱 인덱싱", Base: "Elasticsearch", Icon: "search", Category: "platform", Subdomain: "", Containers: []string{"polyon-search"}, BaseStatus: "coming-soon", Details: "글로벌 검색.", BackendURL: "http://polyon-search:9200"},
	{ID: "vault", Name: "Vault", Desc: "비밀번호 관리 · 팀 인증정보 공유", Base: "Vaultwarden", Icon: "locked", Category: "platform", Subdomain: "vault", Containers: []string{"app-vaultwarden"}, BaseStatus: "available", Details: "Bitwarden 호환 비밀번호 관리.", BackendURL: "http://app-vaultwarden:80"},
	{ID: "mail", Name: "Mail", Desc: "SMTP · IMAP · 웹메일", Base: "Stalwart Mail", Icon: "email", Category: "communication", Subdomain: "mail", Containers: []string{"polyon-mail"}, BaseStatus: "active", Details: "Stalwart Mail Server.", BackendURL: "http://polyon-mail:8080"},
	{ID: "calendar", Name: "Calendar", Desc: "CalDAV · 일정 관리 · 리소스 예약", Base: "Stalwart (CalDAV)", Icon: "calendar", Category: "communication", Subdomain: "cal", Containers: []string{"polyon-mail"}, BaseStatus: "available", Details: "Stalwart 내장 CalDAV.", BackendURL: "http://polyon-mail:8080"},
	{ID: "contacts", Name: "Contacts", Desc: "CardDAV · 주소록 · AD 연동", Base: "Stalwart (CardDAV)", Icon: "userMultiple", Category: "communication", Subdomain: "contacts", Containers: []string{"polyon-mail"}, BaseStatus: "available", Details: "Stalwart 내장 CardDAV.", BackendURL: "http://polyon-mail:8080"},
	{ID: "chat", Name: "Chat", Desc: "팀 메신저 · 채널 · 화상회의", Base: "Rocket.Chat", Icon: "chat", Category: "communication", Subdomain: "chat", Containers: []string{"app-rocketchat", "app-rocketchat-mongo"}, BaseStatus: "available", Details: "Rocket.Chat 기반 팀 메신저.", BackendURL: "http://app-rocketchat:3000"},
	{ID: "board", Name: "Board", Desc: "공지사항 · 게시판 · 사내 소식", Base: "Custom", Icon: "bulletin", Category: "communication", Subdomain: "board", Containers: []string{"app-board"}, BaseStatus: "coming-soon", Details: "사내 공지, 부서 게시판.", BackendURL: "http://app-board:3000"},
	{ID: "kakao-alimtalk", Name: "카카오 알림톡", Desc: "알림톡 발송 · 템플릿 관리", Base: "Kakao Biz API", Icon: "chat", Category: "communication", Subdomain: "", Containers: []string{}, BaseStatus: "available", Details: "카카오 비즈니스 채널 기반 알림톡.", BackendURL: ""},
	{ID: "sms", Name: "SMS", Desc: "문자 발송 · 2FA 인증", Base: "CoolSMS", Icon: "mobile", Category: "communication", Subdomain: "", Containers: []string{}, BaseStatus: "available", Details: "SMS/LMS/MMS 발송.", BackendURL: ""},
	{ID: "drive", Name: "Drive", Desc: "파일 스토리지 · 팀 폴더 · 문서 공유", Base: "Nextcloud", Icon: "folder", Category: "productivity", Subdomain: "drive", Containers: []string{"polyon-nextcloud"}, BaseStatus: "available", Details: "Nextcloud 기반 파일 스토리지. RustFS S3 연동.", BackendURL: "http://polyon-nextcloud:80"},
	{ID: "documents", Name: "Documents", Desc: "파일 관리 · 공동편집 · WebDAV", Base: "Nextcloud+ONLYOFFICE", Icon: "document", Category: "productivity", Subdomain: "docs", Containers: []string{"app-nextcloud", "app-onlyoffice"}, BaseStatus: "available", Details: "Nextcloud + ONLYOFFICE.", BackendURL: "http://app-nextcloud:80"},
	{ID: "note", Name: "Note", Desc: "메모 · 스티키노트 · 빠른 공유", Base: "HedgeDoc", Icon: "edit", Category: "productivity", Subdomain: "note", Containers: []string{"app-hedgedoc"}, BaseStatus: "available", Details: "실시간 협업 마크다운 메모.", BackendURL: "http://app-hedgedoc:3000"},
	{ID: "forms", Name: "Forms", Desc: "설문 · 양식 · 피드백 수집", Base: "Formbricks", Icon: "list", Category: "productivity", Subdomain: "forms", Containers: []string{"app-formbricks"}, BaseStatus: "available", Details: "드래그앤드롭 설문 빌더.", BackendURL: "http://app-formbricks:3000"},
	{ID: "wiki", Name: "Wiki", Desc: "지식 관리 · 화이트보드 · 블록 에디터", Base: "AFFiNE", Icon: "notebook", Category: "productivity", Subdomain: "wiki", Containers: []string{"app-affine"}, BaseStatus: "available", Details: "AFFiNE 기반 위키.", BackendURL: "http://app-affine:3010"},
	{ID: "hr", Name: "HR", Desc: "인사 관리 · 조직도 · 근태", Base: "OrangeHRM", Icon: "userMultiple", Category: "business", Subdomain: "hr", Containers: []string{"app-hr"}, BaseStatus: "coming-soon", Details: "인사 관리 시스템.", BackendURL: "http://app-hr:80"},
	{ID: "approval", Name: "Approval", Desc: "전자 결재 · 워크플로우", Base: "Custom (Polaris)", Icon: "checkmark", Category: "business", Subdomain: "approval", Containers: []string{"app-approval"}, BaseStatus: "coming-soon", Details: "전자 결재 시스템.", BackendURL: "http://app-approval:3000"},
	{ID: "bpmn", Name: "BPMN Engine", Desc: "비즈니스 프로세스 · 전자결재 · 워크플로우", Base: "Operaton", Icon: "flow", Category: "platform", Subdomain: "bpmn", Containers: []string{"polyon-operaton"}, BaseStatus: "available", Details: "Operaton (Camunda 7 CE 포크) 기반 BPMN 2.0 프로세스 엔진. Apache 2.0 라이선스.", BackendURL: "http://polyon-operaton:8080"},
	{ID: "automation", Name: "Automation", Desc: "워크플로우 자동화 · 앱 연동 · 이벤트 트리거", Base: "n8n", Icon: "flow", Category: "platform", Subdomain: "auto", Containers: []string{"polyon-n8n"}, BaseStatus: "available", Details: "n8n 기반 노코드/로우코드 자동화. 앱 간 이벤트 연결.", BackendURL: "http://polyon-n8n:5678"},
	{ID: "workstream", Name: "Workstream", Desc: "업무 관리 · 타임라인 · 컨텍스트 엔진", Base: "Custom", Icon: "reportData", Category: "intelligence", Subdomain: "ws", Containers: []string{"app-workstream"}, BaseStatus: "coming-soon", Details: "업무 중심 그룹웨어.", BackendURL: "http://app-workstream:3000"},
	{ID: "ai-agent", Name: "AI Agent", Desc: "LLM 기반 업무 지원 · 자동화", Base: "OpenClaw/Ollama", Icon: "bot", Category: "intelligence", Subdomain: "ai", Containers: []string{"app-ai-agent"}, BaseStatus: "coming-soon", Details: "AI 기반 업무 어시스턴트.", BackendURL: "http://app-ai-agent:3000"},
	{ID: "homepage", Name: "Homepage", Desc: "웹사이트 빌더 · CMS · Git 배포", Base: "Strapi + Next.js", Icon: "applicationWeb", Category: "productivity", Subdomain: "", Containers: []string{"polyon-strapi", "polyon-gitea"}, BaseStatus: "active", Details: "CMS 또는 Git으로 홈페이지를 만들고 배포합니다.", BackendURL: "http://polyon-strapi:1337"},
	{ID: "erpengine", Name: "PolyON ERPEngine", Desc: "ERP · HR · 회계 · 비즈니스 관리 엔진", Base: "Odoo", Icon: "Enterprise", Category: "business", Subdomain: "erp", Containers: []string{"polyon-erpengine"}, BaseStatus: "active", Details: "검증된 ERP 엔진. Odoo 19 기반. HR, 회계, 프로젝트 관리.", BackendURL: "http://polyon-erpengine:8069"},
}

// migrateApps creates the polyon_apps table and seeds the initial catalog.
// Called from migrate() during Store init.
func (s *Store) migrateApps() {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create table (note: "desc" is quoted because it is a reserved SQL keyword)
	_, err := s.pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS polyon_apps (
			id          TEXT PRIMARY KEY,
			name        TEXT NOT NULL,
			"desc"      TEXT NOT NULL DEFAULT '',
			base        TEXT NOT NULL DEFAULT '',
			icon        TEXT NOT NULL DEFAULT '',
			category    TEXT NOT NULL DEFAULT '',
			subdomain   TEXT NOT NULL DEFAULT '',
			containers  TEXT[] NOT NULL DEFAULT '{}',
			base_status TEXT NOT NULL DEFAULT 'available',
			details     TEXT NOT NULL DEFAULT '',
			created_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at  TIMESTAMPTZ NOT NULL DEFAULT NOW()
		)
	`)
	if err != nil {
		log.Warn().Err(err).Msg("migrateApps: table creation failed")
		return
	}

	// Migration: add backend_url column (idempotent)
	_, _ = s.pool.Exec(ctx, `ALTER TABLE polyon_apps ADD COLUMN IF NOT EXISTS backend_url TEXT NOT NULL DEFAULT ''`)

	// Seed initial catalog (ON CONFLICT DO NOTHING — preserves any manual edits)
	for _, app := range seedApps {
		containers := app.Containers
		if containers == nil {
			containers = []string{}
		}
		_, err := s.pool.Exec(ctx, `
			INSERT INTO polyon_apps (id, name, "desc", base, icon, category, subdomain, containers, base_status, details, backend_url)
			VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)
			ON CONFLICT (id) DO NOTHING
		`, app.ID, app.Name, app.Desc, app.Base, app.Icon,
			app.Category, app.Subdomain, containers,
			app.BaseStatus, app.Details, app.BackendURL)
		if err != nil {
			log.Warn().Err(err).Str("app", app.ID).Msg("migrateApps: seed insert failed")
		}
	}
	log.Debug().Msg("migrateApps: polyon_apps table ready")
}

// ListApps returns all apps from the DB, ordered by category then name.
func (s *Store) ListApps(ctx context.Context) ([]AppMeta, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, name, "desc", base, icon, category, subdomain, containers, base_status, details,
		       COALESCE(backend_url, '') as backend_url,
		       COALESCE(access_mode, 'subdomain') as access_mode,
		       COALESCE(path_prefix, '') as path_prefix
		FROM polyon_apps
		ORDER BY category, name
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var apps []AppMeta
	for rows.Next() {
		var a AppMeta
		if err := rows.Scan(
			&a.ID, &a.Name, &a.Desc, &a.Base, &a.Icon,
			&a.Category, &a.Subdomain, &a.Containers,
			&a.BaseStatus, &a.Details, &a.BackendURL,
			&a.AccessMode, &a.PathPrefix,
		); err != nil {
			return nil, err
		}
		if a.Containers == nil {
			a.Containers = []string{}
		}
		apps = append(apps, a)
	}
	return apps, rows.Err()
}

// GetApp returns a single app by ID from the DB.
func (s *Store) GetApp(ctx context.Context, id string) (*AppMeta, error) {
	var a AppMeta
	err := s.pool.QueryRow(ctx, `
		SELECT id, name, "desc", base, icon, category, subdomain, containers, base_status, details,
		       COALESCE(backend_url, '') as backend_url,
		       COALESCE(access_mode, 'subdomain') as access_mode,
		       COALESCE(path_prefix, '') as path_prefix
		FROM polyon_apps
		WHERE id = $1
	`, id).Scan(
		&a.ID, &a.Name, &a.Desc, &a.Base, &a.Icon,
		&a.Category, &a.Subdomain, &a.Containers,
		&a.BaseStatus, &a.Details, &a.BackendURL,
		&a.AccessMode, &a.PathPrefix,
	)
	if err != nil {
		return nil, err
	}
	if a.Containers == nil {
		a.Containers = []string{}
	}
	return &a, nil
}
