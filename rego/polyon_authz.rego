package polyon.authz

import rego.v1

# ── 기본값 ────────────────────────────────────────────────────────────────────
default allow := false
default role := "member"

# ── 역할 결정 ──────────────────────────────────────────────────────────────────
# input.roles: Keycloak JWT에서 전달된 역할 목록 (문자열 배열)
# input.client: JWT azp 클레임 (polyon-console = Console 관리자)
role := "superadmin" if "superadmin" in input.roles

# polyon-console 클라이언트 토큰은 Console 관리자 → superadmin fallback
role := "superadmin" if {
	input.client == "polyon-console"
	not "member" in input.roles
	not "operator" in input.roles
}

role := "admin" if {
	"admin" in input.roles
	not "superadmin" in input.roles
}

role := "operator" if {
	"operator" in input.roles
	not "admin" in input.roles
	not "superadmin" in input.roles
}

# ── superadmin: 모든 허용 ──────────────────────────────────────────────────────
allow if role == "superadmin"

# ── admin 허용 경로 ────────────────────────────────────────────────────────────
admin_paths := {
	"/users",
	"/groups",
	"/ous",
	"/computers",
	"/apps",
	"/modules",
	"/mail",
	"/mail-proxy",
	"/appengine",
	"/directory",
	"/auth",
	"/credentials",
	"/account",
	"/dns",
	"/domain",
	"/sites",
	"/homepage",
	"/drive",
	"/wiki",
	"/chat",
	"/automation",
	"/bpmn",
	"/engines",
	"/strapi",
}

allow if {
	role == "admin"
	some prefix in admin_paths
	startswith(input.path, prefix)
}

# ── operator 허용 ──────────────────────────────────────────────────────────────
# GET 요청만 허용 + 특정 POST (재시작 등)
allow if {
	role == "operator"
	input.method == "GET"
}

allow if {
	role == "operator"
	input.method == "POST"
	some suffix in {"/restart", "/reload", "/sync", "/test"}
	endswith(input.path, suffix)
}

# ── member 허용 ────────────────────────────────────────────────────────────────
member_paths := {"/account/me", "/portal", "/apps"}

allow if {
	role == "member"
	input.method == "GET"
	some prefix in member_paths
	startswith(input.path, prefix)
}

allow if {
	role == "member"
	input.method == "PUT"
	input.path == "/account/me"
}

# ── policy/status는 모두 허용 (헬스체크) ──────────────────────────────────────
allow if input.path == "/policy/status"

# ── 디버그용: 현재 역할 노출 ──────────────────────────────────────────────────
current_role := role
