# 0016. GitHub App은 데몬이 올린 Change만 비추고 리뷰의 이유를 판단으로 기록하며, 릴리스는 태그→goreleaser→keyless 서명이다

Status: accepted · Date: 2026-09-10 · Supersedes: 없음 (계획서 §4.5·§6의 구체화)

## Context
12주차 완료 기준: "GitHub App PR 코멘트(영향·판단 기록) + install.sh·릴리스 파이프라인(goreleaser·cosign) + 외부 사용자 3명". 스펙 §4.5는 "서버 클론 계산 없음: 영향은 데몬이 push 시 올려 둔 서명 결과를 쓴다"와 "review submitted: 이유 확인 → Judged"를 정한다. 정해야 했던 것: PR과 Change를 잇는 키, 코멘트/체크의 의미, 리뷰어 신원과 이유 없는 승인, 설치 ↔ org 매핑, 서명 방식.

## Decision
- **키는 PR head 커밋 SHA**: 데몬의 `Change`(commit 도출, ADR 0013)의 `proposal.ref`가 SHA이고 `change/*` 스트림은 push 대상이므로, 서버는 `ChangeIndex.ByRef(head_sha)`만 본다. 없으면 코멘트로 `keelage change derive <sha> && keelage sync push`(또는 Action)를 안내하고 체크는 `neutral`. 서버는 리포를 클론하거나 계산하지 않는다.
- **코멘트 하나를 갱신**: `<!-- keelage:pr -->` 마커가 있는 코멘트를 찾아 PATCH, 없으면 POST. 내용: Change id·kind/mode·state·적용 자율 수준·판단 수·세션 수, 닿은 제약(id·kind·state·본문 첫 줄), 닻(상태). 체크 런 `keelage`: `success`(판단됨 또는 영향 없음) / `neutral`(판단 대기 또는 기록 없음). v0에서 체크는 PR을 실패시키지 않는다 — 판단은 하네스가 하고 CI가 대신하지 않는다.
- **리뷰 → Judged**: `approved`→accept, `changes_requested`→reject, `commented`/`dismissed`는 판단이 아니다. 리뷰어(login)는 org 멤버여야 한다(아니면 무시). 커맨드는 `Judge{Idem: "gh-review:<id>", Judgment{ID: "ghr-<id>", Reason: 본문, Source: review, By: human(login)}}`이므로 재전송은 멱등. 영향 있는 Change에 이유 없는 승인은 애그리게이트가 `no-reason`으로 거부하고(Rejected 기록) 코멘트에 이유를 요청하는 한 줄을 붙인다.
- **installation ↔ org**: `installation` 웹훅(created)이 installation id를 `installation.account.login`을 id로 하는 org에 묶는다(org 생성 포함, `installation` 표). PR 이벤트의 org는 그 매핑, 없으면 `repository.owner.login`. 수동은 `keelage-server org link-installation`.
- **인증·검증**: App 인증은 RS256 JWT(표준 라이브러리, iat−60s, exp+9m) → installation token(만료 2분 전까지 캐시). 웹훅은 `X-Hub-Signature-256` HMAC(서버 비밀, 상수 시간 비교) 없이는 401, 미설정이면 503. 규약은 `adapters/githubapp/VERSION`(OpenAPI 설명서·docs 원문, 2026-09-10).
- **릴리스**: `v*` 태그 → `release.yml` → goreleaser v2: `keelage`·`keelage-server` (linux/darwin × amd64/arm64, CGO 없음, `-trimpath`, `main.version`), `checksums.txt`, syft SBOM, **cosign keyless** `sign-blob --bundle`(체크섬 파일에 서명; 인증서 identity = `release.yml@refs/tags/<tag>`), `ghcr.io/young1ll/keelage-server` 멀티아치 이미지(distroless nonroot) + `cosign sign`. `scripts/install.sh`는 최신 릴리스를 받아 체크섬을 확인하고 cosign이 있으면 번들을 검증한다(`KEELAGE_REQUIRE_COSIGN=1`로 강제). `deploy/docker/compose.yaml`이 셀프호스트(서버+Postgres) 5분 설치 경로다. `keelage update`(서명 검증 강제)는 12주 밖.
- **외부 사용자 3명**: 코드로 할 수 없는 항목이다. `docs/metrics/external-users.md`에 절차(설치→setup→첫 편집 확인→제거)와 7개 확인 항목·기록표를 두고, 세 명의 3(훅 주입)·4(fail-open)·6(원상 복원)이 통과하면 완료로 본다.

## Consequences
서버는 여전히 리포 내용을 보지 않는다(코드가 서버에 가지 않는다). PR 코멘트의 정보량은 데몬이 push한 만큼이며, push하지 않은 팀은 안내만 받는다. 리뷰 판단은 GitHub login으로 기록되므로 ADR 0015의 정체성(서버 사용자명 = GitHub login)과 일치한다. 이미지 태그·릴리스 저장소는 `young1ll/keelage`로 고정(fork는 `KEELAGE_REPO`). 12주 밖: PR 커밋 전체 조회(head 외 커밋), `/changes/{id}/impact` Action 경로, deployment_status→Deployed, 결과 창 웹훅, Homebrew tap, `keelage update`.
