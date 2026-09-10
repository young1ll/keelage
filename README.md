# keelage — AI 시대 팀 워크벤치

사람과 점점 자율화되는 시스템 사이의 **의도·제약·책임 층**.
사람은 차터(Charter, 내부 명칭 하네스)를 소유하고, 그 안에서 AI가 자율적으로 개발해도 이해와 책임이 유지되게 한다.

- 스펙: `docs/spec/team-workbench-spec-v1.md`
- 구현·배포 계획: `docs/spec/implementation-plan-v0.md`
- 아키텍처·구현 패턴: `docs/spec/architecture-patterns-v0.md`
- 배치 다이어그램: `docs/spec/architecture-aws-style.svg`
- 결정 기록: `docs/adr/`
- 이전 방향(참고용): `docs/archive/`

12주 목표(한 문장): Claude Code를 쓰는 개인이 설치하면 편집 전에 자기 제약이 주입되고, 하루 작업이 Change·판단으로 남으며, 팀이 생기면 서버로 제약을 공유한다.

## 써 보기 (6–7주차 기준, Claude Code)

```sh
make build && export PATH=$PWD/bin:$PATH
keelage setup                                      # 전역 1회: 훅 등록(백업)·MCP 등록·~/.claude/CLAUDE.md 흡수. 항목별 승인
keelage daemon &                                   # ~/.keelage/keelage.sock, ledger.db
keelage constraint add --scope path='src/**' --anchor 'code://src/calc.ts#fee' --verify "no retries in billing"
keelage constraint add --kind autonomy --level L0 --deny-edit --scope path='src/vault/*' --verify "vault code is edited by humans"
keelage init --verify                              # 이 리포 활성화: CLAUDE.md·AGENTS.md·docs/adr 흡수, 컨텍스트 렌더(포인터 한 줄), .keelage/ 사이드카
keelage status                                     # 데몬·훅·MCP·사이드카·diverged
keelage uninstall                                  # 전부 되돌림 — 사용자 파일은 바이트 동일, 원장은 남김(--purge로 삭제)
```

이후 Claude Code가 `src/calc.ts`를 편집하기 직전에 제약·닻 상태가 컨텍스트로 주입되고, `src/vault/*` 편집은 거부된다.
데몬이 없거나 50ms를 넘기면 훅은 아무것도 하지 않는다(fail-open).

하루 작업이 남는 곳 (8주차):

```sh
keelage adapter git print post-commit > .git/hooks/post-commit && chmod +x .git/hooks/post-commit
git commit …                 # 커밋마다 Change가 도출된다 (제약에 닿으면 판단 대기, 아니면 즉시 settle)
keelage inbox                # 판단할 Change · stale 닻 · 미검증 제약 · 세션 판단 후보
keelage history code://src/calc.ts#fee
keelage sessions             # 턴 수·만진 파일·판단 후보 — 대화 원문은 어디에도 없다
```

팀이 생기면 (10–11주차): 서버 하나, 데몬은 커밋·공유 시점에 push, 팀 층은 pull.

```sh
keelage-server org create acme --db postgres://…            # 셀프호스트: org 1개
keelage-server member add acme alice --db …                  # GitHub login
keelage-server serve --db … --key server.ed25519 --github-client-id …   # device 로그인; 없으면 --token만
keelage-server token create acme cc-1 --kind agent --owner alice --db …  # 에이전트 토큰(책임자 = alice)

keelage server login --url https://keelage.example.com --org acme       # 브라우저에서 코드 입력 → 토큰 저장, 데몬 키 등록
keelage sync push                 # 제약·결정·scope·닻·Change·gate (개인 범위·미공유 세션 제외)
keelage sync pull                 # 팀 층 사본 → ~/.keelage/cache/team.db, 투영에 반영
keelage gate ask --scope 'org=|product=|team=core|repo=|path=|person=' --proposal refactor-billing --level L1   # 에이전트 토큰
keelage gate resolve <id> --allow --reason "reviewed"                    # 사람 토큰
keelage server proof --seq 1 | keelage verify-proof --server-key "$(keelage server key)"   # 오프라인 포함 증명
```
