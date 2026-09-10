# 0012. 훅 어댑터는 데이터만 매핑하고, 차단은 `deny-edit` 자율 제약이 명시할 때만, 데몬은 원장 catch-up으로 CLI 쓰기를 본다
Status: accepted · Date: 2026-09-10
## Context
6–7주차: Claude Code 훅 파이프라인·시뮬레이터·로컬 MCP. 공식 문서(code.claude.com/docs/en/hooks, 확인 2026-09-10)로 규약을 재확인했다: 입력은 stdin JSON(`session_id`·`cwd`·`hook_event_name`·`tool_name`·`tool_input`), 출력은 exit 0 + stdout JSON(`hookSpecificOutput.permissionDecision|additionalContext`, `systemMessage`), 타임아웃 단위는 초, 타임아웃된 훅은 차단하지 않는다(우리 fail-open과 같은 방향). 스펙 §6은 "L0=경고·주입, 차단은 자율 제약이 명시할 때만"이라고만 했고 "명시"의 형식이 필요했다. 또 CLI(`constraint add`)와 데몬이 같은 SQLite 원장을 쓰는데 데몬 투영은 메모리라 CLI 쓰기를 못 본다.
## Decision
- **어댑터 위치**: 스펙의 `adapters/<tool>/`는 코드에서 `internal/adapter/<tool>/`이다(depguard 경계 안). VERSION·`hooks.json`·`skills/keelage/SKILL.md`를 임베드하고 `keelage adapter claude-code print hooks|skill|mcp|version`으로 꺼낸다. 어댑터는 도구 이벤트 → 정규 이벤트 6종 매핑과 응답 역매핑만 가진다(패턴 §6).
- **차단 형식**: `kind: autonomy` 제약의 `checkable == "deny-edit"`가 유일한 차단 선언이다. 그 범위 안에서 에이전트의 pre_edit은 `permissionDecision: deny`(사유 = 제약 본문)로 답한다. 그 외 모든 제약·닻 상태는 `additionalContext`(+ 경고는 `systemMessage`)로만 간다. 차단을 지원하지 않는 이벤트(PostToolUse)에서는 컨텍스트로 강등.
- **예산**: `keelage hook`은 전체 50ms(dial 포함) 후 무출력·exit 0. 데몬 쪽은 40ms 데드라인. pre_edit는 투영만 읽고 파싱하지 않는다.
- **catch-up**: 데몬은 훅·질의마다 `ledger.Head()`를 보고 새 seq가 있으면 그만큼만 재투영한다(`app.Replay`). CLI 쓰기가 즉시 보이고, 데몬 재시작이 필요 없다.
- **시뮬레이터**: `testdata/hooks/claude-code/*.jsonl`은 공식 스키마로 **합성**한 시퀀스다(실세션 녹화 아님). 골든은 `-update`로 재생성. 리포 id는 루트 디렉터리 이름이므로 픽스처는 고정 이름을 쓴다.
- **MCP**: 공식 Go SDK(v1.7.0) stdio. `what_touches(anchor, repo?)`·`related(id)`는 데몬 UDS API의 얇은 클라이언트. 데몬 부재는 도구 오류 메시지로(크래시 없음).
- **프롬프트 원문**: `UserPromptSubmit`의 텍스트는 정규 이벤트에 실려 데몬까지 가지만 세션 레지스트리는 개수만 남긴다. 8주차 턴 분류도 메모리에서만 쓴다.
## Consequences
Claude Code에서 편집 전 제약이 주입되고, 명시된 범위에서만 차단된다. 훅이 없는 도구는 스킬 문안대로 `what_touches`를 부른다. 실세션 녹화본이 생기면 픽스처와 VERSION 확인 날짜를 함께 올린다. 리포 id가 디렉터리 이름이라 같은 이름의 리포가 둘이면 제약 범위가 섞인다 — 서버 sync(10–11주차)에서 remote 기반 id로 바꾼다.
