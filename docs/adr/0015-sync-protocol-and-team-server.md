# 0015. 데몬은 서명한 기록을 제안으로 올리고, 서버는 원 서명을 보존한 채 다시 체인하며, 팀 층은 캐시로 내려온다

Status: accepted · Date: 2026-09-10 · Supersedes: 없음 (계획서 §4.3–4.4·패턴 §9의 구체화)

## Context
10–11주차 완료 기준: "둘 이상의 데몬이 팀 제약을 공유, 포함 증명 검증, 에이전트가 `/gates`로 묻고 답이 원장에 남음". 스펙은 push/pull의 모양(`origin{daemon_id, local_seq}`, 서명 검증, 커맨드 재검증→확정/Rejected, 커서=서버 seq)을 정하지만, 다음은 구현이 정해야 했다: 서버가 다시 체인한 기록에서 데몬 서명을 어떻게 검증 가능하게 남기는가, 두 원장이 같은 바이트를 해시하려면 무엇을 맞춰야 하는가, 로컬 행위자 ID와 서버 사용자를 어떻게 잇는가, 충돌은 v0에서 어디까지 다루는가, 로그인은 무엇으로 하는가.

## Decision
- **서명 보존과 origin**: 서버 원장은 자기 `prev_hash`로 다시 체인하되(`hash`는 서버 것), `meta_hash`·`body_hash`·데몬 `sig`를 그대로 저장하고 `origin{daemon_id, local_seq, prev_hash}`에 **데몬 쪽 prev_hash**를 넣는다. `Envelope.OriginHash() = H(origin.prev_hash ‖ meta_hash ‖ body_hash)`가 데몬이 서명한 값이므로, 서버 사본만으로 데몬 서명을 다시 검증할 수 있다. 서버가 파이프라인으로 직접 만든 기록(gate)은 서버 키로 서명한다.
- **타임스탬프 정밀도**: `Envelope.Seal`이 TS를 마이크로초로 자른다. SQLite(text)·Postgres(timestamptz) 모두 그 정밀도를 저장하므로 같은 기록이 두 원장에서 같은 `meta_hash`를 낸다.
- **원장 유일성**: 서버는 `(org, origin.daemon_id, origin.local_seq)` 유일 인덱스로 재푸시를 중복(accepted, duplicate)으로 처리한다. 데몬 id는 데몬 키의 지문(`ed25519:sha256[:8]`)이며 `POST /sync/register`가 (org, id) → 공개키·사용자로 묶는다. 같은 id에 다른 키는 409(키 회전 = 새 id, v0).
- **push 검증 순서**(서버, org 단위 직렬화): origin 중복 → 데몬 체인 검증(`Verify(prev_hash)`) → 데몬 서명 → 공유 가능 스트림(constraint·decision·scope·anchor·change·gate; session은 서버가 거부) → 행위자(사람이면 push한 사용자 본인, 에이전트면 owner가 그 사용자) → 코덱 디코드 → `expected = ver-1`로 append. 해시·서명 실패는 응답에만(인증 실패), 나머지 거부는 서버 `rejected` 스트림에 `Rejected{command: "sync:<Kind>"}`로 남긴다. 버전 불일치(`version-conflict`)도 그렇다. 커맨드 핸들러를 다시 돌리는 재검증은 하지 않는다(이벤트→커맨드 역변환은 v1; 서명·행위자·연속성·스키마 검증이 v0의 재검증이다).
- **데몬 쪽 필터**: person 축이 있는 제약·scope는 올리지 않는다. 세션은 `SessionShared` 이후(공유 목록이 비어 있지 않을 때)만 스트림 전체를 올린다. `rejected` 스트림은 올리지 않는다. push 커서는 서버가 답한 곳까지 전진하며 거부는 로그로 남기고 재시도하지 않는다(스펙 v0 단순화).
- **pull과 팀 캐시**: `GET /sync/pull?since=`는 constraint·decision·scope·anchor 스트림만 돌려주고 커서는 마지막으로 **훑은** 서버 seq다. 데몬은 자기 origin의 기록을 건너뛰고 나머지를 `~/.keelage/cache/team.db`(서명 없는 SQLite 원장, origin 보존)에 `AnyVersion`으로 붙인 뒤 같은 투영에 적용한다. 기동 시 개인 원장 → 팀 캐시 순으로 재생하고, `catchUp`은 둘 다 따라간다. 같은 스트림을 두 데몬이 로컬에서 나란히 쓰면 뒤에 올린 쪽이 `version-conflict`로 거부된다(정정은 v1).
- **정체성**: `keelage server login` 뒤에는 서버 사용자명(GitHub login)이 로컬 행위자 ID가 된다(헤드리스 커맨드의 사람 행위자, 훅 캡처의 에이전트 owner). 로그인 전 기록은 git 이메일/OS 사용자 ID라 push에서 `unauthorized`로 거부되고 기록된다.
- **로그인**: GitHub OAuth **device flow**(RFC 8628; `adapters/github/VERSION`에 확인일)를 서버가 대행한다. `POST /auth/device/start`는 GitHub의 코드를 그대로 돌려주고, `POST /auth/device/poll {org, device_code}`가 승인 시 `/user`의 login으로 `member` 표를 확인해 사람 토큰을 발급한다. 서버는 단계 사이에 상태를 두지 않고 GitHub 토큰을 저장하지 않는다. CI·에이전트는 `keelage-server token create --kind agent|ci --owner <사람>`으로 발급한 토큰을 `--token`으로 쓴다. 토큰은 sha256만 저장한다.
- **gates**: `POST /gates`는 토큰의 principal이 행위자다(agent 토큰이면 `agent:<user>` owner=사람). 같은 (행위자, proposal_ref)는 같은 gate(`GateIDFor`). `ask`면 사람 토큰의 `POST /gates/{id}/resolve`. 두 기록 모두 서버 원장에 서버 서명으로 남는다. gate·change 스트림은 pull 대상이 아니다(데몬 투영이 서버 스키마를 흉내 내지 않는다).
- **증명**: `GET /ledger/proof/inclusion?seq=`는 헤드가 마지막 체크포인트를 지났으면 즉시 새 체크포인트를 서명하고 번들을 준다. 체크포인트 워커는 주기(기본 10분)마다 움직인 org만 서명한다. `keelage server proof --seq N | keelage verify-proof --server-key …`가 오프라인 검증 경로다.

## Consequences
e2e(`cmd/keelage/sync_test.go`, Postgres 필요)가 완료 기준 세 가지를 한 번에 확인한다: alice의 제약이 push→bob pull→`constraint list`로 보이고(개인 제약은 남고), seq 1의 포함 증명이 서버 키로만 검증되며, 에이전트 토큰의 `gate ask`→사람의 `gate resolve`가 서버 원장 두 기록으로 남는다. 서버 사본에서 데몬 서명이 `OriginHash`로 검증된다. 12주 밖: 이벤트→커맨드 역변환 재검증, 로컬 정정, 키 회전, 서버 클론 계산, SSE 롱폴(pull은 페이지 폴링).
