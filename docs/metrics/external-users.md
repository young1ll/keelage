# 외부 사용자 3명 — 설치·훅 동작 확인 (12주차 완료 기준)

계획서 §9 12주차: "외부 3명이 설치·훅 동작 확인". 이 문서는 절차와 기록표다. 한 사람당 30분을 넘기지 않는 것이 목표다(설치 5분 + setup 5분 + 첫 편집 확인 10분 + 제거 5분).

## 절차 (사용자에게 그대로 보내는 스크립트)

```sh
# 1. 설치 (릴리스 전: Go 1.25.13+로 소스에서; 릴리스 뒤에는 install.sh)
go install github.com/young1ll/keelage/cmd/keelage@main      # 또는 curl -fsSL https://raw.githubusercontent.com/young1ll/keelage/main/scripts/install.sh | sh
keelage version

# 2. 전역 설정 — 항목별 y/N. 원본은 ~/.keelage/backup/에 남는다
keelage setup
keelage daemon &                     # 또는 별도 터미널

# 3. 아무 TS 리포에서
cd <repo> && keelage init --verify   # CLAUDE.md·AGENTS.md·docs/adr 흡수, 컨텍스트 렌더
keelage constraint add --scope path='src/**' --verify "이 리포의 규칙 한 줄"
# Claude Code로 src/ 아래 파일을 편집 → 편집 직전에 제약이 컨텍스트로 보이면 성공
keelage sessions                     # 턴이 기록됐는지 (원문은 없다)

# 4. 제거 — 사용자 파일이 바이트 동일해야 한다
keelage uninstall
```

## 확인 항목 (사용자별 기록)

| # | 항목 | 기대 | 확인 방법 |
|---|------|------|-----------|
| 1 | 설치 | 2분 내 `keelage version` 출력 | `go install` 성공 (릴리스 뒤: install.sh의 checksum ok / signature verified) |
| 2 | setup | `~/.claude/settings.json`에 우리 훅만 추가, 백업 존재 | `keelage status` |
| 3 | 훅 주입 | 편집 직전 제약 문단이 Claude Code에 보임 | 사용자 스크린샷 또는 `KEELAGE_DEBUG=1` |
| 4 | fail-open | 데몬을 끈 상태에서 편집이 막히지 않음 | `pkill keelage; ` 편집 |
| 5 | 세션 캡처 | `keelage sessions`에 턴·파일 수, 원문 없음 | `~/.keelage/ledger.db`에 프롬프트 문자열 없음(`strings ledger.db \| grep`) |
| 6 | 제거 | settings.json·CLAUDE.md 바이트 동일 | `keelage uninstall` 전후 sha256 |
| 7 | 50ms | 훅 지연 체감 없음 | `KEELAGE_DEBUG=1`의 elapsed |

## 기록표

| 사용자 | 날짜 | OS/arch | 도구 버전 | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 걸린 시간 | 막힌 곳 / 한 줄 |
|--------|------|---------|-----------|---|---|---|---|---|---|---|-----------|------------------|
| (1) |  |  |  |  |  |  |  |  |  |  |  |  |
| (2) |  |  |  |  |  |  |  |  |  |  |  |  |
| (3) |  |  |  |  |  |  |  |  |  |  |  |  |

세 명 모두 3·4·6이 통과하면 12주차 완료 기준을 만족한다. 막힌 곳은 issue로 남기고, 어댑터 규약 차이면 `internal/adapter/claudecode/VERSION`의 확인 날짜를 갱신한다.
