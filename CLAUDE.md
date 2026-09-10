# kb — Claude Code 작업 규칙

이 리포는 `docs/spec/`의 스펙과 계획을 구현한다. 작업 전 다음 순서로 읽는다:
1. `docs/spec/implementation-plan-v0.md` §9 (12주 일정과 "뺀 것" 목록)
2. `docs/spec/architecture-patterns-v0.md` (코드 모양)
3. 관련 ADR (`docs/adr/`)

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

## 스택
Go 1.2x · chi · pgx+sqlc · goose · SQLite(modernc) · tree-sitter(wasm+wazero, v0는 TS만) · transparency-dev/merkle · cobra · React+Vite(13주 이후)

## 테스트
core는 테이블 테스트, 해시·머클은 속성 테스트, 렌더러는 골든, 훅은 녹화 재생(`testdata/hooks/`).
