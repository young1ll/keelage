# Engineroom (가칭) — 기술 스펙 v0

> 한 서버 안에서 **정의(API 계약) → 구현 → 운영·관측 → 유지보수**가 하나의 지도(그래프 UI)에서 순환하는 API-first 백엔드 작업대.
> 본체는 지도이고 실행은 그 아래의 세부다.

## 0. 확정된 결정 (논의 결과)

| 항목 | 결정 |
|---|---|
| 본체 | 그래프 지도. 실행 엔진은 부속 |
| 단위 | 계약(Contract)이 붙은 노드. 경계는 도구가 강제, 내부 코드는 자유 |
| 대상 | 1차: AI 활용 개발로 코드가 관리 범위 밖으로 밀려나는 개발자/1인 사업가·CTO. 2차: PM/PO |
| 배포 형태 | 바이너리/이미지 1개. 기본은 솔로 모드(portal+runtime). 분리 시 `portal` / `runtime --join` |
| 분리 원칙 | Portal=설계·지식·관측 집계(컨트롤 플레인), Runtime=실행(데이터 플레인). desired state 배포만 하고 스케줄링은 안 함. Run은 Runtime을 넘지 않음. Runtime은 Portal 없이 계속 동작 |
| 코어 언어 | Go. 근거는 성능이 아니라 배포·운영 특성(단일 정적 바이너리·작은 이미지·런타임 의존 없음·동시 연결). UI·SDK는 TS/Python |
| 언어 | 코어는 언어 중립 프로토콜. SDK: TypeScript, Python 우선 |
| 코드→지도 | 정적 분석 안 함. SDK 자기 등록 + 트레이스 |
| 진실 원천 | append-only 이벤트 로그 1개. 레지스트리(git 모형)와 운영 뷰는 투영 |
| 이력 모델 | git 자료구조(blob/tree/commit/ref)를 DB에 구현. 실제 git은 내보내기 어댑터 |
| 버전 단위 | Group(=컨텍스트). Root는 Group Live 버전의 락파일 |
| 라이프사이클 | Draft → Build(불변 스냅샷) → Live → 재정의(새 Draft) |
| 지식 | ADR/AIP/RULE/Runbook은 노드 부착물(attachment). 노드가 아님 |
| AI | 사람과 같은 행위자(actor). 계약 정의·호출·코드 생성이 같은 로그에 남음 |
| 내구성 실행 | 노드 단위 체크포인트, at-least-once + 멱등 키 강제 |

**비목표(v0)**: 자동 배치·재배치·오토스케일·페일오버(14절 제외 열), 인라인 수동 코드 에디터(코드는 SDK/AI 산출물 슬롯), 프론트엔드 빌더, 언어별 정적 분석, RBAC 세분화.

---

## 1. 시스템 구성

용어: 그래프의 "노드"와 구분해 실행 서버는 **Runtime**이라 부른다.

```
┌──────────── Portal (컨트롤 플레인) ────────────┐
│  UI (React, 그래프 = react-flow)               │
│  Registry   계약·그룹·스냅샷·ref (설계 이벤트의 투영)│
│  Ledger     설계 이벤트(원천) + 실행 이벤트(복제본) │
│  Knowledge  md 파싱 · 링크 검증 · AI 컨텍스트 렌더 │
│  Observe    OTel 집계 → 엣지 지표 · 신호        │
│  Control    Runtime 등록 · 스냅샷/라우팅 배포 · Gate│
│  MCP        Live 계약(도구)·verified 문서(리소스) │
└───────────────────────┬────────────────────────┘
        SSE(desired)    │    ▲ 등록·이벤트·트레이스 (아웃바운드만)
                        ▼    │
┌──────────── Runtime (데이터 플레인) ────────────┐   × N
│  Snapshot cache  Live tree + 라우팅 테이블 로컬 사본│
│  Gateway         HTTP 라우팅 · 인증 · 계약 검증   │
│  Scheduler       cron · 큐 · 재시도              │
│  Ledger(local)   실행 이벤트 원천 + outbox 커서    │
│  Worker Hub      로컬 언어 워커 spawn · IPC      │
│   ├ ts-worker  ├ py-worker                     │
└────────────────────────────────────────────────┘
```

- 바이너리 하나: `engineroom`(솔로 = portal+runtime, 루프백으로 같은 프로토콜 사용), `engineroom portal`, `engineroom runtime --join <url> --token <t>`.
- Portal DB = Postgres. Runtime 로컬 로그 = 내장 SQLite(단일 프로세스, 의존 없음).
- 사용자 앱에 SDK를 넣고 `--join`하면 그 앱이 곧 Runtime이다(기존 코드 붙이기 경로).
- 워커↔Runtime 전송은 로컬 HTTP + SSE. 프로토콜은 4절.

## 2. 데이터 모델

### 2.1 노드 정체성 (가장 먼저 안정화할 것)

```
node_id   : ULID   — 최초 등록 시 발급, 영구 불변
key       : string — "<group>/<name>" 사람이 읽는 슬러그, 변경 가능
aliases   : string[] — 이전 key 이력 (리팩토링·개명 추적)
fingerprint: string — SDK가 보내는 소스 해시(파일경로+함수명+시그니처)
```

- 등록 시 매칭 순서: `node_id`(SDK가 로컬에 캐시) → `key` → `fingerprint` 유사 매칭 → 신규 발급.
- 매칭이 애매하면(fingerprint만 부분 일치) "미확정 노드"로 올리고 사람/AI가 병합 확정. 자동 병합 금지.
- 계약·코드·트레이스·부착물은 전부 `node_id`로만 연결한다. `key`로 조인하지 않는다.

### 2.2 이벤트 로그 (원천)

```sql
event (
  seq        bigserial PK,        -- 전역 순서
  ts         timestamptz,
  kind       text,                -- 설계 | 실행
  type       text,                -- 아래 2.3
  actor      jsonb,               -- {kind: human|ai|system, id, model?, prompt_ref?}
  subject    jsonb,               -- {node_id?, group_id?, run_id?}
  payload    jsonb,
  causation  bigint,              -- 원인 이벤트 seq
  snapshot   text                 -- 발생 시 Live 스냅샷 commit 해시
) PARTITION BY LIST (kind);       -- 설계=영구, 실행=보존기간 후 아카이브
-- 소유: 설계 이벤트는 Portal이 원천. 실행 이벤트는 Runtime 로컬 로그가 원천이며
--       (runtime_id, local_seq)로 Portal에 복제·중복 제거. Portal의 seq는 복제 커서일 뿐
--       발생 순서가 아니다 — 정렬은 ts·causation으로 한다.
```

### 2.3 이벤트 타입 v0

설계: `NodeRegistered`, `NodeMerged`, `ContractDefined`, `ContractRevised`, `AttachmentAdded`, `AttachmentStateChanged`, `GroupBuilt`, `BuildFailed`, `LiveMoved`, `DraftForked`, `ExportedToGit`, `RuntimeJoined`, `RuntimeLeft`, `GroupAssigned`, `SnapshotApplied`, `SnapshotRejected`, `DrainStarted`, `DrainCompleted`
실행: `RunStarted`, `StepStarted`, `StepCompleted`, `StepFailed`, `StepRetried`, `GateOpened`, `GateResolved`, `Compensated`, `RunCompleted`, `RunFailed`, `SpanIngested`

### 2.4 레지스트리 (설계 이벤트의 투영, git 모형)

```sql
object (hash text PK, kind text /*blob|tree|commit*/, body jsonb)
ref    (group_id, name text /*draft/<n>|live|v<n>*/, target text, PK(group_id,name))
group  (id, key, root_id)
root   (id, lock jsonb /* {group_key: commit_hash} */)
```

- blob = Contract JSON 1개(해시 중복 제거). tree = `{node_key: blob_hash}`. commit = `{parent, tree, actor, message, validation}`.
- Build = 새 tree + commit + `v<n>` ref. Live 이동/롤백 = `live` ref 갱신 1행.
- diff는 의미 diff(JSON 필드 단위)로 계산해 호환성 판정(3.4)에 사용.

### 2.5 실행 상태 (실행 이벤트의 투영)

```sql
run  (id, trigger_node, snapshot, status, started, ended, idempotency_key UNIQUE)
step (id, run_id, node_id, attempt, status, input_hash, output_ref, error, started, ended)
gate (step_id PK, requested_at, resolved_at, resolver actor, decision)
```

### 2.6 부착물 (지식)

```sql
attachment (
  id, node_id, type text /*adr|aip|rule|runbook|note*/,
  path text,            -- 리포/업로드 내 md 경로
  frontmatter jsonb,    -- 4.1
  content_hash text,
  state text,           -- generated|verified|live|stale|retired
  provenance jsonb      -- actor, prompt_ref, snapshot
)
```

---

## 3. 계약 (Contract) 스키마 v0

```json
{
  "node_id": "01J...",
  "key": "billing/issue-invoice",
  "kind": "function",
  "version": 3,
  "summary": "청구서 발행",
  "trigger": null,
  "input":  { "$schema": "json-schema", "type": "object", "..." : "..." },
  "output": { "type": "object", "..." : "..." },
  "errors": [ { "code": "INVOICE_DUPLICATE", "retryable": false } ],
  "auth":   { "scopes": ["billing:write"], "actors": ["human", "ai"] },
  "idempotency": { "key": "$.input.orderId", "required": true },
  "compensate": { "node": "billing/void-invoice", "map": { "invoiceId": "$.output.id" } },
  "limits": { "timeout_ms": 30000, "retry": { "max": 3, "backoff": "exp" } },
  "gate": null,
  "labels": { "lang": "ts", "tier": "core" },
  "exposure": { "http": { "method": "POST", "path": "/billing/invoices" }, "mcp": true }
}
```

### 3.1 kind별 차이

| kind | trigger | 비고 |
|---|---|---|
| `function` | 없음(호출됨) | 워커가 구현 |
| `http` | `{method, path}` | Gateway가 라우팅 → function 또는 flow |
| `cron` | `{expr, tz}` | Scheduler가 Run 생성 |
| `event` | `{topic}` | 내부 이벤트/큐 구독 |
| `model` | 없음 | `impl: {model, prompt_ref, schema_out}` — 프롬프트가 계약의 일부 |
| `gate` | 없음 | `gate: {approvers, timeout, on_timeout}` — 사람 승인 후 진행 |
| `store` | 없음 | `impl: {table, schema}` — 코어가 관리하는 테이블/KV |
| `external` | 없음 | `impl: {openapi_ref | mcp_ref}` — ACL, 트레이스만 |
| `flow` | 선택 | 노드 DAG: `steps: [{node, in_map, out_map, on_error}]` |

### 3.2 규칙

- `input`/`output`은 JSON Schema draft 2020-12. 계약 없는 노드는 등록되지만 `exposure`·`flow` 참여 불가.
- `actors`에 `ai`가 포함된 계약만 MCP 도구로 노출된다. 기본값은 `["human"]`.
- `idempotency.required=true`인데 키 없는 호출은 Gateway가 거부(400).
- `compensate`가 없는 쓰기 계약은 Build 시 경고, `labels.tier=core`면 오류.

### 3.3 Group 단위 버전

- Group마다 `v<n>` 증가. Root `lock`은 `{group_key: {commit, runtime}}` — 배정도 lock의 일부이며 변경은 Build/Live의 한 종류.
- 타 Group 참조는 `"node": "finance/post-ledger@^2"` — Build 시 Root lock 기준으로 해석·고정.

### 3.4 호환성 판정 (Build 검증)

| 변경 | 판정 |
|---|---|
| input 선택 필드 추가, output 필드 추가, errors 추가, limits 완화 | 호환 → 같은 major, 즉시 교체 |
| input 필수 필드 추가/삭제, 타입 변경, output 필드 삭제, scopes 강화, actors 축소 | 파괴 → 새 major. 구버전 deprecation 기간 병행 서빙 |
| `flow`가 참조하는 노드 부재/major 불일치 | Build 실패 (Live 무영향) |
| RULE 부착물 lint 실패 | Build 실패 또는 경고(RULE의 severity) |

---

## 4. 노드 프로토콜 (워커 ↔ 코어)

메시지 3종. JSON. 워커→코어는 HTTP POST, 코어→워커 호출은 SSE 스트림(워커가 구독) + 결과는 HTTP POST.

### 4.1 Register (워커 부팅 시)

```json
POST /v1/workers/register
{
  "worker": { "id": "cached-or-null", "lang": "ts", "sdk": "0.1.0", "labels": {} },
  "nodes": [
    { "node_id": "cached-or-null", "key": "billing/issue-invoice",
      "fingerprint": "sha256:...", "contract": { "...": "선언된 계약(있으면)" } }
  ]
}
→ { "worker_id": "...", "nodes": [ { "key": "...", "node_id": "...", "status": "matched|new|ambiguous" } ] }
```

### 4.2 Invoke (코어 → 워커, SSE)

```json
event: invoke
data: { "invocation_id": "...", "node_id": "...", "step_id": "...",
        "input": {...}, "context": { "run_id", "snapshot", "actor", "attempt", "deadline_ms" } }
```
워커 응답:
```json
POST /v1/invocations/{invocation_id}/result
{ "status": "ok|error", "output": {...}, "error": { "code", "message", "retryable" } }
```

### 4.3 Observe (워커 → 코어)

OTLP/HTTP(JSON) 그대로 수용. 필수 속성: `engineroom.node_id`, `engineroom.step_id`(있으면). SDK가 자동 주입. 외부 앱의 트레이스도 같은 엔드포인트로 받아 블랙박스 노드를 만든다.

### 4.4 SDK 표면 (TS 예)

```ts
import { node, contract } from "@engineroom/sdk";

export const issueInvoice = node(
  contract({ key: "billing/issue-invoice", input: InvoiceIn, output: InvoiceOut,
             idempotency: "$.orderId", compensate: "billing/void-invoice" }),
  async (input, ctx) => { /* 자유 코드 */ }
);
```
Python은 데코레이터 `@node(contract(...))`. 두 SDK는 동일한 계약 적합성 테스트(`sdk-conformance/`)를 통과해야 한다.

---

## 5. 실행 모델

- **Run은 Runtime을 넘지 않는다.** Runtime 경계를 넘는 Group 간 호출은 flow Step이 아니라 계약을 통한 HTTP 호출(`external`과 동일 취급, 트레이스로 엣지). 분산 내구성 실행은 필요 없다.
- **Run** = 트리거 1회. **Step** = 노드 1회 실행. Step 경계에서 체크포인트(입력 해시·출력 ref를 이벤트로 기록).
- 의미론: at-least-once. 재시도는 같은 `step_id`, `attempt+1`. 멱등 키가 같으면 코어가 이전 출력을 재사용(워커 호출 생략).
- Runtime 재시작 시 자기 로컬 로그로 `RunStarted` 이후 `RunCompleted/Failed`가 없는 Run을 마지막 완료 Step 다음부터 재개.
- **Gate**: Step이 `gate` 노드에 도달하면 `GateOpened` 후 대기. 결정 주체는 Portal(UI/MCP/API로 `GateResolved`). Portal 단절 시 `on_timeout`만 Runtime이 로컬 집행. 타임아웃 시 `on_timeout`(reject|approve|escalate).
- **보상**: Run 실패 또는 사용자 "되돌리기" 시, 완료된 Step을 역순으로 `compensate` 계약 호출. 보상 없는 Step은 수동 확인 항목으로 남김.
- **AI 호출**: `actor.kind=ai`인 호출은 계약의 `actors`에 `ai`가 있어야 하고, `labels.tier=core`면 기본적으로 Gate를 자동 삽입(정책으로 해제 가능).

---

## 6. 라이프사이클 & 내보내기

- Draft: Group당 N개. 편집은 `ContractDefined/Revised` 이벤트로 기록(Draft ref가 가리키는 tree 갱신).
- Build: 검증(3.4, 참조 무결성, RULE lint, 워커 등록 상태) → commit → `v<n>`. 실패 시 `BuildFailed`(원인 목록).
- Live: `live` ref 이동 = desired. Runtime이 적용해 `SnapshotApplied`를 올려야 applied. UI는 desired/applied를 구분 표시(어긋남 = 롤아웃 중 또는 실패). 진행 중 Run은 시작 시점 스냅샷으로 완료.
- 재정의: `DraftForked(from=live)`.
- **Git 내보내기**(어댑터): commit 1개 = `contracts/<group>/<key>.json` + `docs/**` 파일 세트 → 실제 git 커밋/PR. 가져오기: 리포의 파일 변경을 Draft로 흡수. 코어 기능이 아니며 켜는 팀만 사용.
- **OpenAPI 가져오기**: 기존 OpenAPI 3.x → Group 생성 + `external` 또는 `http` 계약. 첫 커밋 생성. (온보딩 관문)

---

## 7. 관측

- 엣지 = 트레이스 부모-자식에서 유도. `node_id → node_id` 별 호출 수·p50/p95·오류율을 1분 버킷으로 집계(`edge_stat` 투영).
- 지도 렌더: 엣지 굵기=호출량, 색=오류율, 노드 테두리=상태 신호.
- **신호(flag)** — "관리 범위 밖" 시각화의 핵심:
  - `no-contract`, `no-test`, `no-doc`(부착물 0)
  - `ai-unreviewed`: 최근 변경 actor가 ai이고 사람 `GateResolved`/승인 없음
  - `doc-stale`: 부착물이 참조하는 계약 version과 현재 version 불일치
  - `dead`: N일간 호출 0 (Live인데)
  - `blackbox`: 트레이스만 있고 등록 없음
  - `drift`: managed external 노드의 실제 상태가 desired와 불일치(16절)
- 지표 저장은 이벤트 로그가 아니라 별도 집계 테이블(재투영 가능). 집계는 Portal에서.
- Runtime 경계를 넘는 엣지는 점선. 하트비트 30초 무소식 Runtime의 Group은 `disconnected` 표시(실행은 계속 중일 수 있으므로 Portal은 개입하지 않음).

---

## 8. 지식 (부착물)

### 8.1 frontmatter 규약

```yaml
---
type: adr            # adr | aip | rule | runbook | note
id: ADR-0012
title: 청구서 발행은 멱등 키를 orderId로 고정
nodes: [billing/issue-invoice, billing/void-invoice]   # key 또는 node_id
contract_version: 3
status: verified     # generated | verified | live | stale | retired
actor: { kind: ai, model: claude-…, prompt_ref: "…" }
supersedes: ADR-0007
---
```
- ADR 본문은 MADR, AIP는 Google AIP 번호 체계 차용. RULE은 본문에 검사 스펙(예: `rest.resource-tree: error`)을 포함하며 Build lint로 실행된다.

### 8.2 생애주기 규칙

- `generated`(AI 생성) → `verified`(링크·계약 버전·코드 대조 통과, 사람 확인) → `live` → `stale`(참조 계약 변경 시 자동 강등) → `retired`.
- **AI 컨텍스트 렌더(`CLAUDE.md`/`AGENTS.md`/MCP resource)에는 `verified` 이상만 포함**. 슬롭이 슬롭을 낳는 고리를 여기서 끊는다.
- 동일 노드에 유사 부착물(임베딩 유사도 임계 초과)이 생기면 병합 제안.

---

## 9. AI 통합

- **행위자**: `actor.kind=ai`는 모델·프롬프트 참조·발급 토큰을 가진 1급 주체. 모든 설계·실행 이벤트에 동일하게 기록.
- **MCP 서버**: 코어가 Live 계약 중 `actors∋ai`인 것을 도구로, `verified` 부착물을 리소스로 노출. 외부 에이전트(Claude Code 등)가 붙는 표면.
- **구현 슬롯**: 노드의 코드는 SDK 리포 안 파일이며, UI에서 "의도 입력 → AI가 계약 안에서 코드 생성/수정 → 테스트 → Build" 흐름을 제공. 코어는 코드 저장소가 아니라 코드의 위치·해시·출처만 안다(`fingerprint`·`provenance`).
- **BYOK**: Anthropic/OpenAI 키는 사용자 소유. `model` 노드와 구현 슬롯이 사용.

---

## 10. UI

- 지도: react-flow. 노드 모양=kind, 색=Group, 테두리=신호. 레이아웃 자동(elk) + 수동 고정 저장.
- 노드 패널 탭: 계약 · 코드(읽기+AI 슬롯) · 실행(최근 Run/Step) · 지표 · 문서(부착물) · 이력(commit diff).
- 상단: Group 선택 · Draft/Live 전환 · Build 버튼 · 신호 필터.
- Run 타임라인: Step 간트 + Gate 대기 항목 인박스.
- 지식 뷰: 부착물 상태별 목록, stale 일괄 재검토.

---

## 11. 기술 선택

### 11.1 언어 경계

- **Go**: 코어 전부(Registry·Ledger·Scheduler·Gateway·Observe·Knowledge·Worker Hub) + CLI. 개발자 대상 운영 도구가 갖춰야 할 배포 특성(단일 정적 바이너리, 수십 MB 이미지, 런타임 의존 없음, 즉시 재시작, 수천 동시 연결)이 v0부터 사용자에게 보이기 때문. 성능은 부수 효과.
- **TypeScript**: UI(React), TS SDK. **Python**: Python SDK. 워커는 별도 프로세스라 코어 언어와 무관(Windmill의 Rust 코어 + TS/Py 워커와 같은 분할).
- **공유 소스 `spec/`**: JSON Schema(계약·이벤트)·OpenAPI(코어 API)·프로토콜 정의. Go·TS·Python 타입은 여기서 생성한다. 코어 언어와 무관한 프로토콜이라는 조건이 이 구조로 강제된다.
- **패키지 경계**: NestJS 같은 모듈 강제력이 없으므로 1절의 모듈 분할을 디렉터리 규약(`internal/registry`, `internal/ledger`, …)과 import 제한 lint로 초기에 고정한다.

### 11.2 스택

| 영역 | 선택 | 이유 |
|---|---|---|
| 코어 | Go | 11.1 |
| HTTP/SSE | `net/http` + chi | 프레임워크 최소, SSE 팬아웃 단순 |
| DB 접근 | pgx + sqlc | 타입 있는 SQL, ORM 없음, 투영 질의 직접 제어 |
| 마이그레이션 | goose(또는 atlas) | 단순 |
| DB | Postgres(JSONB, 파티션) / 개발: embedded-postgres | 의미 diff·투영 질의, 도커 1장 요구 |
| 이벤트 로그 | Postgres 테이블(파티션) | v0 규모에 충분, 재투영 단순 |
| 스케줄러 | 코어 내장(robfig/cron + pg 잠금) | 외부 의존 최소 |
| JSON Schema | santhosh-tekuri/jsonschema | 2020-12 지원 |
| 관측 수집 | OTLP/HTTP JSON (otel collector 라이브러리 재사용) | 언어 중립, 기존 앱 호환 |
| UI | React + react-flow + elk | 그래프 표준. 빌드 결과를 Go 바이너리에 `embed` |
| SDK | TS(npm), Python(pypi) | 우선 언어 |
| 스키마 | JSON Schema 2020-12 | 언어 중립 계약 |
| 배포 | 단일 바이너리 이미지 + compose(postgres) | 온보딩 마찰 최소 |

### 11.3 알려진 비용과 경계 조건

- 초기 3~4주 생산성 저하(Go 학습). AI 코딩 보조가 Go에 강해 예전보다 작으며, "AI 활용 개발"의 첫 사용자 경험을 직접 겪는 기회로 본다.
- CPU 무거운 작업(대량 스키마 검증·의미 diff·임베딩 유사도·트레이스 집계)은 고루틴 워커 풀 또는 Postgres 안으로 보낸다.
- 고루틴·채널이 손에 잡혀도 멀티 서버로 돌아가지 않는다. 단일 서버가 제품이다.

## 12. MVP 절단 (v0 → 첫 결제)

1. `spec/` 프로토콜(4) + 적합성 테스트 + Go Worker Hub(Register/Invoke/Observe) + TS SDK — 여기서 Go에 손을 풀며 제품 진척과 겹친다
2. 이벤트 로그(2.2) + 레지스트리 투영(2.4) + `function`/`http`/`cron` 계약
3. 지도 읽기 뷰 + 신호 5종 + 노드 패널(계약·실행·지표)
4. OpenAPI 가져오기 + 트레이스 블랙박스 노드
5. Draft/Build/Live + 호환성 판정
6. 부착물 + frontmatter 검증 + `CLAUDE.md` 렌더
7. Python SDK(적합성 테스트 통과)
8. Gate 노드 + 보상 + MCP 서버 노출
9. Runtime 분리 실행(`--join`) + 스냅샷 적용 프로토콜 + 라우팅 테이블 + Runtime 간 토큰
10. Group 재배정(드레인)
11. Provider 인터페이스 + Keycloak·Postgres·OIDC 프로바이더
12. AWS 첫 세트(SQS·S3·SSM/Secrets·Cognito·Bedrock) + ECS/EC2 배포 모듈
13. (v1) Lambda pull Runtime 어댑터

**첫 결제 데모(5분)**: 리포에 SDK 넣고 데코레이터 3개 → `docker run` → 지도에 노드·트래픽·신호가 뜬다 → 문서 없는 노드에 AI로 ADR 생성 → verified로 승격 → `CLAUDE.md`에 반영 → Build.

---

## 13. 열린 질문

- 노드 코드의 저장 위치: 사용자 리포(현안) vs 코어가 코드도 보관(내보내기 편의). v0는 리포.
- `flow` 편집을 v0에 넣을지(드래그앤드롭의 핵심) vs 코드로 정의한 flow를 읽기만 할지.
- `store` 노드 범위: 코어 관리 테이블을 어디까지(마이그레이션까지?) 책임질지.
- 멀티 테넌시·RBAC: 개인/소팀 전제라 v0 제외. Group 단위 권한이 언젠가 필요.
- Runtime 간 호출 인증: 단기 서명 토큰(v0) → mTLS(v1) 시점.
- Lambda Runtime 어댑터(pull) 도입 시점: Step Functions와의 겹침·콜드스타트가 flow에 미치는 영향 평가 후.
- 이름.

---

## 14. Portal / Runtime 분리

"허접한 k8s": desired state 배포만 하고 스케줄링은 하지 않는다.

### 14.0 Runtime의 정의
Runtime = "우리 바이너리"가 아니라 **4절 프로토콜을 구현하는 무엇**. 두 종류를 구분한다.

| 종류 | 등록 | 호출 | 스냅샷 적용 | 예 |
|---|---|---|---|---|
| **push** (상주) | 부팅 시 자기 등록, SSE 유지 | Portal/타 Runtime → Runtime Gateway HTTP | Runtime이 수신·검증·교체 | 우리 바이너리, SDK 넣은 사용자 앱 |
| **pull** (비상주) | 배포 시 SDK가 노드 목록 산출 → Portal에 등록 | Portal의 Runtime 어댑터가 클라우드 API로 호출 | 어댑터가 "새 버전 배포 + alias 이동"을 `SnapshotApplied`로 해석 | AWS Lambda (v1) |

pull Runtime은 SSE·하트비트·로컬 로그가 없다. 관측은 함수 안 SDK의 OTLP 푸시로만. 14.1~14.5는 push 기준이며 pull은 어댑터가 동등 의미를 제공해야 한다.

### 14.1 합류와 신원
- `runtime --join <portal_url> --token <join_token>`. 조인 토큰은 1회용, 교환 후 장기 자격증명(runtime_id + 키쌍)을 로컬 보관.
- 등록 시 라벨(호스트·보유 워커 언어·환경 태그) 전송. Portal은 라벨을 **배정 검증에만** 사용(Build 시 "이 Group은 python 워커 필요, 이 Runtime엔 없음"을 오류로).
- 하트비트는 관측 푸시에 동승. 무소식 시 표시만 하고 개입하지 않는다.

### 14.2 스냅샷 적용 프로토콜
1. Portal `LiveMoved(group, commit)` → 해당 Runtime SSE로 `desired {group, commit}`.
2. Runtime: tree 수신·로컬 저장 → 적용 전 검증(노드 등록 여부·스키마 로드) → 라우팅·cron·구독을 새 tree로 원자 교체 → `SnapshotApplied`.
3. 실패: `SnapshotRejected(reason)` + 이전 스냅샷 유지.
4. 진행 중 Run은 시작 스냅샷으로 완료, 새 Run만 새 스냅샷 사용(무중단).

### 14.3 오프라인·복제
- Runtime 로컬 로그가 실행 이벤트 원천. outbox 커서로 Portal에 순차 푸시, `(runtime_id, local_seq)`로 중복 제거. Portal 장기 단절 시 축적 후 따라잡기.
- Gate만 Portal이 결정 주체. 단절 시 `on_timeout` 정책만 로컬 집행.
- 컨테이너 교체 시 미전송 outbox 유실 방지: 종료 신호 수신 시 outbox가 빌 때까지 종료 지연(ECS `stopTimeout` 등과 맞춤) + 선택적으로 로컬 로그를 영구 볼륨(EFS 등)에 배치.

### 14.4 Runtime 간 호출
- Portal이 스냅샷과 함께 라우팅 테이블 `{group_key: {runtime_id, base_url}}` 배포.
- 호출자 Gateway가 테이블로 HTTP 호출. **호출자·피호출자 양쪽 계약 검증**(버전 불일치 조기 검출).
- 인증: Portal 서명 단기 토큰(호출자 runtime_id, 허용 Group). 만료 24h — Portal 부재 시 유일한 시간 제한. mTLS는 v1.
- W3C traceparent로 트레이스 연결 → Portal 지도에 점선 엣지.

### 14.5 Group 재배정(드레인)
Build의 한 종류(lock의 `runtime` 변경). ① 새 Runtime에 스냅샷 적용 확인 ② 라우팅 테이블 갱신 배포 ③ 구 Runtime `DrainStarted`(새 Run 거부, 진행 Run 완료 대기) ④ `DrainCompleted` 후 Group 해제.
제약(v0): `store` 노드를 가진 Group은 재배정 불가(데이터 이동 미지원).

### 14.6 솔로 모드
portal+runtime 한 프로세스에서도 루프백으로 같은 프로토콜을 탄다. 코드 경로 단일화 — 분리 모드 버그가 솔로에서 먼저 잡힌다.

### 14.7 경계

| 포함 | 제외 |
|---|---|
| desired/applied 분리, 롤아웃·롤백 | 자동 배치·재배치·오토스케일 |
| Runtime 라벨로 배정 검증 | 리소스 요청/제한 |
| 라우팅 테이블 배포, Runtime 간 인증 | 서비스 메시·네트워크 정책 |
| 드레인 후 재배정(수동) | 장애 시 자동 페일오버 |
| 하트비트·연결 상태 표시 | 헬스 기반 자동 복구 |

제외 열이 필요한 사용자는 k8s 위에서 Runtime을 여러 개 띄운다. 우리는 그것들을 Group 배정 대상으로만 본다.

---

## 15. 시크릿·자격증명

원칙: **Portal은 시크릿을 저장하지 않는다.** 스냅샷·계약에는 참조만 들어가고, 해석은 사용하는 쪽(Runtime)에서 한다. Portal DB 유출이 자격증명 유출이 되지 않게 하기 위함이며, "Portal은 실행하지 않는다" 원칙과 같은 선상.

### 15.1 참조 형식
계약·프로바이더 spec 안에서 `secretRef: "<backend>://<path>"`. 예: `ambient://aws`, `env://KC_ADMIN_PW`, `file:///run/secrets/kc`, `aws-sm://prod/keycloak/admin`, `vault://kv/engineroom/kc`.

### 15.2 백엔드 (우선순위)
1. **ambient** — 실행 환경이 이미 가진 신원. AWS 태스크/인스턴스 역할, k8s 서비스어카운트, GCP 워크로드 아이덴티티. 저장할 것이 없어 기본값.
2. **env / file** — 도커·컴포즈·홈랩 기본.
3. **aws-sm / ssm** — Secrets Manager, Parameter Store. Portal 조인 토큰의 표준 보관처.
4. **vault** — v1.

### 15.3 규칙
- Runtime만 해석한다. Portal UI는 참조 문자열과 "해석 가능 여부(Runtime이 보고)"만 표시.
- 프로바이더 `apply`(16절)는 해당 `external` 노드가 배정된 Runtime에서 실행되므로 자격증명도 그 Runtime에서만 해석된다.
- 시크릿 참조 변경은 계약 변경이며 Build 검증에서 "배정 Runtime이 해당 백엔드를 지원하는가"를 검사한다.

---

## 16. 외부 서비스 통합 (Provider)

외부 제품(Keycloak, Cognito, Postgres, …)을 **desired/applied를 가진 `external` 노드**로 본다. Runtime과 같은 spec/status 모델. 제품과 프로토콜을 구분한다 — 관리 대상은 제품 인스턴스, OAuth/OIDC는 그 위의 계약 형식.

### 16.1 통합 깊이

| 단계 | 내용 | 비용 | v0 |
|---|---|---|---|
| 관측 | `health {url, interval}`, Gateway 경유 트레이스, 메트릭 스크레이프 | 낮음 | 포함 |
| 설정 관리 | 제품 설정을 Group에 선언 → Build/Live 시 admin API로 적용, 실제 상태 읽어 드리프트 신호 | 중간 | 포함(첫 프로바이더 3종) |
| 생애주기 운영 | 제품을 띄우고·내리고·업그레이드 | 높음(=오케스트레이션, 14.7 제외 열) | 제외. 최대 "Runtime이 도커 소켓으로 컨테이너 감독(재시작·로그)" 옵션까지 |

### 16.2 Provider 인터페이스
```
read(ref)            -> actual state
plan(desired, actual)-> diff (호환/파괴 판정에 재사용)
apply(plan)          -> events
health(ref)          -> status
```
- `apply`는 배정 Runtime이 실행하는 특수 Step. 즉 외부 설정 변경도 Run/Step/이벤트이며 되돌리기(이전 스냅샷 재적용)가 따라온다.
- 드리프트(콘솔에서 직접 변경)는 주기적 `read`로 감지해 `drift` 신호(7절 신호에 추가).
- **프로바이더는 얇게**: 리소스 종류를 명시적으로 제한하고, 커버 밖 설정은 제품 콘솔에서 하되 드리프트로 감지만 한다. 전부 관리하려 들면 Terraform이 된다.

### 16.3 계약 확장
```json
{ "kind": "external", "key": "identity/keycloak",
  "impl": { "openapi_ref": "..." },
  "managed": { "provider": "keycloak", "secretRef": "ambient://aws",
               "spec": { "realm": "...", "clients": [...], "roles": [...], "idps": [...] } },
  "health": { "url": "https://.../health/ready", "interval_s": 30 } }
```

### 16.4 첫 프로바이더
- **keycloak**: realm · client · role · identity provider 4종으로 고정.
- **postgres**: role · schema · grant.
- **oidc-client**: 범용 OIDC 클라이언트 등록/검증(IdP 무관).
- Portal 자체 로그인을 관리 대상 Keycloak/OIDC로 붙인다(dogfooding).

### 16.5 AWS
**run-on**(우리가 AWS 위에서):
- Portal = ECS 서비스 1태스크 + RDS. Runtime = Group 배정 단위별 ECS 서비스 또는 EC2 바이너리. 프라이빗 서브넷 + 아웃바운드만(14절 원칙).
- 조인 토큰은 SSM, AWS 자격증명은 태스크 역할(`ambient://aws`).
- 관측: OTel 익스포터로 CloudWatch/X-Ray에 동일 트레이스 전송. CloudWatch 로그 흡수는 v1.
- 배포 모듈: ECR 이미지 + Terraform/CDK 모듈 제공.

**run-with**(AWS 서비스를 우리 안으로):

| 우리 개념 | AWS | 비고 |
|---|---|---|
| `event` 트리거/큐 | SQS · SNS · EventBridge | Runtime Scheduler가 폴링/구독 |
| `cron` | EventBridge Scheduler | 우리 cron 기본, EB 선택 |
| `store` | S3 · DynamoDB · RDS | |
| `model` | Bedrock | IAM으로 인증 |
| `external`+Provider | Cognito · IAM 정책 · Secrets Manager | |
| Runtime(pull) | Lambda | v1, 14.0 |
| 블랙박스 | API Gateway · Step Functions | 트레이스로만 표시 |

첫 세트: SQS · S3 · SSM/Secrets · Cognito · Bedrock. 의존성은 aws-sdk-go-v2 하나.

**겹침 원칙**: API Gateway/Step Functions/EventBridge Scheduler는 우리 Gateway/flow/cron과 동일 역할. 우리 것이 기본, AWS 것은 있으면 블랙박스·트리거로 지도에 남긴다. 둘 다 관리하지 않는다.
