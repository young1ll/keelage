# 0002. 원장이 유일한 원천이다
Status: accepted · Date: 2026-09-10
## Context
"리포 md가 원천"은 코드를 실현체로 본 원칙과 모순이었고 GHA 의존을 키웠다.
## Decision
append-only 이벤트 원장(개인=SQLite, 팀=Postgres)이 원천. 리포의 md·CLAUDE.md는 렌더 결과이자 가져오기 소스. 언제든 완전 내보내기(md 렌더러)·형식 공개·셀프호스트로 종속 우려를 해소.
## Consequences
파일 감시·웹 편집·리포 가져오기 세 경로가 같은 커맨드로 원장에 들어간다. 충돌 시 원장이 이긴다.
