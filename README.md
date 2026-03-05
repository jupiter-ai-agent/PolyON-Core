# polyon-core

HELIOS Core API 서버 (Go/Chi).  
사용자·그룹·서비스·메일·모니터링 등 비즈니스 로직 REST API를 제공합니다.

---

## 역할

- **사용자·그룹 관리**: Keycloak + Samba AD DC LDAP 연동
- **서비스 제어**: Docker 컨테이너 상태 조회·제어
- **메일 프록시**: Stalwart Mail Admin API 프록시
- **Elasticsearch 프록시**: 검색 API 프록시
- **모니터링**: Prometheus 메트릭 조회
- **도메인·DNS**: 도메인 설정 관리
- **알림**: 시스템 알림 발송

---

## 빌드

```bash
cd core-go
go build -o polyon-core ./cmd/polyon/...
```

### Docker 이미지

```bash
docker buildx build --platform linux/amd64,linux/arm64 \
  -t jupitertriangles/polyon-core:202602 \
  ./core-go
```

---

## 실행

```bash
# 환경변수는 Operator가 런타임 볼륨(.env)에 생성
cd core-go
go run ./cmd/polyon/...
```

---

## 주요 API 엔드포인트

| 경로 | 설명 |
|------|------|
| `GET /health` | 헬스체크 |
| `GET /api/users` | 사용자 목록 |
| `POST /api/users` | 사용자 생성 |
| `PUT /api/users/:id` | 사용자 수정 |
| `DELETE /api/users/:id` | 사용자 삭제 |
| `GET /api/groups` | 그룹 목록 |
| `POST /api/groups` | 그룹 생성 |
| `GET /api/containers` | 컨테이너 목록 및 상태 |
| `POST /api/containers/:name/restart` | 컨테이너 재시작 |
| `GET /api/mail/*` | Stalwart Mail API 프록시 |
| `GET /api/es/*` | Elasticsearch 프록시 |
| `GET /api/monitor/*` | Prometheus 메트릭 |
| `GET /api/domain` | 도메인 설정 조회 |
| `POST /api/setup/*` | 초기 설정 실행 |

---

## 디렉토리 구조

```
core-go/
├── cmd/polyon/         — 진입점 (main.go)
├── internal/
│   ├── api/            — 라우터 및 핸들러
│   ├── auth/           — JWT 인증 미들웨어
│   ├── config/         — 환경변수 설정
│   ├── docker/         — Docker 클라이언트
│   ├── httputil/       — HTTP 유틸리티
│   ├── ldap/           — LDAP 클라이언트 (Samba AD DC)
│   ├── monitor/        — Prometheus 연동
│   ├── notify/         — 알림 발송
│   ├── provision/      — 서비스 프로비저닝
│   ├── proxy/          — HTTP 리버스 프록시
│   ├── samba/          — Samba AD DC 관리
│   ├── server/         — Chi HTTP 서버
│   └── store/          — 데이터 영속성
├── migrations/         — DB 마이그레이션
├── setup-runner/       — 초기 설정 실행기
└── Dockerfile
```
