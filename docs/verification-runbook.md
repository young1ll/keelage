# 사람 검증 런북 (12주 완료 확인)

12주 표의 완료 기준 가운데 **사람만 할 수 있는 것**과, 그 전에 사람이 눌러야 하는 스위치를 순서대로 적었다. 기계 검증(lint·test·생성물·PG e2e·goreleaser snapshot·govulncheck)은 CI와 `make`가 한다. 예상 시간: 사전 준비 40분 + 검증 60분.

## 0. 사전 준비 (한 번)

| # | 할 일 | 어디서 | 결과 |
|---|------|--------|------|
| 0.1 | LICENSE 결정·추가 | 리포 루트 | 계획서 §1: `keelage` Apache-2.0, 서버는 FSL/BSL 계열. `.goreleaser.yaml` `archives.files`에 `LICENSE*`를 다시 넣는다 |
| 0.2 | **릴리스 태그** | 로컬 | `git tag -a v0.1.0 -m "v0.1.0" && git push origin v0.1.0` → `release.yml`이 바이너리·checksums·SBOM·cosign 번들·`ghcr.io/young1ll/keelage-server` 이미지를 만든다. Actions 탭에서 초록 확인. 먼저 `v0.1.0-rc.1`로 리허설해도 된다(`release.prerelease: auto`라 프리릴리스로 표시되고 `install.sh`의 latest·ghcr `latest`에는 잡히지 않는다; 설치는 `KEELAGE_VERSION=v0.1.0-rc.1`) |
| 0.3 | GitHub **OAuth app** (device 로그인) | github.com → Settings → Developer settings → OAuth Apps | "Enable Device Flow" 체크. Client ID를 `KEELAGE_GITHUB_CLIENT_ID`로 |
| 0.4 | GitHub **App** (PR 코멘트·체크) | Developer settings → GitHub Apps | 권한: Contents read · Pull requests write · Checks write · Metadata read. 이벤트: Installation, Pull request, Pull request review. Webhook URL `https://<server>/webhooks/github`, secret 생성. App ID·secret을 `KEELAGE_GITHUB_APP_ID`·`KEELAGE_GITHUB_WEBHOOK_SECRET`로, private key(PEM)는 `deploy/docker/secrets/app.pem`에 두고 `KEELAGE_GITHUB_APP_KEY=/secrets/app.pem`(컨테이너 안 경로; compose가 `./secrets`를 `/secrets`로 읽기 전용 마운트) |
| 0.5 | 서버 기동 | 아무 Docker 호스트 | `cd deploy/docker && KEELAGE_VERSION=v0.1.0 docker compose up -d` (위 env를 `.env`에). 공개 URL이 필요하면 임시로 `cloudflared tunnel --url http://localhost:8080` 등 |
| 0.6 | org·멤버 | 서버 컨테이너 | `docker compose exec server /keelage-server org create <org>` · `member add <org> <github-login>` (검증자 본인 + 외부 3명) · `key`(공개키 메모) |

## 1. 개인 경로 (외부 사용자 3명 — `docs/metrics/external-users.md`)

절차·7개 확인 항목·기록표는 그 문서에 있다. 핵심 3개: **훅 주입**(편집 직전 제약이 보임) · **fail-open**(데몬 없이 편집이 막히지 않음) · **원상 복원**(`uninstall` 뒤 파일 바이트 동일). 설치는 `curl -fsSL https://raw.githubusercontent.com/young1ll/keelage/main/scripts/install.sh | sh`.

## 2. 팀 경로 (검증자 본인 + 1명, 10–11주차 완료 기준)

```sh
# 데몬 A(본인)
keelage server login --url https://<server> --org <org>      # 브라우저에 코드 입력 (device flow)
cd <repo> && keelage constraint add --scope team=<team> --verify "팀 규칙 한 줄"
keelage sync push                                            # accepted 1
# 데몬 B(다른 계정, 다른 머신 또는 KEELAGE_HOME=/tmp/b)
keelage server login --url https://<server> --org <org>
keelage sync pull && keelage constraint list                 # A의 규칙이 보인다
# 증명
keelage server proof --seq 1 > bundle.json
keelage verify-proof --server-key "$(keelage server key)" --file bundle.json   # VALID inclusion
# 승인 브로커 (에이전트 토큰: docker compose exec server /keelage-server token create <org> cc-1 --kind agent --owner <login>)
KEELAGE_HOME=/tmp/agent keelage server login --url https://<server> --token kl_…
KEELAGE_HOME=/tmp/agent keelage gate ask --scope 'org=|product=|team=<team>|repo=|path=|person=' --proposal p1 --level L1   # open/ask
keelage gate resolve <id> --allow --reason "reviewed"        # 사람 토큰
keelage gate list --state resolved
```

기대: 두 데몬이 제약을 공유, 포함 증명이 서버 키로만 검증, gate의 질문·답이 서버 원장(`GateOpened`·`GateResolved`)에 남는다.

## 3. GitHub App 경로 (12주차 완료 기준)

1. 테스트 리포에 App 설치 → 서버 로그에 `installation` 배달, `keelage-server org list`에 계정 이름의 org.
2. 그 리포에서 커밋 → `keelage change derive && keelage sync push` (`--commit <sha>`로 다른 커밋; 또는 post-commit 훅) → PR 열기.
3. 기대: PR에 `keelage` 코멘트(Change id·닿은 제약·닻·판단 상태)와 `keelage` 체크(`neutral`: 판단 대기).
4. 이유를 적은 Approve → 코멘트가 갱신되고 체크가 `success`, `keelage history`/서버 원장에 `Judged`(source=review, by=리뷰어 login). 이유 없는 Approve는 코멘트에 "needs a reason"이 붙고 원장에 `Rejected(no-reason)`.

## 4. 릴리스 검증 (0.2 뒤)

```sh
cosign verify-blob checksums.txt --bundle checksums.txt.sigstore.json \
  --certificate-identity "https://github.com/young1ll/keelage/.github/workflows/release.yml@refs/tags/v0.1.0" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
cosign verify ghcr.io/young1ll/keelage-server:v0.1.0 --certificate-identity-regexp 'young1ll/keelage' --certificate-oidc-issuer https://token.actions.githubusercontent.com
```

## 5. 기록

- 외부 사용자 표: `docs/metrics/external-users.md`.
- 막힌 곳은 issue로. 도구 규약 차이면 해당 어댑터 `VERSION`의 확인 날짜를 갱신하고 ADR을 추가한다.
- 통과하면 12주 완료. 13주차부터는 계획서 §9의 다음 줄(웹 인박스·Change 뷰)이다.
