# 구현·배포 계획 v0

> [[team-workbench-spec-v1]]을 실제로 만들고 배포하는 방법. 스펙이 "무엇"이면 이 문서는 "어떻게·무엇으로·어떤 순서로".
> 전제: 1인 + AI 코딩. 첫 12주는 **데몬을 끝까지**, 서버는 우리 dogfood용 최소. 웹 UI는 12주 밖.
> 원칙: 더할 게 없을 때가 아니라 뺄 게 없을 때 끝난다. 아래 표에 없는 것은 거절한다.

## 0. 산출물 한눈에

| 산출물 | 형태 | 대상 | 라이선스 |
|---|---|---|---|
| `spec/` | JSON Schema(지속 객체·Change·Session·Finding·이벤트) + OpenAPI(서버 API) + 훅/MCP 계약 | 누구나 | Apache-2.0 |
| `kb` | 단일 바이너리: 데몬 · CLI · TUI · 로컬 MCP · 훅 엔드포인트 · 헤드리스 | 개인 개발자·CI | Apache-2.0 |
| `kb-server` | 단일 바이너리(UI embed): 원장 · sync · GitHub App · 웹훅 · 웹 UI | 팀(셀프호스트) | 소스 공개(FSL/BSL 계열, 4년 후 Apache) |
| 호스팅 | 같은 `kb-server`, 멀티테넌트 | 팀(설치 없음) | 상용 |
| `action` | GitHub Action(`kb verify --changed`, 영향 코멘트) | 리포 | Apache-2.0 |
| 어댑터 | 도구별 규칙 파일·훅 어댑터(데몬 내장, 별도 버전) | — | Apache-2.0 |
| 배포 키트 | Docker 이미지·compose·Terraform 모듈(ECS/EC2)·(후속) Helm | 셀프호스트 | Apache-2.0 |

원칙: 개인이 쓰는 것은 전부 열고, 팀이 돈 내는 것(서버·호스팅)만 닫는다. 형식은 어떤 경우에도 연다.

---

## 1. 모노레포 구조

```
kb/
├─ spec/                     # 공개 계약. 객체 스키마는 Go 구조체에서 생성해 여기 publish, OpenAPI는 손 작성(design-first)
│  ├─ schema/*.json          # Intent, Constraint, Judgment, Decision, Outcome, Change, Session, Finding, Work, Actor, Event
│  ├─ openapi.yaml           # kb-server API
│  ├─ hooks.md               # 훅 계약(도구별 매핑은 adapters/)
│  └─ mcp.md                 # MCP 리소스·도구 계약
├─ cmd/
│  ├─ kb/                    # 데몬+CLI+TUI 진입점
│  └─ kb-server/
├─ internal/                 # Go 패키지 경계 = 바운디드 컨텍스트(Harness/Accountability/Realization/Supply/Work). import lint로 강제
│  ├─ core/                  # 애그리게이트+커맨드 핸들러(Change·Constraint·Scope·Session)·범위 해석 (순수 로직, IO 없음). 이벤트는 여기서만 생성
│  ├─ ledger/                # append-only 이벤트 + 머클 + 체크포인트 + 증명 (로컬 SQLite / 서버 Postgres 공용 인터페이스)
│  ├─ anchor/                # 닻 파서·해석기(tree-sitter) · 3층 해시 · AnchorMoved
│  ├─ context/               # (Supply) 범위 합성 · 렌더러(md/MCP/웹) · 가져오기(리포·파일 감시·웹) · 사이드카 manifest
│  ├─ session/               # 훅 이벤트 수신 · 턴 분류 · 세션 요약 · Change 도출
│  ├─ watch/                 # finding 생성기(원천별)
│  ├─ graph/                 # 그래프 투영(엣지 테이블·재귀 CTE) · MCP 질의(related/path/why/what_touches)
│  ├─ work/                  # dispatch
│  ├─ sync/                  # 데몬↔서버 커서 동기화
│  ├─ mcp/                   # 로컬 MCP 서버
│  ├─ github/                # App 웹훅 · PR 코멘트 · 체크
│  ├─ ai/                    # 프로바이더 인터페이스 + 폴백 (anthropic, openai, local)
│  └─ tui/                   # bubbletea 화면
├─ adapters/                 # 도구별: claude-code/, cursor/, agents-md/, copilot/, gemini/, openclaw/(SOUL.md·AGENTS.md 개인 층) — 각자 VERSION
├─ dispatch/                 # Work 대상: claude-code(로컬), grok-bot·openclaw(채널/API), 승인 브로커 클라이언트 예제
├─ web/                      # React UI (Vite). 빌드 산출을 kb-server에 embed
├─ action/                   # GitHub Action (composite, kb 바이너리 호출)
├─ deploy/
│  ├─ docker/                # Dockerfile(kb, kb-server), compose.yaml
│  ├─ terraform/             # ECS Fargate + RDS 모듈, EC2 단일 노드 모듈
│  └─ install.sh             # curl | sh (릴리스 바이너리)
├─ sdk/                      # (v1) ts/, python/ — 훅 없는 도구·CI에서 kb API 호출용
└─ docs/                     # 우리 리포 자신의 ADR/RULE — kb로 관리(dogfood)
```

코드 생성(하이브리드): **객체 타입의 원천은 Go 구조체**(`internal/core`) → JSON Schema 생성(invopop/jsonschema류) → `spec/schema` publish → TS 타입 생성(UI·v1 SDK). Python SDK는 v1. `openapi.yaml`은 손 작성 → Go 서버 스텁·클라이언트(`oapi-codegen`)·TS 클라이언트. CI: 생성물이 커밋본과 다르면 실패. "코드가 진실, 공개 계약은 design-first".

---

## 2. 기술 선택 (확정)

| 영역 | 선택 | 비고 |
|---|---|---|
| 언어 | Go 1.2x | 데몬·서버 단일 바이너리, 크로스 컴파일 |
| CLI | cobra. 표 출력은 lipgloss 최소 | TUI(bubbletea)는 v1 |
| HTTP | net/http + chi | 서버·데몬 로컬 API |
| 로컬 DB | SQLite(modernc, CGO 없음) | 데몬 원장·개인 층·캐시. WAL |
| 서버 DB | Postgres 16 (pgx + sqlc, goose) | JSONB·파티션·RLS(테넌트) |
| 닻 해석 | tree-sitter **wasm 문법 + wazero**(CGO 없음) — v0는 **TypeScript/TSX만**. Python·Go는 generic(파일 해시) → v1 | 파일 단위 게으른 파싱 + 캐시. 심볼 인덱스 영속화 없음 |
| 머클/증명 | transparency-dev/merkle (rfc6962) — **서버 원장만** | 데몬 원장은 해시 체인 append-only(증명 불필요). 체크포인트 서명 ed25519 |
| 서명 | ed25519. 사람은 OIDC 키리스(sigstore 방식, v1) / v0는 서버 발급 사용자 키 | 에이전트 키는 데몬이 owner 아래 발급 |
| MCP | 공식 Go SDK(또는 mcp-go) stdio + (v1) streamable HTTP | 도구는 stdio로 로컬 데몬에 |
| AI | v0는 **폴백만 구현**, 프로바이더는 인터페이스만. 이유 초안은 사람이 씀 | 프로바이더 구현(anthropic/openai-compatible/ollama)은 v1. BYOK |
| UI | React + Vite + TS + Tailwind + react-flow + elk | kb-server embed |
| GitHub | go-github + App 인증(JWT→installation token) | 웹훅: pull_request, push, deployment_status, check_run |
| 관측(우리) | OTel SDK → OTLP → Grafana Cloud(무료 티어) | dogfood |
| 릴리스 | goreleaser + cosign 서명 + SBOM(syft) | 바이너리·이미지 모두 |
| CI | GitHub Actions | lint(golangci, import-boundary), 스펙→코드 생성 검증, 테스트, e2e(데몬+Claude Code 훅 시뮬레이터) |

검증 필요(구현 직전): Claude Code 훅 이벤트 이름·입출력 형식·settings 경로, Cursor 훅/규칙 규약, MCP Go SDK 안정 버전. 어댑터 `VERSION`에 확인 날짜 기록.

---

## 3. 데몬 `kb` 상세

### 3.1 프로세스 모델
- `kb daemon`: 사용자당 1개, 로그인 시 자동 시작(launchd/systemd user unit/Windows 서비스 — v0는 macOS·Linux). Unix 소켓 `~/.kb/kb.sock` + 로컬 HTTP(127.0.0.1, 랜덤 포트, 토큰).
- `kb hook <event>`: 도구 훅이 실행하는 초경량 명령. stdin(JSON) → 소켓으로 전달 → 응답(stdout JSON: 컨텍스트 주입/경고/차단). 목표 지연 < 50ms(데몬 캐시 히트).
- `kb mcp`: stdio MCP 서버. 도구가 spawn. 내부적으로 소켓으로 데몬에 질의(데몬이 단일 진실).
- `kb inbox` / `kb history <anchor>` / `kb preview --tool`: 표 출력 CLI. TUI는 v1.
- `kb --headless`: 서비스 없이 CI에서 1회 실행(verify, 영향 계산, Change 도출).

### 3.2 디렉터리
```
~/.kb/
  config.toml           # 서버 URL·토큰, 프로바이더, 공유 기본값
  keys/                 # 사용자 키, 에이전트 키(owner=사용자)
  ledger.db             # 개인 층 원장(SQLite) — 세션·판단·개인 제약
  cache/                # 팀 층 sync 사본, 해석기 캐시
<repo>/.kb/
  manifest.json         # 관리 파일·해시·출처·어댑터 버전
  context/<tool>/...    # 생성 본문(gitignore)
  kb.toml               # 리포 설정(범위 키, 어댑터 on/off)
```

### 3.3 훅 파이프라인 (핵심 경로)
1. 도구 훅 → `kb hook pre_edit {file, range?, tool, session_id}`
2. 데몬: 파일→닻 후보(캐시된 심볼 인덱스) → 범위 해석(리포·경로·개인) → 해당 닻에 묶인 제약·결정·닻 이력 조회
3. 응답: L0 = `{inject: "<한 문단 컨텍스트>", warn?: [...]}`; 자율 제약이 차단을 명시하면 `{block: true, reason}`. **fail-open**: 데몬 부재·캐시 미스·50ms 초과 시 주입 생략 + 로그, 편집은 막지 않는다
4. `kb hook post_edit` → 편집 사실 기록(닻·해시), 턴 분류 후보 갱신
5. `kb hook session_end` → 요약(폴백: 제목·닻·판단 목록 / 옵션: AI 요약) → 개인 원장에 Session 이벤트
6. `git` 훅(post-commit/pre-push): Change 도출(커밋 경계·닻 군집) → 영향 계산 → 서명 → 개인 원장·(연결 시) 서버 push. 서버 클론 계산 없음
7. (v1) 파일 감시: 리포 md 직접 편집 → 가져오기 제안 → 재렌더. v0는 git 훅 + 명시적 `kb import`

### 3.4 턴 분류
- 폴백 규칙(v0 기본): 편집 취소·`git checkout/restore`·리셋 = revert / 부정어·대체 지시 패턴("아니","말고","대신","instead","no,") 후 재편집 = redirect / 그 외 accept.
- (v1) 프로바이더 분류(로컬 모델 권장). 원문은 프로바이더에만 가고 저장하지 않음.
- 측정: dogfood 3개월 동안 수동 라벨 표본으로 재현율 기록.

### 3.5 어댑터 인터페이스
```go
type Adapter interface {
  Name() string; Version() string
  Detect(repo) ([]File, error)
  Parse(File) ([]RuleDoc, error)
  Render(resolved Context) ([]File, error)
  Hooks() HookSpec   // 훅 설치 방법·이벤트 매핑 (없으면 nil)
}
```
`kb init`이 어댑터 Detect → 흡수 → Render → 훅 설치(사용자 확인 후 settings 수정, 백업 보관).

### 3.5b 벤더 의존의 정직한 표기
편집 전 훅은 v0에서 Claude Code에서만 확인된 경로다. 따라서 "루프 내 개입"은 v0에서 사실상 Claude Code 전용이며, Cursor 어댑터는 12주 밖(v1, 렌더·흡수만)이다. 같은 주(6–7)에 **훅 없는 경로**를 만든다: 로컬 MCP 도구 `what_touches(anchor)`·`related(id)`를 노출하고, 팀 스킬/규칙에 "편집 전 what_touches 호출"을 넣어 훅 없는 도구에서도 에이전트가 스스로 제약을 조회하게 한다. git 신호(commit·reflog)는 도구 무관.

### 3.6 설치·동기화 원칙 (전역 vs 리포)
- **`kb setup`(전역, 1회)**: 변경 목록을 보여 주고 항목별 승인 후 적용. 원본은 `~/.kb/backup/`. 항목: 도구 훅 등록(우리 항목만 추가, 기존 보존) / MCP 서버 등록 / 전역 규칙(`~/.claude/CLAUDE.md` 등) **흡수는 무조건, 되쓰기(포인터 한 줄)는 선택** / 개인 스킬 디렉터리 인덱싱(읽기 전용) / 데몬 자동 시작(선택).
- 사용자 파일이 원천: 사용자가 전역 규칙을 직접 고치면 데몬이 감지해 개인 층 갱신. 우리는 덮어쓰지 않는다. 훅·MCP 등록 상태는 도구 실행 시 확인하고 사라졌으면 알림만.
- **관찰 모드(자동)**: 초기화 안 된 리포에서도 전역 훅이 동작 — 닻·편집·판단을 개인 원장에만 기록, 리포에 쓰기 없음, 주입은 개인 층(전역 규칙·닻 이력)만. 설치만으로 닻 이력 가치가 생긴다.
- **활성 모드(`kb init`)**: 리포 규칙 파일·ADR·경로별 규칙 흡수, `.kb/` 사이드카·생성 본문, 프로젝트 규칙 파일에 포인터/managed 블록(승인). 팀 서버 연결 선택.
- **`kb uninstall`**: 진입점(훅·MCP) 제거 + 백업 복원 + `.kb/`·managed 블록 제거 → 원상태 보장. 신뢰의 전제.

---

## 4. 서버 `kb-server` 상세

### 4.1 구성
단일 바이너리. 고루틴 워커: 웹훅 처리, 영향 계산, 체크포인트(주기), sync 스트림, 결과 창 타이머. 상태는 Postgres에만.
```
kb-server serve --db postgres://... --github-app-id ... --github-private-key ... --oidc-issuer ...
```

### 4.2 테넌시
- `org` = 테넌트. 모든 테이블 `org_id` + RLS(세션 변수 `app.org_id`). 호스팅·셀프호스트 동일 스키마(셀프호스트는 org 1개).
- 사용자 ↔ org 매핑은 GitHub App 설치(installation ↔ org)로 시작. OIDC 로그인(GitHub 먼저, Google/자체 IdP 후속).

### 4.3 원장
```sql
event(org_id, seq bigserial, ts, type, actor jsonb, subject jsonb, meta_hash, body jsonb, body_hash, leaf_hash, sig)
checkpoint(org_id, tree_size, root_hash, ts, sig, external_ref jsonb)  -- TSA/증인 결과
```
- leaf = H(meta || body_hash). 머클 트리는 tree_size 기준 재구성 가능(캐시 테이블 `merkle_node`).
- 체크포인트: 10분 또는 1,000 이벤트마다. 서버 키 서명. 플러그인 인터페이스 `Anchor(checkpoint) -> external_ref` (v0: 없음, v1: TSA).
- 증명 API: `GET /ledger/proof/inclusion?seq=`, `GET /ledger/proof/consistency?from=&to=`.
- 투영 테이블(재투영 가능): `constraint_current`, `change_current`, `finding_open`, `anchor_history`, `scope_index`.

### 4.4 sync 프로토콜
- 데몬 → 서버: `POST /sync/push {cursor, events[]}` — 공유 선택된 이벤트만, 데몬 서명 포함. 서버는 서명 검증 후 원장에 append(원 서명 보존).
- 서버 → 데몬: `GET /sync/pull?since=<cursor>&scopes=...` 롱폴 또는 SSE. 팀·제품 층 제약·결정·닻 이력 사본.
- 서버 append 시 `core` 커맨드 핸들러로 재검증 → 확정 또는 `Rejected` 이벤트. 데몬 이벤트는 제안, 서버 seq가 전역 순서. 커서는 `(org_id, seq)`.
- **v0 단순화**: 데몬 push는 커밋·공유 시점에만(상시 스트림 없음). pull은 팀·제품 제약 사본만. Rejected는 로그로 남기고 로컬 정정은 "다음 pull에서 서버 사본으로 덮어쓰기". 정교한 투영 정정은 충돌이 실제 관측된 뒤(v1).
- 데몬 투영은 메모리 + 단순 테이블로 제한. 서버 스키마를 흉내 내지 않는다(두 DB·두 쿼리 세트 억제).

### 4.5 GitHub App
- 권한: contents(read), pull_requests(write), checks(write), deployments(read), metadata.
- PR opened/synchronize: 영향은 이미 데몬이 push 시 올려 둔 서명 결과를 사용 → 코멘트·체크. 없으면 Action(헤드리스 데몬)이 계산해 `POST /changes/{id}/impact`. 서버 클론 계산은 없음.(영향·세션 판단 요약·이유 초안) + Check(제약 위반 시 neutral/failure는 리포 설정).
- PR review submitted: 이유 확인 → `Judged`.
- merged + deployment_status(success): `Deployed` → 결과 창 타이머 → 오류율 웹훅(Sentry/Datadog generic webhook) 수신 시 `OutcomeRecorded`.

### 4.6 API 표면(v0, OpenAPI)
`/auth/*`, `/sync/*`, `/orgs/{org}/scopes`, `/constraints`, `/decisions`, `/changes`, `/changes/{id}/judgments`, `/findings`, `/work`, `/anchors/{anchor}/history`, `/context/resolve?repo&path&tool`, **`/gates`, `/gates/{id}/resolve`(승인 브로커, v0)**, `/graph/related|path|why|what_touches`, `/discussions`, `/docs/{id}` (웹 편집=커맨드), `/export/md`, `/ledger/proof/*`, `/audit/export`(v1), `/webhooks/github`, `/webhooks/outcome`.

---

## 5. 웹 UI
- 라우트: `/inbox`, `/changes/:id`, `/anchors/:anchor`, `/context/preview`, `/harness`, `/docs/:id`(읽기·v1 편집·Discussion), `/graph`(투영 뷰).
- 상태: TanStack Query + 생성된 TS 클라이언트. 실시간은 SSE(`/events/stream`).
- 문법 고정(색=범위, 테두리=상태, 점선=로컬/미검증)을 디자인 토큰으로 한 파일에.
- 빌드 산출을 `kb-server`에 `embed` → 이미지 1장.

---

## 6. 배포

### 6.1 개인(kb)
- `curl -fsSL https://<domain>/install.sh | sh` 또는 `brew install <tap>/kb`, `go install`. GitHub Releases에 macOS(arm64/amd64)·Linux 바이너리, cosign 서명·SBOM.
- 설치 → `kb setup`(전역 승인) → 관찰 모드 즉시 동작 → 리포별 `kb init`(선택). 업데이트는 `kb update`(릴리스 확인, 서명 검증). `kb uninstall`로 완전 복원.

### 6.2 팀 셀프호스트
- `ghcr.io/<org>/kb-server:<ver>` + `compose.yaml`(kb-server + postgres + 볼륨). 5분 설치 목표.
- Terraform 모듈: ECS Fargate(kb-server 1태스크) + RDS Postgres + ALB + Secrets Manager(App 키) / EC2 단일 노드 + Docker.
- Helm 차트는 요청 있을 때.

### 6.3 호스팅(우리 운영)
- AWS ap-northeast-2. ECS Fargate(kb-server ×2, 무상태) + RDS Postgres(Multi-AZ) + ALB + WAF + Secrets Manager + S3(리포 클론 캐시 아님 — 임시 EFS) + CloudWatch/OTLP→Grafana.
- 테넌트 격리 RLS. 백업: RDS 스냅샷 일 1회 + PITR. 원장은 append-only라 복구 검증이 쉽다(체크포인트 재검증).
- 초기 비용: 월 $150~300 수준(Fargate 2×0.5vCPU, db.t4g.medium). 사용자 100팀까지 무변경.

### 6.4 릴리스·CI/CD
- 브랜치: main만. PR → CI(lint·생성 검증·테스트·e2e) → 머지 → goreleaser(태그) → 이미지·바이너리·Action 태그.
- 주간 릴리스. 어댑터는 독립 버전(도구 규약 변경 시 어댑터만 패치).
- 호스팅 배포: 태그 → ECS 롤링. 마이그레이션은 goose, 후방 호환만(원장 스키마는 add-only).

---

## 7. 보안
- 키: 사용자 키·에이전트 키는 `~/.kb/keys`(0600). 에이전트 키는 `owner`·만료·폐기 목록(서버 sync). 서버 체크포인트 키는 Secrets Manager/KMS(v1: KMS 서명).
- 토큰: 데몬↔서버는 OIDC 로그인 후 발급된 장기 refresh + 단기 access. CI 헤드리스는 org 범위 토큰.
- 데이터: 세션 원문은 디스크에 저장하지 않음(프로바이더 전송 시에도 일시). 서버는 body를 받되 meta/body 분리 해시. 저장 암호화(RDS·EBS), 전송 TLS.
- GitHub App 키는 서버에만. 리포 클론 캐시는 임시 디스크, 처리 후 삭제.
- 공급망: cosign 서명 검증을 `kb update`가 강제. 의존성 스캔(govulncheck) CI.
- 위협 모델 문서를 `docs/`에 kb로 관리(dogfood).

---

## 8. 개발 프로세스
- **Day 1부터 dogfood**: 이 리포의 ADR·RULE·CLAUDE.md를 kb로 관리, 우리 세션을 kb가 캡처. 3개월 명제 실험 데이터가 여기서 나온다.
- 명제 지표 수집: 주간 — 판단 수·이유 존재율·승격 수·에이전트 제안 수용률·회귀(되돌림) 수·루프 내 개입 수·턴 분류 재현율(수동 표본 20건).
- 스펙 우선: 데이터 모델 변경은 `spec/` PR로만. 코드 생성 결과 diff가 리뷰 대상.
- AI 코딩 규칙(우리 CLAUDE.md): 패키지 경계·원장 add-only·폴백 필수·어댑터 VERSION 갱신.

---

## 9. 12주 일정 (1인 + AI) — 데몬 완주, 서버 최소

| 주 | 목표 | 완료 기준 |
|---|---|---|
| 1–3 | Go 골격(학습 포함)·패키지 경계 lint·Go→JSON Schema 생성·OpenAPI 초안·해시 체인 원장(SQLite)·`core` 애그리게이트 뼈대 | 커맨드→이벤트→투영이 메모리에서 동작, 스키마 publish |
| 4–5 | 닻: TypeScript tree-sitter(wasm) + generic. 3층 해시. `verify` | 우리 리포에서 stale 오탐률 측정 시작 |
| 6–7 | Claude Code 어댑터 + 훅 파이프라인(fail-open, 50ms) + **훅 시뮬레이터(e2e)** + 로컬 MCP(`what_touches`·`related`) + 훅 없는 경로(스킬) | 편집 전 제약 주입 체감, 시뮬레이터로 회귀 테스트 |
| 8 | 세션 캡처·턴 분류 폴백·Change 도출(post-commit)·`inbox`/`history` CLI | 하루 작업 후 Change·판단이 CLI에 보임 |
| 9 | `kb init` 흡수·렌더·사이드카·`setup`/`uninstall` 복원 | 설치→제거 왕복이 원상태 |
| 10–11 | 서버 최소: Postgres 원장(머클·체크포인트)·증명 검증 CLI·sync(push@commit/share, pull 제약)·OIDC(GitHub)·**승인 브로커 `/gates`(정책 판정+기록)** | 둘 이상의 데몬이 팀 제약을 공유, 포함 증명 검증, 에이전트가 `/gates`로 묻고 답이 원장에 남음 |
| 12 | GitHub App PR 코멘트(영향·판단 기록) + install.sh·릴리스 파이프라인(goreleaser·cosign) + 외부 사용자 3명 | 외부 3명이 설치·훅 동작 확인 |

13–16주: 웹 인박스·Change 뷰 → Watch 3종 → Cursor 어댑터(렌더·흡수) → Python 닻 → `work dispatch` → 파일 감시 → 프로바이더 구현 → TUI.

12주에서 **뺀 것**(이유): 데몬 머클(증명은 서버만) / TUI(시간 흡수) / 파일 감시(오탐) / AI 프로바이더 구현(폴백으로 충분) / Cursor 어댑터(편집 전 훅 부재) / Python·Go tree-sitter(CGO·시간) / 웹 UI(개인 단계에 무의미) / Rejected 투영 정정(충돌 관측 전) / 서버 클론 계산(없음).

이름: `kb`는 충돌이 많다. 고유 이름을 4주차 전에 정한다(설치 스크립트·brew tap·MCP 서버 이름에 필요).

---

## 10. 기술 리스크와 대응
| 리스크 | 대응 |
|---|---|
| 도구 훅 규약 변동·부재 | 어댑터 격리·VERSION·e2e 시뮬레이터. 훅 없으면 git 신호 폴백 |
| 훅 지연이 편집 흐름을 방해 | 심볼 인덱스·제약 캐시를 데몬 메모리에, 50ms 예산, 초과 시 주입 생략(경고만) |
| 닻 해시 소음 | 3층 해시·review/stale·커밋 묶음. dogfood로 stale 오탐률 측정 |
| tree-sitter | wasm+wazero로 CGO 회피. v0는 TS만. 대형 리포는 게으른 파일 단위 파싱, 인덱스 영속화 없음 |
| 첫 가치가 Claude Code 훅에 종속 | §3.5b 명시. 훅 없는 경로(MCP 도구+스킬)를 같은 주에. Cursor는 렌더·흡수만 |
| 승인 중복(에이전트 자체 승인 UI와 우리 인박스) | 승인 브로커: 우리는 정책·기록, 답은 사용자의 채널에서. 인박스는 브로커가 ask로 남긴 것만 |
| 머클 구현 오류 = 신뢰 붕괴 | rfc6962 검증된 라이브러리, 증명 검증 CLI를 외부 공개, 퍼즈 테스트 |
| 1인 범위 초과 | 12주 표의 완료 기준 밖은 전부 거절. 어댑터 1개(Claude Code), tree-sitter 언어 1개(TS) 고정. "뺀 것" 목록이 기준 |
| Go 학습 곡선 | 1–2주차 골격을 AI와 함께, 코드 리뷰 규칙을 CLAUDE.md에 |
