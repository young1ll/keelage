# 아키텍처·구현 패턴 v0

> [[implementation-plan-v0]]의 "무엇으로·언제"를 받아, "코드를 어떤 모양으로 쓰는가"를 정한다. 12주 범위 기준이며 v1 항목은 표시한다.

## 1. 계층과 의존 방향

```
cmd/kb, cmd/kb-server            (조립: 설정·와이어링만)
        │
internal/app                      (유스케이스 = 커맨드 파이프라인·쿼리. 포트에만 의존)
        │
internal/core                     (도메인: 애그리게이트·값 객체·범위 해석·자율 전이. 표준 라이브러리만)
        ▲
internal/port                     (인터페이스: Ledger, Projection, AnchorResolver, ContextRenderer, Clock, Signer …)
        ▲
internal/adapter/*                (sqlite, postgres, treesitter, github, mcp, hook, git, oidc …)
```
- import 규칙(lint로 강제): `core`는 아무것도 import하지 않음(표준 라이브러리 제외). `app`은 `core`·`port`만. `adapter/*`는 `port`·`core`를 구현/사용하되 서로 import 금지. `cmd`만 전부를 본다.
- 바운디드 컨텍스트는 `core` 안의 하위 패키지: `core/harness`, `core/accountability`, `core/realization`, `core/supply`, `core/work`. 컨텍스트 간 참조는 ID 값 객체로만(구조체 공유 금지).

---

## 2. 도메인 구현 패턴

### 2.1 애그리게이트 = 순수 함수 두 개
```go
// 결정: 상태 + 커맨드 → 이벤트들 (부작용 없음, 결정적)
func (c Change) Decide(cmd Command, ctx DecideContext) ([]Event, error)
// 적용: 상태 + 이벤트 → 새 상태 (실패하지 않음)
func (c Change) Apply(e Event) Change
```
- `DecideContext`는 결정에 필요한 외부 사실만 값으로 전달(현재 유효 제약 스냅샷, 자율 수준, 시각, 행위자). 애그리게이트가 저장소를 부르지 않는다.
- 규칙은 전부 `Decide` 안에 테이블 드리븐으로. 예: 자율 전이표 `map[Level]map[Trigger]Level`, settle 조건 검사 함수 목록.
- 이벤트는 불변 구조체 + `Kind() string` + `Version() int`. 스키마 변경은 새 버전 + 업캐스터(`Upcast(old) new`). 기존 이벤트는 절대 재작성하지 않는다(원장 원칙).

### 2.2 값 객체
- `Anchor`: 파싱된 구조(`Scheme, Path, Symbol`)와 정규 문자열. 생성자에서만 검증. 비교는 정규 문자열.
- `ScopeKey`: `{Org, Product, Team, Repo, PathGlob, Person}` — 격자 좌표. 해석 함수 `Resolve(keys []ScopeKey, kind ObjectKind) []ScopeKey`가 제약(넓은 우선)/컨텍스트(좁은 우선) 순서를 돌려준다. 순수 함수, 테이블 테스트.
- `Hash3 {Signature, Body, File}`: 닻 해시 3층. `Cmp(a,b) Change` → none/body/signature/file.
- `ActorRef`: kind·id·key fingerprint. 서명 검증은 어댑터, 비교는 core.

### 2.3 ID·시각
ULID(정렬 가능). 생성은 `port.IDGen`·`port.Clock`으로 주입(테스트 결정성).

---

## 3. 커맨드 파이프라인 (app)

```
Handle(cmd):
  1 인증·권한   actor 종류 × 범위 자율 수준 × 판단 종류(하네스/실현체) → 거부 시 Rejected 이벤트 기록(무음 실패 금지)
  2 로드       aggregate = replay(ledger.Read(streamID)) 또는 snapshot+tail
  3 컨텍스트   DecideContext 구성(유효 제약 스냅샷은 투영에서, 시각은 Clock)
  4 결정       events, err = agg.Decide(cmd, ctx)
  5 추가       ledger.Append(streamID, expectedVersion, events)  -- 낙관적 동시성
  6 발행       in-process bus → 투영 갱신(동기, 같은 트랜잭션 또는 직후)
  7 응답       새 버전·이벤트 ID
```
- 스트림 = 애그리게이트 ID. 전역 seq는 원장이 부여.
- 멱등: 커맨드에 `IdempotencyKey`(훅·sync 재시도 대비). 원장이 `(stream, key)` 유니크로 거부.
- 쿼리는 파이프라인 밖: `app/query`가 투영 테이블을 직접 읽는다(CQRS 읽기).

---

## 4. 원장과 투영

### 4.1 포트
```go
type Ledger interface {
  Append(stream string, expected int64, evs []Event) (Range, error)
  Read(stream string, from int64) ([]Event, error)
  ReadAll(fromSeq int64, limit int) ([]Event, error)   // 투영·sync용
  Head() (Seq, Hash)
}
```
데몬(SQLite)과 서버(Postgres)가 같은 포트를 구현. 투영 쿼리는 서버에만 풍부하게, 데몬은 최소.

### 4.2 SQLite (데몬)
```sql
event(seq INTEGER PK, stream TEXT, ver INTEGER, kind TEXT, v INTEGER, ts, actor JSON, meta JSON, body JSON,
      meta_hash BLOB, body_hash BLOB, prev_hash BLOB, hash BLOB, sig BLOB, idem TEXT,
      UNIQUE(stream, ver), UNIQUE(stream, idem));
-- hash = H(prev_hash || meta_hash || body_hash). WAL 모드. 투영: anchor_hist, constraint_local, change_local (단순).
```

### 4.3 Postgres (서버)
```sql
event(org_id, seq BIGSERIAL, stream, ver, kind, v, ts, actor JSONB, meta JSONB, body JSONB,
      meta_hash, body_hash, leaf_hash, sig, origin JSONB /* {daemon_id, local_seq} */, idem,
      UNIQUE(org_id, stream, ver), UNIQUE(org_id, origin)) PARTITION BY LIST(org_id)  -- 또는 hash
merkle_node(org_id, level, idx, hash)              -- 증분 갱신
checkpoint(org_id, tree_size, root, ts, sig, external JSONB)
```
- RLS: 모든 테이블 `org_id` + 정책 `org_id = current_setting('app.org_id')`. 커넥션 획득 시 세션 변수 설정 미들웨어.
- 투영: `constraint_current`, `decision_current`, `change_current`, `finding_open`, `anchor_history`, `scope_index`, `ownership`, `graph_edge(src, rel, dst)`. 각 투영은 `projection_offset(name, seq)`로 체크포인트, 재구성은 offset=0.
- 그래프 질의: `graph_edge` 재귀 CTE(깊이 제한 2~3). 그래프 DB 없음.

### 4.4 투영 패턴
```go
type Projector interface { Name() string; Handle(tx, Event) error }
```
동기 투영(같은 트랜잭션)은 판단·제약처럼 즉시 일관성이 필요한 것만. 나머지(그래프·통계)는 비동기 루프가 `ReadAll(offset)`로 따라잡는다. 재구성은 `projection reset <name>`.

### 4.5 머클(서버)
rfc6962 해시(leaf `0x00||…`, node `0x01||…`). 이벤트 append마다 leaf 추가·경로 노드 갱신(O(log n)). 체크포인트는 워커가 10분/1,000건마다 `root` 서명. 증명 API는 `merkle_node`에서 경로를 읽어 구성. 검증 CLI(`kb verify-proof`)는 서버 없이 체크포인트 서명·포함·일관성을 검사 — 코드 공개.

---

## 5. 데몬 프로세스 패턴

- 단일 프로세스, `errgroup` 감독자: `api(UDS)`, `git-hook-listener`, `sync`, `projector`, `cache-janitor`. 하나가 죽으면 전체 재시작(단순).
- API는 HTTP over Unix 소켓(`~/.kb/kb.sock`, 0600). `kb hook`·`kb mcp`·CLI는 전부 이 API의 얇은 클라이언트. 데몬 내부 로직을 CLI가 중복 구현하지 않는다.
- 타임아웃: 훅 경로 50ms(컨텍스트 데드라인), CLI 2s. 초과는 fail-open.
- 캐시: 파일 → 심볼 목록·Hash3를 `(path, mtime, size)` 키로 LRU. 제약·소유 지도는 메모리 스냅샷(sync 후 교체, 불변 구조 스왑).
- 종료: SIGTERM → 진행 중 append 완료 → outbox 남으면 최대 10초 push 시도 → 종료.

---

## 6. 훅 어댑터 패턴

- 내부 정규 이벤트 6종: `session_start`, `prompt`, `pre_edit{files}`, `post_edit{files}`, `tool_result`, `session_end`. 어댑터는 도구 이벤트 → 정규 이벤트 매핑표 + 응답 역매핑만 가진다.
- `kb hook <tool> <event>`: stdin JSON → 어댑터 정규화 → UDS 요청 → 응답을 도구 형식으로 stdout. 프로세스 기동 예산 5ms(Go 정적 바이너리).
- 응답 계약(정규): `{inject?: string, warn?: []string, block?: {reason}}`. 도구가 block을 지원 안 하면 inject로 강등.
- 시뮬레이터: `testdata/hooks/<tool>/<case>.jsonl` — 실제 도구에서 녹화한 이벤트 시퀀스. e2e는 데몬을 띄우고 시퀀스를 재생해 원장 결과를 골든과 비교. 어댑터 버전 올릴 때 녹화 갱신.

---

## 7. 닻 해석 구현

- tree-sitter 문법을 wasm으로 로드(wazero). 언어별 `queries/symbols.scm`(tree-sitter query)로 심볼·시그니처 노드 추출.
- `Signature` 해시: 이름·파라미터 타입·반환 타입·export 여부를 정규화한 문자열의 SHA-256. `Body` 해시: 심볼 서브트리의 S-표현에서 주석·공백 제거 후 해시. `File`: 파일 바이트 해시.
- 파일+범위 → 심볼: 범위를 포함하는 가장 작은 심볼 노드. 없으면 파일 닻으로 강등.
- 이동 감지: 같은 `Signature` 해시가 다른 경로/위치에서 나타나고 원위치에서 사라지면 `AnchorMoved` 후보(사람 확인 없이 자동, 상태 변화 없음).
- generic 언어: 파일 해시만. `Signature`=`Body`=파일 해시(즉 모든 변경이 시그니처 변경 = 보수적).
- 대형 리포: 인덱스를 영속화하지 않고 필요한 파일만 파싱. `verify`는 변경 파일 목록(git diff)에서 시작.

---

## 8. Context 합성·렌더

- `Resolve(repo, path, tool, person)`: 적용 범위 키 → 제약(넓은 우선)·컨텍스트(좁은 우선) 목록 → 상태 필터(`verified|review`, `audience∋ai`) → 렌더러 입력.
- 렌더러는 순수 함수 `Render(input) []FileOp`(생성·교체·삭제). 실제 쓰기는 어댑터가 manifest와 대조해 수행(변경 없으면 no-op).
- managed 블록: `<!-- kb:begin <id> <hash> -->` … `<!-- kb:end -->`. 바깥은 절대 손대지 않음. 블록 해시 불일치 = `diverged`.
- 골든 테스트: `testdata/render/<tool>/<case>/{input.json, expected/}`.

---

## 9. sync 프로토콜 구현

- `POST /sync/push`: `{daemon_id, events[]}` 각 이벤트에 데몬 서명·`origin{daemon_id, local_seq}`. 서버는 서명 검증 → `origin` 유니크로 중복 제거 → 커맨드 파이프라인 재검증 → 확정/`Rejected`. 응답에 `accepted[]`, `rejected[]{local_seq, reason}`.
- `GET /sync/pull?since=&scopes=`: 팀·제품 층 제약·결정·소유 지도·닻 이력 이벤트만. 데몬은 메모리 스냅샷을 원자 교체.
- 배치 200건, 지수 백오프, `outbox(local_seq, pushed_at)` 테이블.
- 인증: OIDC 로그인 → refresh(장기, 파일 0600) + access(15분).

---

## 10. 서버 구현 패턴

- HTTP: `oapi-codegen` 스트릭트 서버 인터페이스 → `app` 호출. 미들웨어: 요청 ID·OTel·인증·`org_id` 세션 변수·레이트리밋.
- 웹훅: GitHub 서명(HMAC) 검증 → 이벤트를 원장이 아니라 `inbound_webhook` 테이블에 저장(멱등: delivery id) → 워커가 처리(커맨드 발행). 처리 실패는 재시도, 원장 오염 없음.
- 아웃바운드(PR 코멘트·체크): **아웃박스 패턴** — 투영 이벤트가 `outbound(kind, payload, attempts)`를 만들고 워커가 GitHub API 호출. 중복 코멘트 방지 키 = `(change_id, kind)`.
- 워커: `checkpoint`, `outcome-window`, `outbound`, `projector-async`. 전부 단일 프로세스 고루틴, Postgres advisory lock으로 다중 인스턴스 시 1개만 실행.

---

## 10b. 승인 브로커 구현
- `Gate` 애그리게이트: `Request → Decide(policy)`가 자율 수준·상한·소유 지도·행위자 종류로 allow/deny/ask 즉시 판정. ask면 `GateOpened`, 만료 타이머는 워커.
- 답은 어느 채널에서 와도 같은 커맨드 `Resolve{decision, reason, by, sig}` — 웹 인박스·CLI·에이전트의 자체 승인 UI(웹훅 콜백)·채팅 봇 어댑터. 채널은 어댑터, 기록은 하나.
- 에이전트 클라이언트 계약: 행동 전 `POST /gates`, 응답의 `policy_ref`를 결과 보고(`/changes`)에 첨부. 묻지 않은 행동은 사후 `ai-unreviewed` finding.
- 멱등: `(actor, proposal_ref)` 유니크. 같은 제안을 재요청하면 기존 gate 반환.

## 11. 테스트 전략

| 층 | 방식 |
|---|---|
| core | 테이블 테스트(커맨드→이벤트, 이벤트→상태), 자율 전이표 전수, 범위 해석 케이스 |
| 해시·머클 | 속성 테스트(임의 이벤트 시퀀스 → 증명 검증 항상 참), 알려진 벡터 |
| 렌더러 | 골든 파일 |
| 훅 | 녹화 재생 e2e |
| 원장 | SQLite·Postgres 공용 계약 테스트(같은 테스트 스위트를 두 구현에) |
| 통합 | 픽스처 리포 3개(소·중·대)에서 `init → 훅 시퀀스 → commit → verify` |

---

## 12. 관측성(우리 것)
커맨드마다 OTel span(`command.kind`, `stream`, `events`), 훅 경로에 히스토그램(`hook.latency`, `hook.fail_open`), 투영 지연(`projection.lag`), sync(`push.rejected`). 로그는 구조화(JSON), 개인 데이터 필드는 로그 금지 목록으로 차단.

---

## 13. UI 아키텍처 (13주 이후, 설계는 지금)

### 13.1 구조
- React + TypeScript + Vite. 라우팅 `react-router`. 데이터는 **생성된 API 클라이언트 + TanStack Query**, 실시간은 SSE(`/events/stream`)로 쿼리 무효화만(상태를 SSE로 직접 갱신하지 않음 — 단순성).
- 화면 상태 모델 = 서버 투영과 1:1. 클라이언트 파생 상태 최소. 폼은 커맨드 하나에 대응(`judge`, `promote`, `setAutonomy`).
- 디자인 토큰 한 파일: 색=범위(product/team/repo/path/person 램프), 테두리=상태(generated/verified/review/stale/diverged/retired), 점선=로컬·미검증. 이 셋 외의 의미 있는 시각 문법을 추가하지 않는다(범례 금지 원칙).

### 13.2 뷰와 컴포넌트
| 뷰 | 핵심 컴포넌트 | 상호작용 |
|---|---|---|
| 인박스 | `InboxList`(가상 스크롤), `InboxItem`(종류 아이콘·범위 색·상태 테두리), `DetailPane` | 키보드 우선: j/k 이동, a 승인, r 거부, e 수정, s 건너뜀. 한 번에 한 항목, 커밋 묶음 접힘 |
| Change | `ImpactTree`(변경 파일 ← 닻 ← 제약/결정, 3층 접기), `JudgmentPanel`(이유 초안·확인), `SessionFacts`(거부·되돌림 목록), `PlainToggle`(기술/평문 렌더러 전환) | 영향 노드 클릭 → 해당 제약·닻 이력으로 |
| 닻 이력 | `Timeline`(Change·판단·결과·finding 단일 축) | 필터: 행위자·결과·기간 |
| 컨텍스트 미리보기 | `ResolvedDoc`(도구별 탭, 줄마다 출처 범위 색 띠), `WhyLine`(이 줄이 포함된 이유) | 범위 토글로 "이 팀만 보기" |
| 하네스 | `ScopeTree`(제품 헌법/우리 팀/내 것 3층만 노출), `AutonomyChip`(L0~L3·상한), `PromotionQueue` | 승격 승인은 원천 판단·설명 문단이 펼쳐진 상태에서만 활성 |
| 문서 | `DocView`(md 렌더·상태·닻 링크·supersedes 체인), `Discussion` (v1) | v1: 편집=커맨드 |
| 그래프 | react-flow + elk, 범위 클러스터 그룹 노드, 반경 2 확장 | 노드 200 초과 시 클러스터만 표시 |

### 13.3 원칙
- 인박스가 홈. 대시보드 없음.
- 모든 액션은 되돌릴 수 있는 커맨드(원장에 남음)이므로 확인 대화상자 최소. 되돌리기는 "반대 커맨드".
- 평문 렌더러는 같은 impact 객체에 대한 두 번째 뷰 — 데이터 분기 없음.
- 접근성: 키보드 완전 조작, 색 외 테두리·아이콘으로 상태 중복 표현(색맹 대응은 문법 자체가 보장).
- 로딩은 투영 지연을 그대로 표시("최신 seq 대비 N건 뒤") — 감추지 않는다.

### 13.4 CLI 출력(12주 내)
`kb inbox`: 표(종류·범위·닻·상태·원인). `kb history <anchor>`: 시간순 표. `kb preview --tool claude`: 렌더 결과 + 줄별 출처 접두. `--json` 플래그로 동일 데이터. 색은 터미널 지원 시 범위 색만.

---

## 14. 장애·오류 원칙
- 훅 경로는 항상 fail-open. 데몬 장애가 사용자의 편집을 막지 않는다.
- 원장 append 실패는 사용자에게 보인다(무음 손실 금지). 투영 실패는 로그+재구성 가능.
- sync 실패는 로컬에 쌓인다. 서버 장애가 로컬 작업을 막지 않는다.
- Rejected는 이벤트다. 충돌은 사라지지 않고 기록된다.
