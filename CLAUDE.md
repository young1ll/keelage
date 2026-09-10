# keelage — Claude Code 작업 규칙

이 리포는 `docs/spec/`의 스펙과 계획을 구현한다. 작업 전 다음 순서로 읽는다:
1. `docs/spec/implementation-plan-v0.md` §9 (12주 일정과 "뺀 것" 목록)
2. `docs/spec/architecture-patterns-v0.md` (코드 모양)
3. 관련 ADR (`docs/adr/`) — 스펙의 `kb`는 `keelage`로 읽는다(ADR 0009)

## 절대 규칙
- 12주 표의 완료 기준 밖 기능은 만들지 않는다. "뺀 것" 목록(계획서 §9)은 거절 사유다.
- `internal/core`는 표준 라이브러리만 import한다. 애그리게이트는 `Decide/Apply` 순수 함수. 저장소를 부르지 않는다.
- 패키지 의존 방향: cmd → app → core; adapter/* → port, core. adapter 간 import 금지.
- 원장은 append-only. 기존 이벤트 재작성 금지. 스키마 변경은 새 버전 + 업캐스터.
- 훅 경로는 fail-open. 데몬 부재·타임아웃이 사용자의 편집을 막아선 안 된다.
- 객체 타입의 원천은 Go 구조체. JSON Schema는 생성해 `spec/schema`에 publish. OpenAPI는 손으로 쓴다.
- 어댑터(`adapters/*`)는 각자 VERSION과 확인 날짜를 가진다. 도구 규약은 구현 직전 공식 문서로 재확인한다.
- 개인 데이터(세션 원문)는 디스크에 저장하지 않는다. 로그 금지 필드를 지킨다.
- 설계 결정이 바뀌면 ADR을 추가한다(`docs/adr/NNNN-title.md`, MADR). 스펙 문서를 직접 고치지 말고 ADR로 supersede한다.

## 코드 구조 (1–11주차 기준)
- `cmd/keelage`(데몬·CLI), `cmd/keelage-server` — 조립만. `tools/schemagen` — Go 구조체 → `spec/schema`.
- `internal/core` — 값 객체(ID·ActorRef·Level·ScopeKey·Anchor·Hash3), `Event/Command/Root[T]`, `Codec`(kind·version·업캐스터), `Envelope`(meta/body 분리 해시·체인), `DecideContext`.
  - `core/harness`: Constraint(닻 바인딩 포함)·Scope·Decision(ADR 흡수) 애그리게이트. `core/accountability`: Change·Gate·Session(턴·판단 후보, 원문 없음) 애그리게이트. `core/realization`: Syntax 모델·심볼 추출·Hash3·이동 감지·Anchor 애그리게이트(review/stale 2단계). `core/supply`: 정규 훅 이벤트 6종·`Compose`(사실→주입 문단·경고·차단)·턴 분류 폴백(`ClassifyPrompt`·`IsRevertCommand`)·순수 렌더러(`RenderClaudeCode`, managed 블록, 골든 `testdata/render/`)·사이드카 `Manifest`. 커맨드는 `XxxCmd`(ID·Idem)를 임베드한다.
- `internal/port` — Ledger(+`OriginLedger`: `FindOrigin`)·Clock·IDGen·Signer·Projector·SyntaxParser·HookService·ContextQuery·Commits·Authenticator·DaemonRegistry·SignatureVerifier·TokenIssuer·Membership·OAuthDevice, sync 객체(`PushRequest/Response`·`PullResponse`·`GateRequest/Resolution`·`DeviceStart/Poll`)와 `TeamService`(서버)/`TeamClient`(데몬). `port/ledgertest`는 모든 Ledger 구현이 통과해야 하는 계약 스위트.
- `internal/merkle` — rfc6962 리프(`H(meta_hash‖body_hash)`)·포함/일관성 증명·서명 체크포인트·오프라인 검증 번들(ADR 0004). 계층 밖의 잎 라이브러리(transparency-dev/merkle 래핑).
- `internal/app` — 커맨드 파이프라인(`Pipeline`, `Register[T]`), 메모리 투영(`ScopeIndex`·`ConstraintIndex`·`ChangeIndex`·`AnchorIndex`), `ProjectionContext`, `Resolver`(닻→Hash3, 캐시, generic 강등), `Verifier`, `Rebuild`/`Replay`, `Hooks`(HookService·ContextQuery: 사실 수집 + 세션 캡처 `WithCapture`), `SessionIndex`·`DecisionIndex`, `Deriver`(커밋→Change, ADR 0013), `Inbox`·`History`, `Importer`(규칙 파일·ADR 흡수)·`Renderer`(Plan/Apply, diverged)·`Uninit`(ADR 0014), 팀 서버(ADR 0015): `Orgs`/`Org`(org별 원장·투영·파이프라인)·`TeamServer`(sync 수신: origin 중복→체인·서명→공유 스트림→행위자→디코드→`ver-1` append, 거부는 `Rejected`; `/gates` 브로커; `GateIndex`)·`Login`(device flow)·데몬 쪽 `Syncer`(push 필터: person 축·미공유 세션 제외, pull→팀 캐시+투영)·`SyncState`.
- `internal/adapter/{memory,sqlite,ulid,uds,httpapi,git,treesitter,claudecode,mcp,keys,postgres,github,teamclient}` — 어댑터. 서로 import 금지(depguard); cmd가 배선한다. `keys`: ed25519 키(0600)·`Verifier`. `postgres`: 서버 원장(goose 임베드 마이그레이션, org RLS, `body TEXT`, 머클 노드·체크포인트·증명 번들, member·api_token(sha256)·daemon). `github`: OAuth device flow(`VERSION`). `teamclient`: 데몬→서버 HTTP 클라이언트. `httpapi.MountServer`: `/auth/device/*`·`/me`·`/sync/register|push|pull`·`/gates[/{id}/resolve]`·`/ledger/key|checkpoint|proof/*`. `treesitter`는 wazero로 `wasm/keelage-ts.wasm`(런타임+TS/TSX, `make wasm`으로 재빌드, `VERSION` 참조)을 돌린다. `claudecode`는 스펙의 `adapters/claude-code`(VERSION·hooks.json·SKILL.md 임베드). `mcp`는 공식 Go SDK stdio 서버(데몬 UDS의 얇은 클라이언트).
- `cmd/keelage`: `daemon`(UDS: `/v1/hook`·`/v1/what_touches`·`/v1/related`, 원장 catch-up) · `hook <tool> <event>`(50ms fail-open) · `mcp` · `adapter claude-code print …` · `constraint add|verify|list` · `anchor add|list` · `verify [--changed]` · `change derive|list` · `inbox` · `history <anchor>` · `sessions` · `adapter git print post-commit` · `init [--verify|--force|--dry-run]` · `uninit` · `status` · `setup` · `uninstall [--purge]` · `server login|logout|key|proof` · `sync push|pull|status` · `gate ask|resolve|list` · `verify-proof --server-key`(헤드리스). 로그인 뒤에는 서버 사용자명이 로컬 행위자 ID다. e2e: `verify_test.go`, `week8_test.go`(derive·inbox·history), `setup_test.go`(setup→init→uninstall 왕복 원상태), 훅 시뮬레이터 `hook_test.go`(`testdata/hooks/claude-code/*.jsonl`, `-update`로 골든 재생성), `sync_test.go`(두 데몬 공유·증명·gates, Postgres 필요).
- `cmd/keelage-server`: `serve --db --key [--github-client-id] [--checkpoint-interval]` · `org create|list` · `member add` · `token create|revoke` · `checkpoint <org>` · `key`. 체크포인트 워커는 움직인 org만 서명한다.
- 검증: `make lint test` · `make check-generated`(스키마 생성물 diff) · `make test-pg`(일회용 Postgres 16, `scripts/pg-test.sh`; 마이그레이션을 고쳤으면 `make pg-reset`). dogfood: `make dogfood-record` → 커밋마다 `make dogfood-verify`(docs/metrics).

## 스택
Go 1.2x · chi · pgx+sqlc · goose · SQLite(modernc) · tree-sitter(wasm+wazero, v0는 TS만) · transparency-dev/merkle · cobra · React+Vite(13주 이후)

## 테스트
core는 테이블 테스트, 해시·머클은 속성 테스트, 렌더러는 골든, 훅은 녹화 재생(`testdata/hooks/`).
