# AI 시대 팀 워크벤치 (가칭) — 통합 스펙 v0

> AI가 코드·인프라·문서를 쓰는 팀에서, 사람이 여전히 시스템을 이해하고 책임지며 지속 개발할 수 있게 하는 도구.
> 단위는 코드가 아니라 **변경(Change)** — 의도 → 제안 → 판단 → 배포 → 결과. 사람은 의도를 말하고 판단을 내리는 두 지점에 있다.
>
> 이 문서는 [[knowledge-layer-spec-v0]]와 [[engineroom-spec-v0]]를 대체한다. 두 문서는 각각 Context 면과 (후속) 런타임의 상세 참고로만 남긴다. 예측 레인은 별개 제품이며 이 문서의 범위 밖.

## 0. 확정된 결정

| 항목 | 결정 |
|---|---|
| 단위 | 변경(Change). 문서도 코드도 아님 |
| 구조 | 네 면 — Context / Watch / Change / Work |
| 원칙 | 수용, 발명 아님(도구 원본 파일·형식 그대로) / verified-only context / 마스터 하나·뷰는 오버레이 / 얇은 닻 해석기 |
| 에이전트 | **만들지 않는다.** Work는 사용자가 이미 쓰는 에이전트(Claude Code·Codex 등)에 작업과 컨텍스트를 넘기고 결과를 Change로 회수 |
| 개인/팀 경계 | 세션은 개인, PR은 팀. 개인 층은 머신을 떠나지 않고 팀 층은 개인 층을 덮지 않음 |
| 배치 | 각자 로컬 데몬(+로컬 MCP) + 원격 팀 서버 하나. 원격 MCP는 v1 이후(CI·클라우드 에이전트용) |
| 원천 | 리포에 있는 것(팀 문서)은 리포가 원천, 리포에 없는 것(이벤트·판단·finding·제품 층)은 서버가 원천 |
| 결정(ADR)의 생성 | 따로 쓰지 않는다. 판단의 이유에서 추출·승격 |
| 낡음 판정 | 닻 해시 3층(signature/body/file) + review/stale 2단계 + 커밋 단위 묶음. 시그니처 변경은 무조건 stale |
| 첫 사용자 | AI 세션을 매일 여는 개인 개발자. 서버 없이 시작 → PR을 열 때 팀으로 |
| 지표 | 사용자 수·확산 > 결제. 핵심 명제 검증: 판단 축적 후 에이전트 제안의 수용률↑·회귀율↓ |
| 코어 언어 | Go(데몬·서버 단일 바이너리). UI는 React. SDK/어댑터는 TS·Python |

비목표(v0): 에이전트 런타임, 자체 실행 런타임, 원문(PDF 등) 컴파일, 원격 MCP, 사람별 판단 채점(§8.2), 자동 머지.

---

## 1. 네 면과 루프

| 면 | 역할 |
|---|---|
| **Context** | 개인·팀·제품 층의 설정·규칙·md·스킬·ADR/AIP를 유지·합성해 각 AI 도구에 원본 형식으로 공급 |
| **Watch** | 코드·인프라·문서를 계약·결정·불변식·관측에 대해 감시. 산출 = finding(어느 파일 어느 라인, 무엇이, 왜) |
| **Change** | 의도 → 제안 → 판단 → 배포 → 결과의 기록. 판단에서 결정·규칙이 파생 |
| **Work** | finding·의도를 작업으로 만들어 사람 또는 에이전트에 컨텍스트 묶음과 함께 배분, 결과를 Change로 회수 |

루프: Watch → finding → Work → (Context 부착) → 에이전트/사람 → 제안 → Change(판단) → 배포 → Watch(결과) → Context(결정 축적).

---

## 2. 작업 모드

Change는 네 모드를 모두 담는다.

| 모드 | 의도 | 제안 | 판단 위치 |
|---|---|---|---|
| 사람이 직접 씀 | 없음(추론) | 사람 | PR 리뷰 |
| **사람 + AI 세션** | 첫 프롬프트 | AI 편집 | **세션 안**(수락·거부·되돌림) + PR |
| 위임된 에이전트 작업 | Work가 넘긴 것 | 에이전트 | PR 리뷰 |
| 자율 에이전트 | 없음/도출 | 에이전트 | PR 리뷰 |

두 번째 모드가 현재 가장 흔하며, PR은 이미 끝난 판단의 요약본이다. 세션을 잡지 못하면 가장 가치 있는 판단(거부·되돌림·방향 전환)을 잃는다.

---

## 3. 데이터 모델

### 3.1 닻 (Anchor)
```
code://<path>#<symbol>   route://<METHOD> <path>   ui://<Component>
schema://<Type>          infra://<tool>:<resource>  doc://<doc_id>
contract://<group>/<key> (후속 런타임)
```
- 정체성: 닻 문자열 + 해석기가 추적하는 심볼 이동(`AnchorMoved`). 라인 범위는 finding에만 붙고 닻 자체는 심볼 단위.
- 해시 3층: `signature`(공개 표면) / `body`(AST 정규화, 주석·공백 무시) / `file`. 첫 해석기: TS/JS, Python, Go, React 컴포넌트, generic(경로).

### 3.2 Change
```
Change
  id, mode(§2), repo(s)
  intent        {text, source: human|issue|inferred, inferred: bool}   -- "의도 없음"도 명시 상태
  sessions[]    §3.3 (0..N)
  proposals[]   {diff_ref, actor{human|ai, model, prompt_ref}, at}
  impact        {contracts[], decisions[]{ref, relation: references|conflicts}, invariants[], anchors[], graph}
  judgment      {decision: accept|modify|reject, reason, by, at, source: session|review}[]
  verification  {tests, checks, compat}
  deployment    {snapshot|commit, at}
  outcome       {window, signals[], verdict: confirmed|regressed|unknown}
  derived[]     결정·규칙·불변식 ID
  state         proposed → judged → deployed → observed → settled
```
AI 컨텍스트에 들어가는 것은 `settled` Change의 `derived`와 닻 이력.

### 3.3 Session (개인 층, 로컬)
```
Session
  tool          claude-code | cursor | codex | …
  actors        human + ai(model)
  intent        첫 프롬프트 또는 명시 (inferred 가능)
  touched[]     편집이 일어난 닻
  judgments[]   수락·거부·되돌림 + (있으면) 이유
  summary       AI 생성 요약. 원문 대화 미포함
```
캡처 우선순위: ① 도구 훅 ② MCP 도구(`intent`, `decision` — 팀 스킬로 에이전트가 스스로 기록) ③ 커밋 트레일러(`Change-Id`, `Intent:`) ④ PR diff에서 추론(`inferred`).
공유: PR을 열 때 사용자가 선택. 기본 = 의도+요약, 판단은 선택. 원문은 절대 올라가지 않는다.

### 3.4 Decision / Rule / Invariant (Context 팀·제품 층)
- `decision`(ADR): 판단 이유에서 승격. `supersedes`는 충돌 판단의 수용 시 자동. 본문 형식 MADR.
- `rule`: 주로 **거부** 판단에서 승격("이렇게 하지 마라"). 검사 스펙을 가지면 Watch가 실행.
- `invariant`: 반복 회귀에서 추출. 계약·테스트·정책으로 검사 가능해야 한다.
- 승격 트리거: 같은 닻에 같은 취지 판단 N회(기본 3) → 제안 → 사람 확인.
- `guide`/`concept`는 허용하되 방어 대상이 아님(코드에서 재도출 가능). `concept` 형식은 OKF.
- 상태: `generated → verified → review → stale → retired`. `review`는 컨텍스트에 남되 `[review]` 표시, `stale`은 제외.

### 3.5 Finding (Watch)
```
Finding {anchor, lines, kind, evidence, severity, source, change_ref?}
```
kind v0(6): `doc-stale`(시그니처/본문 변경 대비), `contract-violation`, `decision-conflict`, `ai-unreviewed`(세션 수락 없음), `regression`(배포 후), `no-test`.
원천 v0: git diff·닻 해석기, CI 결과, 배포 웹훅, 오류율 웹훅(Sentry/Datadog 등). 자체 런타임은 후속 원천.

### 3.6 Work
```
Work {origin: finding|intent, anchors[], context_bundle, assignee: human|agent{tool}, status, result → Change}
```
context_bundle = 해당 범위의 결정·규칙·불변식·스킬 + 닻 이력("이 닻을 마지막으로 건드렸을 때"). 우리는 에이전트를 실행하지 않고 넘긴다.

### 3.7 이벤트 (append-only, 서버 원천)
Context: `DocImported/Drafted/Verified/Staled/Retired`, `DecisionPromoted`, `RulePromoted`, `InvariantExtracted`
Change: `ChangeOpened`, `IntentSet`, `SessionShared`, `ProposalAdded`, `ImpactComputed`, `Judged`, `Deployed`, `OutcomeRecorded`, `Settled`
Watch: `FindingRaised/Resolved`, `AnchorMoved`
Work: `WorkCreated/Dispatched/Returned`

---

## 4. 배치 — 로컬 데몬 + 원격 서버

```
로컬 머신 (팀원 각자)                          원격 팀 서버 (셀프호스트 바이너리 / 호스팅)
┌────────────────────────────┐   sync(커서)   ┌────────────────────────────────┐
│ 데몬                        │◄─────────────►│ 이벤트 로그(원천) + 투영          │
│  개인 층 Context (로컬만)     │               │ 팀·제품 층 Context 인덱스         │
│  세션 캡처(훅) → 개인 층      │               │ Change · Watch · Work            │
│  닻 해석기 · verify           │               │ GitHub App · 배포/오류율 웹훅     │
│  로컬 MCP = 팀 ∪ 개인 합성    │               │ UI(React embed)                 │
│  도구 파일 실체화(sync)       │               │ (v1) 원격 MCP — CI·클라우드 에이전트│
└────────────────────────────┘               └────────────────────────────────┘
       ▲ Claude Code · Cursor · Codex (로컬만 안다)
```
- 도구는 로컬 MCP와 로컬 파일만 본다. 서버는 MCP 프로토콜 변화와 무관.
- 팀원 A의 판단 → A 데몬 → 서버 → B 데몬 → B 로컬 MCP. 실시간 불필요.
- 서버 없는 기간 허용: 데몬만으로 개인 층·세션·로컬 MCP·verify 동작. sync 대상이 없을 뿐 프로토콜은 처음부터 있음.
- 팀 문서 원천 = 리포(git). 서버는 인덱스+이벤트. 제품 층(여러 리포 걸침)만 서버 원천.
- CI에서 컨텍스트가 필요하면 데몬을 헤드리스로 띄운다(원격 MCP 대체).

---

## 5. Context 면 상세 (요지 — 상세는 knowledge-layer-spec-v0)
- 범위 계층: 조직 → 팀 → 제품 → 리포 → 경로(glob) → 개인. 좁은 범위 우선. 스킬도 같은 계층.
- 어댑터: Claude Code / Cursor 먼저, AGENTS.md·Copilot·Gemini 다음. 규약은 구현 직전 공식 문서로 재확인, 어댑터 버전 명시.
- 실체화: 포인터 분리(import 있는 형식) / managed 블록(없는 형식) / 파일 단위(Cursor mdc). 사이드카 `.kb/manifest.json`으로 분기 감지.
- 렌더 포함 조건: `verified`(+`review`) && `audience ∋ ai`.

---

## 6. Change 면 상세
- 영향 계산: diff → 닻 → 계약(시그니처 diff) · 결정(참조/충돌: 결정의 닻과 교집합 + 취지 대조는 AI) · 불변식 · 영향 그래프(반경 2).
- 판단 이유: AI가 diff·논의·의도·세션 판단으로 초안 → 사람 확인/수정. "LGTM"만 있으면 결정 파생 없음(초안 질이 제품 질).
- 결과 귀속(v0, 보수적): 명시적 되돌림 / 같은 닻을 다시 건드리는 핫픽스 / 배포 창 내 오류율 급등만. 통계적 귀속은 후속.
- 리뷰 화면: diff + "세션에서 X 거부·Y 수락, 이유 Z" + 영향 + 닻 이력.

---

## 7. UI (서버 포털 + 로컬 미니 UI)

문법 고정: 색=범위, 테두리=상태, 점선=로컬/미검증. 상태 칩 동일. 범례 추가 금지.

1. **인박스** — 판단 대기 Change, finding(원인·라인·diff), `generated` 문서, `diverged` 파일. 한 번에 한 항목. 커밋 단위 묶음.
2. **컨텍스트 미리보기** — `resolve(repo, path, tool)` 결과를 도구별로. 각 줄의 출처(문서·범위). "AI가 지금 무엇을 읽는가".
3. **닻 이력** — 어떤 닻을 골라 그 닻의 변경·판단·결과·finding 타임라인. Work의 context_bundle과 동일 데이터.
4. **문서 뷰** — 결정·규칙·불변식 렌더, `supersedes` 체인, 파생된 Change 링크.
5. **변경 영향** — 변경 파일 ← 닻 ← 계약/결정/불변식 3층. PR 코멘트의 원본.
6. **지식 그래프** — 탐색용. 범위/디렉터리 클러스터, 필터, 반경 2 뷰. 작업 화면 아님. 후속에 계약 지도(방사형)로 발전.

---

## 8. 프라이버시·소유
### 8.1 층 경계
개인 층(세션 원문·개인 규칙·개인 판단)은 로컬만. 팀으로 가는 것은 PR 시점에 사용자가 고른 구조화 사실뿐.
### 8.2 채점
판단 채점은 **모델별·닻별**로만. 사람별 집계·표시는 만들지 않는다(인사 데이터화되는 순간 이유를 안 쓴다).
### 8.3 형식 공개
Change·Session·Finding·Decision 데이터 형식은 공개(JSON Schema). "우리 DB에 종속된 자산" 거부감 제거. 구현·호스팅은 유료 후보.

---

## 9. 첫 절단 (v0)

1. **데몬**: 닻 해석기(TS·Python·generic) + Context 개인 층 + Claude Code·Cursor 어댑터 실체화 + 세션 캡처(훅·MCP 도구·트레일러) + 로컬 MCP + `verify`. 서버 없이 동작.
2. **서버**: 이벤트 로그 + sync + GitHub App(PR → Change: 영향·판단 초안·이유 확인) + 배포 웹훅(결과 창) + 인박스·미리보기·닻 이력 UI.
3. **Watch v0**: `doc-stale` · `ai-unreviewed` · `decision-conflict` 세 종류. 이후 `regression`(웹훅) · `no-test` · `contract-violation`.
4. **Work v0**: `work dispatch` 명령 하나 — finding/의도 + context_bundle을 Claude Code 세션으로 넘기고 결과 Change를 연다.
5. 결정 승격(같은 닻 3회) + 규칙 승격(거부에서).
6. 어댑터 확장, Go·React 닻, 지식 그래프, 제품 층.
7. (후속) 자체 런타임을 Watch 원천·`contract://` 닻으로 합류. 그래프 UI → 계약 지도.

**핵심 명제 실험(우리 리포에서 먼저)**: 3개월 축적 후 에이전트 제안의 수용률·회귀율 변화. 변화 없으면 명제가 틀린 것.

---

## 10. 비전과 가정

### 10.1 결과물
- 6개월: "AI가 만든 PR을 겁 없이 머지할 수 있게 됐다"는 말이 나와야 한다.
- 18개월: 판단이 채점되고 닻 이력이 AI의 기본 컨텍스트가 된다. Work 루프가 판단 외 무개입으로 한 바퀴 돈다. 제품 층이 생기고 아키텍트가 그 층을 관리한다.
- 3년: 자체 런타임 합류, 계약 지도. 조직 자산이 코드 저장소에서 "결정과 제약의 저장소"로 이동. 신입은 판단 기록을 검토하며 배운다.

### 10.2 참이어야 하는 가정과 반증 신호
| 가정 | 반증 |
|---|---|
| AI 변경은 여전히 사람이 판단해야 한다고 느낀다 | 자동 머지가 일반화되고 문제가 없다 |
| 판단의 이유는 캡처 가능하고 축적되면 AI 오류가 준다 | 3개월 후 수용률·회귀율 불변 |
| 결정·규칙은 코드에서 도출 불가라 남는다 | 모델이 코드만으로 팀 규칙을 재구성한다 |
| 결과를 변경에 귀속시킬 수 있다 | 귀속 정확도가 낮아 채점이 소음 |

### 10.3 해자
코드베이스별로 쌓이는 판단·결과 말뭉치(시간) / 도구·리포·배포·팀을 가로지르는 위치(범위) / 개인 층 불침범으로 개인이 팀에 들여오는 경로(채택).

### 10.4 직군 전망(제품 형태의 근거)
아키텍트 확대(제약을 실행 가능한 형태로 쓰는 역할) / 개발자→시스템 관리인(지정·검증·운영, 운영과 경계 소멸) / 검증이 중심 직무 / 주니어 경로 재편 / PM·PO는 만들 수 있지만 운영 벽 — 이 도구의 2차 대상.

---

## 11. 열린 질문
- 결과 창(outcome window)의 길이와 회귀 정의 — 소음 설계.
- 도구 훅의 실제 지원 범위(도구별 확인 후 어댑터 버전에 기록).
- "의도 없음" 변경의 결정 파생 허용 여부.
- 개인→팀 전환 UI: PR 열기 시 공유 선택의 기본값과 마찰.
- 이름. 대외 서사 후보: "AI가 만든 변경을 이해하고 책임질 수 있게".
