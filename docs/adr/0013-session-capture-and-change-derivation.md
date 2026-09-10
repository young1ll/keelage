# 0013. 세션은 턴 단위로 기록하되 원문은 절대 남기지 않고, Change는 커밋마다 하나이며 영향은 닻 또는 범위 전체 제약으로만 계산한다
Status: accepted · Date: 2026-09-10
## Context
8주차: 세션 캡처·턴 분류 폴백·post-commit Change 도출·inbox/history CLI. 스펙 §3.4는 Session의 필드를, §3.4(계획서)는 분류 폴백 규칙을, §1은 "경계=커밋, 제약에 닿지 않는 Change는 판단 없이 settle"을 말한다. 구체 규칙이 필요했던 것: 턴의 경계, 판단 후보의 형태, "제약에 닿는다"의 정의, 세션과 Change의 연결.
## Decision
- **턴** = 프롬프트 하나 + 그 뒤의 편집들. 다음 프롬프트가 부정·대체 패턴(`supply.ClassifyPrompt`: 아니/말고/대신/그게 아니라/no,/not that/instead/undo…)이면 직전 턴은 `redirect`; 편집 뒤에 되돌리기 명령(`git checkout --|restore|reset|revert|stash`)이 오면 그 턴은 `revert`; 나머지는 `accept`. 편집 없는 턴은 기록하지 않는다.
- **원문 금지의 형태**: `TurnRecorded`는 분류·파일·발화 패턴 이름(`ko-negation` 등)만 담는다. 프롬프트 텍스트는 데몬 메모리에서 분류에만 쓰고 버린다. 세션 요약 폴백은 개수뿐("claude-code session: 3 turns, 2 files, 2 judgment candidates").
- **행위자**: 세션 이벤트의 행위자는 에이전트 `<tool>:<session_id>`, owner = 데몬을 돌리는 사용자. 공유(`ShareSession`)만 사람이 하며 `summary`·`judgments`만 고를 수 있다(transcript는 선택지에 없다).
- **Change 도출**: 커밋 하나 = Change 하나(멱등 키 `derive:<sha>`, 제안 ref = sha). 닻 = 파싱 언어면 시그니처/본문이 바뀌거나 생기거나 사라진 심볼(파일층만 바뀐 심볼 제외), generic이면 파일 닻. **영향** = 그 닻에 바인딩된 제약 ∪ 닻이 없는 범위 전체 제약(경로 범위). 닻이 있는 제약은 닻으로만 영향받는다(리포 전체 범위여도 다른 심볼 변경엔 무관). 영향이 비면 즉시 settle.
- **세션 묶음**: 커밋 시각 기준 24시간 안에 같은 파일을 만진 세션 스트림을 `Change.sessions`에 넣고 mode를 `session`으로 한다(`ChangeOpened` v2). 세션 판단 후보는 Change의 Judgment로 자동 변환하지 않는다 — 이유가 없기 때문. inbox에 "판단 후보"로 보여 사람이 붙인다(승격 규칙은 후속).
- **inbox** = 판단 필요한 Change(영향 있음) · stale/review/unrealized 닻 · generated/review 제약 · 판단 후보가 있는 세션. **history** = 닻 스트림 + Refs에 닻이 있는 레코드 + 닻을 언급하는 이벤트(제약 바인딩·Change 영향·gate).
- post-commit 훅은 `keelage change derive`를 헤드리스로 돌리고 커밋을 절대 실패시키지 않는다. 데몬 경유가 아니다(리졸버·git이 필요).
## Consequences
하루 작업 뒤 `keelage inbox`에 판단할 Change와 세션 판단 후보가, `keelage history <닻>`에 그 닻의 전 이력이 보인다. 재현율은 낮아도 된다(스펙 §1: 같은 닻 3회 규칙이 흡수) — 측정은 docs/metrics. 세션 판단을 Change 판단으로 승격하는 흐름과 "같은 닻 3회 → 제약" 승격은 12주 밖의 후속이다.
