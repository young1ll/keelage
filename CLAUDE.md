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

## 코드 구조 (1–7주차 기준)
- `cmd/keelage`(데몬·CLI), `cmd/keelage-server` — 조립만. `tools/schemagen` — Go 구조체 → `spec/schema`.
- `internal/core` — 값 객체(ID·ActorRef·Level·ScopeKey·Anchor·Hash3), `Event/Command/Root[T]`, `Codec`(kind·version·업캐스터), `Envelope`(meta/body 분리 해시·체인), `DecideContext`.
  - `core/harness`: Constraint(닻 바인딩 포함)·Scope 애그리게이트. `core/accountability`: Change·Gate 애그리게이트. `core/realization`: Syntax 모델·심볼 추출·Hash3·이동 감지·Anchor 애그리게이트(review/stale 2단계). `core/supply`: 정규 훅 이벤트 6종·`Compose`(사실→주입 문단·경고·차단). 커맨드는 `XxxCmd`(ID·Idem)를 임베드한다.
- `internal/port` — Ledger·Clock·IDGen·Signer·Projector. `port/ledgertest`는 모든 Ledger 구현이 통과해야 하는 계약 스위트.
- `internal/app` — 커맨드 파이프라인(`Pipeline`, `Register[T]`), 메모리 투영(`ScopeIndex`·`ConstraintIndex`·`ChangeIndex`·`AnchorIndex`), `ProjectionContext`, `Resolver`(닻→Hash3, 캐시, generic 강등), `Verifier`, `Rebuild`/`Replay`, `Hooks`(HookService·ContextQuery: 세션 레지스트리·사실 수집).
- `internal/adapter/{memory,sqlite,ulid,uds,httpapi,git,treesitter,claudecode,mcp}` — 어댑터. 서로 import 금지(depguard); cmd가 배선한다. `treesitter`는 wazero로 `wasm/keelage-ts.wasm`(런타임+TS/TSX, `make wasm`으로 재빌드, `VERSION` 참조)을 돌린다. `claudecode`는 스펙의 `adapters/claude-code`(VERSION·hooks.json·SKILL.md 임베드). `mcp`는 공식 Go SDK stdio 서버(데몬 UDS의 얇은 클라이언트).
- `cmd/keelage`: `daemon`(UDS: `/v1/hook`·`/v1/what_touches`·`/v1/related`, 원장 catch-up) · `hook <tool> <event>`(50ms fail-open) · `mcp` · `adapter claude-code print …` · `constraint add|verify|list` · `anchor add|list` · `verify [--changed]`(헤드리스). e2e: `verify_test.go`, 훅 시뮬레이터 `hook_test.go`(`testdata/hooks/claude-code/*.jsonl`, `-update`로 골든 재생성).
- 검증: `make lint test` · `make check-generated`(스키마 생성물 diff). dogfood: `make dogfood-record` → 커밋마다 `make dogfood-verify`(docs/metrics).

## 스택
Go 1.2x · chi · pgx+sqlc · goose · SQLite(modernc) · tree-sitter(wasm+wazero, v0는 TS만) · transparency-dev/merkle · cobra · React+Vite(13주 이후)

## 테스트
core는 테이블 테스트, 해시·머클은 속성 테스트, 렌더러는 골든, 훅은 녹화 재생(`testdata/hooks/`).
