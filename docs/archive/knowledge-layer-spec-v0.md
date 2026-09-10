# Knowledge Layer (가칭) — 스펙 v0

> 어떤 코드베이스에든 붙는 **AI-readable 문서 아키텍처**. 새 형식을 만들지 않고, 각 AI 도구가 이미 읽는 규칙 파일(CLAUDE.md, AGENTS.md, .cursor/rules, …)을 원본 그대로 수용해 서버에서 팀 공용으로 관리하고, 로컬에는 도구가 기대하는 파일을 그대로 실체화한다. 로컬 AI는 진짜 파일을 읽으므로 차이를 알 수 없다.
>
> 1단계 제품. 지표는 결제가 아니라 사용자 수·확산. `contract://` 닻과 API-first 런타임([[engineroom-spec-v0]])은 이 층 위에 나중에 붙는다.

## 0. 결정

| 항목 | 결정 |
|---|---|
| 형식 | 새 규칙 형식을 만들지 않는다. 도구 원본 파일이 단위. 메타는 frontmatter(허용 시) 또는 사이드카 |
| 진실 원천 | 서버(팀 범위). 로컬 파일은 해석 결과의 실체화 |
| 전달 | 파일 실체화(주) + MCP(보조) + 도구 훅(옵션) |
| 팀 공용 | 범위 계층 조직 → 팀 → 리포 → 경로(glob) → 개인. 좁은 범위 우선. 개인 층은 상류로 안 올라감 |
| 슬롭 차단 | `generated`는 사람이 `verified`로 올리기 전 AI 컨텍스트에서 제외. 닻 해시 불일치 시 `stale`로 자동 강등·제외 |
| 시각화 | 확인 인박스 → 컨텍스트 미리보기 → 문서 뷰 → 변경 영향 → 지식 그래프 순. 그래프는 탐색용, 인박스는 결정용 |
| 배포 | 오픈소스(형식·CLI·MCP), 서버는 셀프호스트 단일 바이너리(Go) + 호스팅 |
| 지표 | 사용자 수 · GitHub Action 설치 수 · MCP 연결 수 · 도구 간 변환 사용 |

비목표(v0): 문서 편집기(에디터는 사용자 것), 코드 실행, 계약/런타임, 전체 위키 대체, **원문(논문·PDF) 컴파일**(OpenKB 등의 몫 — 우리는 코드가 원천).

---

## 1. 원칙

1. **수용, 발명 아님** — 도구 회사의 규칙을 그대로. 규약이 바뀌면 어댑터만 바꾼다.
2. **파일이 인터페이스** — 로컬 AI에 특별한 통합 없이 동작해야 한다. 오프라인에서도.
3. **verified-only context** — 사람이 확인하지 않은 것, 코드와 어긋난 것은 AI에게 먹이지 않는다.
4. **마스터 하나, 뷰는 오버레이** — 모든 화면이 같은 노드 ID를 쓴다.
5. **얇은 닻 해석기** — 심볼 존재 + 본문 해시만. 정적 분석 전부가 아니다.

---

## 2. 대상 도구와 파일 규약

> 각 도구의 파일 경로·import·중첩·frontmatter 규약은 자주 바뀐다. **구현 직전에 현재 공식 문서로 재확인**하고 어댑터에 버전을 명시한다. 아래는 구조 설계용 목록이지 확정 규약이 아니다.

| 도구 | 파일 | 메타 위치 | 실체화 방식 |
|---|---|---|---|
| Claude Code | `CLAUDE.md` (프로젝트/사용자), import 문법 | 사이드카 | 커밋 파일 = 포인터 + 사람 작성부, 생성 본문은 `.kb/` |
| Codex 등 AGENTS.md 계열 | `AGENTS.md` (디렉터리 중첩) | 사이드카 | managed 블록 마커로 본문 일부 교체 |
| Cursor | `.cursor/rules/*.mdc` (glob·frontmatter) | frontmatter | 파일 단위 생성 |
| GitHub Copilot | `.github/copilot-instructions.md` | 사이드카 | managed 블록 |
| Gemini CLI | `GEMINI.md` | 사이드카 | 포인터 또는 managed 블록 |
| Windsurf 등 | 각 규칙 파일 | 사이드카 | managed 블록 |
| 범용 | `llms.txt` | — | 생성 전용 |
| OpenKB(가져오기 전용) | OKF 위키 페이지 | frontmatter | `concept` 문서(`generated`)로 흡수 → 닻 부착 → 검증. 원문 컴파일은 하지 않음 |

어댑터 인터페이스: `detect(repo) -> files[]`, `parse(file) -> RuleDoc`, `render(resolved) -> files[]`, `conventions: {version, importSyntax?, nesting?, glob?}`.

---

## 3. 데이터 모델

### 3.1 RuleDoc (규칙 문서)
```
id          ULID
scope       {level: org|team|repo|path|person, key}
tool        claude|agents|cursor|copilot|gemini|generic   (원본 형식)
path        원본 파일 경로(리포 기준) 또는 가상 경로(서버에서 생성된 문서)
type        adr | rule | guide | runbook | concept | freeform   (freeform = 분류 안 함)
            concept 페이지 본문 형식은 Google OKF(Open Knowledge Format) 준수 — 새 형식 발명 금지 원칙
title
body        원본 본문 그대로(형식 불변)
anchors[]   4절
state       generated | verified | stale | diverged | retired
verified_at {commit_sha, anchors_hash, by, at}
origin      {actor: human|ai, model?, prompt_ref?, imported_from?}
links       supersedes / superseded_by / related[]
audience    [human, ai]
version     정수. 모든 변경은 이벤트
```

### 3.2 사이드카 `.kb/manifest.json`
리포별. 관리되는 파일마다 `{path, doc_id, rendered_hash, source_scope, adapter_version}`. 로컬 변경 감지·분기 판정의 기준.

### 3.3 이벤트 (append-only)
`DocImported`, `DocDrafted(ai)`, `DocVerified`, `DocStaled(cause: commit, anchor)`, `DocDiverged(local_hash)`, `DocMerged`, `DocRetired`, `ScopeAssigned`, `Rendered(tool, repo, hash)`, `SyncPulled(machine, repo)`.

### 3.4 상태 규칙
- `generated`(AI 초안·자동 변환) → 사람 승인 → `verified`.
- `verified` + 닻 해시 변경 또는 링크 대상 소실 → `stale`.
- 로컬 실체화 파일이 manifest 해시와 다름 → `diverged` (해당 리포·머신 한정 상태).
- 렌더 포함 조건: `state == verified && audience ∋ ai`.

---

## 4. 닻 (Anchor)

```
code://<path>#<symbol>        함수·클래스·모듈 (symbol 생략 시 파일)
route://<METHOD> <path>       HTTP 엔드포인트
ui://<ComponentName>          프론트 컴포넌트
schema://<TypeOrTable>        타입·DB 스키마
infra://<tool>:<resource>     terraform 등
doc://<doc_id>                문서 간 링크
contract://<group>/<key>      (후속) 런타임 계약
```
- 해석기 플러그인: `exists(anchor) -> bool`, `hash(anchor) -> sha`. 첫 세트: TS/JS, Python, Go, React 컴포넌트, 경로 전용(generic).
- 해석 실패 시 경로 수준으로 강등하고 경고. 닻 없는 문서는 허용하되 `stale` 판정을 못 받으므로 "검증 불가" 표시.
- 닻은 frontmatter(가능 시) 또는 사이드카에 명시. 본문 안 경로·심볼 언급에서 자동 추출은 **제안**으로만(사람 확인 후 채택).

---

## 5. 범위 해석과 실체화(sync)

### 5.1 해석
`resolve(repo, path, tool)`:
1. 적용 가능한 RuleDoc 수집: scope가 org/team/repo(해당)/path(glob 매치)/person(현재 사용자).
2. 필터: `verified && audience ∋ ai`.
3. 정렬: 좁은 범위 우선, 같은 범위 내 명시 순서.
4. 어댑터 `render`로 도구 파일 생성.

### 5.2 실체화 방식(어댑터별 택1)
- **포인터 분리**: 커밋되는 파일은 `<import> .kb/<tool>/context.md` 한 줄 + 사람이 직접 쓰는 부분. 생성 본문은 `.kb/`(gitignore).
- **managed 블록**: import가 없는 형식은 `<!-- kb:begin id -->` … `<!-- kb:end -->` 사이만 교체. 바깥은 손대지 않는다.
- **파일 단위 생성**: Cursor mdc처럼 파일이 곧 규칙인 형식은 파일 생성·삭제, 헤더에 출처 표기.

### 5.3 `kb sync`
- 명령: `kb init`(기존 규칙 파일 흡수 → `generated` 또는 `imported-verified` 선택), `kb sync`(pull → resolve → render → manifest 갱신), `kb verify`, `kb render --tool cursor`, `kb serve`(MCP).
- 실행 시점: 수동, git hook(post-checkout/post-merge), 데몬(파일 감시), 도구 세션 시작 훅(지원 도구만).
- 분기 처리: manifest 해시 ≠ 파일 해시 → `diverged` → 사용자 선택: 상류 제안(서버에 초안 생성) / 로컬 유지(개인 층으로 승격) / 덮어쓰기.
- 오프라인: 마지막 pull 결과로 동작. 서버 부재가 로컬 AI를 막지 않는다.

---

## 6. 검증(verify)

세 검사: ① 닻 존재 ② 닻 해시 == `verified_at.anchors_hash` ③ `doc://` 링크 대상 생존. 실패 → `stale` + 원인 이벤트.
- CI: GitHub Action이 PR에서 `kb verify --changed`를 돌려 낡는 문서를 코멘트로("이 변경으로 ADR-0012가 낡습니다: `code://…#issueInvoice`"). 실패 정책은 리포 설정(warn/fail).
- 배지: verified 비율.
- `rule` 타입은 본문에 검사 스펙(lint 규칙 참조)을 가질 수 있으며 verify 단계에서 실행(v1).

---

## 7. MCP · 훅

- MCP 서버(`kb serve`, 로컬 또는 서버): 리소스 = verified 문서(범위 해석 적용). 도구 = `search`, `related(anchor)`, `status(doc)`, `draft(doc)`(AI가 초안 제출 → `generated`), `explain_context(repo,path,tool)`(왜 이 줄이 포함됐는가).
- 훅: 지원 도구의 세션 시작 시 `kb sync` 실행 또는 최신 컨텍스트 주입. 도구별 가능 여부는 어댑터 버전에 기록.

---

## 8. UI (서버 포털)

우선순위 순. 문법 고정: 색=범위, 테두리=상태, 점선=로컬/미검증. 상태 칩 5종 동일 모양. 범례 추가 금지.

1. **확인 인박스** — `generated` / `stale`(깨진 닻 + 커밋 diff) / `diverged`(로컬 diff). 항목당 승인·수정·폐기. 한 번에 한 항목.
2. **컨텍스트 미리보기** — `resolve(repo, path, tool)` 결과를 도구별 탭으로. 각 줄의 출처(문서·범위)를 색으로 추적. "AI가 지금 무엇을 읽는가"의 유일한 답.
3. **문서 뷰(위키)** — md 렌더, 상태 배지, 닻 → 코드 링크, `supersedes` 체인(ADR 타임라인), 적용 범위(glob).
4. **변경 영향** — 변경 파일 ← 닻 ← 문서 3층 다이어그램. PR 코멘트의 원본.
5. **지식 그래프** — 노드: 문서·닻·범위·도구. 범위/디렉터리 클러스터링, 타입·상태·도구 필터, 한 노드 중심 반경 2 뷰. 전체 조망은 클러스터 수준까지. 작업 화면 아님.

---

## 9. 서버

- Go 단일 바이너리(UI embed) + Postgres. 인증은 OIDC(GitHub/Google/자체 IdP). 조직·팀·리포 매핑은 GitHub App 연동으로 시작.
- 셀프호스트 `docker run` + 호스팅 버전 동일 바이너리.
- 이벤트 로그 append-only + 투영(RuleDoc 현재 상태, 범위 인덱스, 렌더 캐시). 향후 [[engineroom-spec-v0]]의 Portal과 같은 프로세스에 합류 가능하도록 모듈 경계 유지.

---

## 10. 확산 장치

- `kb init` 5분 데모: 기존 CLAUDE.md·ADR·Cursor 규칙 흡수 → 다른 도구 형식으로 즉시 렌더("Cursor 규칙을 Claude Code에도").
- GitHub Action + 배지.
- 슬로건: verified-only context.
- 형식·어댑터·CLI·MCP 오픈소스. 서버도 오픈 코어. 팀 범위 관리·호스팅이 유료 후보이나 v0에서 결제는 측정하지 않는다.

---

## 11. MVP 순서

1. 어댑터 2종(Claude Code, Cursor) + `kb init/sync/render` + 사이드카 — 로컬만, 서버 없음
2. 닻 해석기(TS, Python, generic) + `kb verify` + GitHub Action
3. MCP 서버(`kb serve`)
4. 서버: 범위 계층·이벤트·인박스·미리보기
5. 문서 뷰·변경 영향·그래프
6. 어댑터 추가(AGENTS.md, Copilot, Gemini) + Go·React 닻
7. (후속) `contract://` 닻 → 런타임 스펙과 합류

1~3은 서버 없이 확산 가능한 단위. 4부터 팀.

---

## 12. 위험

- 도구 회사가 원격 규칙 동기화를 자체 제공 → 단일 도구 내 가치 소멸. 가치는 도구 횡단·팀 범위·검증에 둔다.
- 생성 파일과 수동 편집 혼재로 사용자 혼란 → 포인터 분리·managed 블록을 엄격히.
- 닻 해석기 확장 유혹 → 첫 세트 고정, 나머지 경로 강등.
- 문서 도구의 낮은 결제 전환 → 지표를 사용자·확산으로 두기로 결정(0절).
- 규칙 파일 규약 변동 → 어댑터 버전 명시, 구현 직전 재확인.
