# 0014. init은 파일을 원천으로 흡수하고 포인터 한 줄만 쓰며, setup/uninstall은 우리 항목만 더하고 빼서 원상태를 보장한다
Status: accepted · Date: 2026-09-10
## Context
9주차(계획서 §3.6): `setup`(전역 1회), 관찰/활성 모드, `init` 흡수·렌더·사이드카, `uninstall` 원상 복원. 완료 기준 "설치→제거 왕복이 원상태". 구체화가 필요했던 것: 무엇을 어떤 객체로 흡수하는가, 렌더 결과를 어디에 두는가, 사용자 파일을 어떻게 건드리고 되돌리는가, ADR/규칙 파일이 바뀌면 어떻게 하는가.
## Decision
- **흡수**: 루트·중첩 `CLAUDE.md`·`AGENTS.md` → `rule` 제약(루트=리포 범위, 중첩=`<dir>/**` 범위, 본문=파일 텍스트에서 우리 managed 블록을 뺀 것, authored, explanation="imported from <path>"). `docs/adr|doc/adr|adr/*.md`(README·template 제외) → `Decision`(제목=첫 `#` 줄). `--verify`면 즉시 verified("imported-verified"), 아니면 generated로 inbox 대기.
- **파일이 원천**: 다시 `init`할 때 해시가 같으면 unchanged; 규칙 파일이 바뀌면 새 제약을 draft하고 옛 것을 supersede; ADR이 바뀌면 `ReviseDecision`(재검증 필요). 사이드카 `.keelage/manifest.json`이 {path, object_id, hash}를 기억한다.
- **렌더(포인터 분리, 패턴 §5.2)**: 생성 본문은 `.keelage/context/claude-code/context.md`(gitignore), `CLAUDE.md`에는 managed 블록 하나(`@.keelage/context/claude-code/context.md`). 이 리포 파일에서 흡수한 객체는 **다시 렌더하지 않는다**(중복 방지). 렌더 대상 = 리포 범위에 유효한 외부 제약(개인·팀·제품·글로벌) + 이 리포의 경로 범위 제약 + Decision 제목(출처 경로) + stale/review 닻.
- **managed 블록**: `<!-- keelage:begin <id> <hash> -->…<!-- keelage:end -->`. 바깥은 바이트 단위로 보존. 블록 안이 편집되면(해시 불일치) `diverged`로 보고하고 `--force` 없이는 덮어쓰지 않는다.
- **`.gitignore`**: `.keelage/context/`가 없으면 한 줄 추가하고 manifest에 기록; uninit 때 그 줄만 제거.
- **setup**: `~/.claude/settings.json`에 우리 핸들러(명령이 `keelage hook claude-code`로 시작)만 추가, 기존 항목 유지, 원본은 `~/.keelage/backup/<ts>/`에 복사. MCP는 `claude` CLI가 있으면 `claude mcp add`, 없으면 명령을 안내. `~/.claude/CLAUDE.md`는 개인 범위 제약으로 흡수하되 되쓰기는 하지 않는다. 데몬 autostart는 v0에서 안내만. 항목별 y/N(`--yes`로 생략), `--dry-run`.
- **uninstall**: 등록된 리포(`~/.keelage/repos.json`)마다 uninit(블록 제거·생성 파일 삭제·gitignore 줄 제거·사이드카 삭제) → settings.json에서 우리 핸들러만 제거(백업 후) → MCP 해제(CLI로 등록한 경우만) → `setup.json` 삭제. 원장·백업은 남긴다(`--purge`만 삭제).
- **리포 id**는 여전히 디렉터리 이름(ADR 0012). 개인 규칙의 manifest는 `~/.keelage/manifest.json`.
## Consequences
e2e가 setup→init→uninstall 뒤 사용자 파일 전부가 바이트 동일함을 검증한다. 흡수된 규칙은 스킬·MCP·훅 주입에 곧바로 쓰인다(개인 규칙은 모든 리포의 pre_edit에 뜬다). AGENTS.md 등 다른 도구 규칙도 같은 경로로 흡수되지만 렌더 대상은 Claude Code뿐(Cursor 어댑터는 12주 밖). ADR의 `supersedes` 링크 파싱은 후속.
