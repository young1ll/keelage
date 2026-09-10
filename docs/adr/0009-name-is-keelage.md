# 0009. 제품·리포·바이너리 이름은 keelage다
Status: accepted · Date: 2026-09-10
## Context
계획서 §9는 `kb`가 충돌이 많으니 고유 이름을 4주차 전에 정하라고 했다(설치 스크립트·brew tap·MCP 서버 이름에 필요). 스펙·계획·패턴 문서는 전부 `kb`로 쓰여 있다.
## Decision
이름은 **keelage**. 전면 적용: 리포 `young1ll/keelage`, Go 모듈 `github.com/young1ll/keelage`, 바이너리 `keelage`·`keelage-server`, 홈 `~/.keelage/`(소켓 `keelage.sock`), 리포 사이드카 `.keelage/`, managed 블록 `<!-- keelage:begin -->`, MCP 서버명 `keelage`, 훅 명령 `keelage hook`. 기존 스펙 문서의 `kb`·`kb-server`·`~/.kb`·`.kb/`는 고치지 않고 이 ADR로 읽는다.
## Consequences
코드·설정·문서 신규 작성분은 keelage만 쓴다. 짧은 CLI 별칭은 두지 않는다(충돌 회피가 이름을 바꾼 이유). 스펙 v2 재작성 시 일괄 치환한다.
