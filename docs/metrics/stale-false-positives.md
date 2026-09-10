# stale 오탐률 측정 (4–5주차부터, dogfood)

계획서 §9 4–5주차 완료 기준: "우리 리포에서 stale 오탐률 측정 시작". 닻 해시 소음(§10 리스크)을 숫자로 본다.

## 정의
- **판정**: `keelage verify`가 닻을 `review`(본문 변경) 또는 `stale`(시그니처 변경·소실)로 내린 것.
- **오탐**: 판정을 사람이 보고 "이 닻에 묶인 제약·결정은 여전히 유효하다"고 한 경우.
- 오탐률 = 오탐 / 판정. `review`와 `stale`을 따로 센다. 시그니처 변경은 정의상 stale이므로(스펙 §1) stale 오탐은 "시그니처가 바뀌었지만 제약은 안 낡음"을 뜻한다 — 이 비율이 높으면 시그니처 정규화(ADR 0011)를 손본다.

## 절차 (매주)
1. 닻 기록: `keelage anchor add <anchor>...` — 이 리포는 Go라 v0에서는 **generic(파일 해시)** 으로 강등된다. 따라서 Go 파일의 모든 변경이 stale이 되고, 그 오탐률이 곧 "generic 폴백의 비용"이다. TS 픽스처(`internal/adapter/treesitter` 테스트의 샘플)는 심볼 단위로 측정한다.
2. 커밋마다 `keelage verify --changed --fail-on none --json > /tmp/verify-<sha>.json`.
3. 판정을 아래 표에 옮기고, 각 판정에 대해 사람이 오탐 여부를 적는다.

## 기록

| 주 | ref | 판정(review/stale) | 오탐(review/stale) | 오탐률 | 비고 |
|---|---|---|---|---|---|
| 5 | b9f2f3f | 0 / 2 | 0 / 2 | stale 100% | 첫 측정(모의): `internal/core/scope.go` 끝에 빈 줄, `queries/typescript.scm`에 주석 한 줄 추가 → 둘 다 generic이라 `stale`. 둘 다 오탐(제약 유효). generic 폴백의 구조적 비용 = 기준선. TS 픽스처는 e2e(`cmd/keelage/verify_test.go`)에서 body→review, 시그니처→stale, 주석→file로 분리됨 |

## 오탐이 나오는 알려진 원인
- generic 언어(Go): 파일 어느 곳의 변경도 시그니처 변경 → Go tree-sitter(13주+)까지는 구조적 오탐.
- 파일 닻(`code://path`)은 파싱 언어에서 심볼 표면(시그니처·본문 목록)으로 해시한다 → 파일 안 어떤 심볼의 본문 변경도 `review`.
- 심볼 이름 변경은 이동이 아니라 소실(`unrealized`) + 새 심볼로 보인다 — 시그니처에 이름이 포함되므로 의도된 동작.
