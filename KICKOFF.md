# Claude Code 시작 프롬프트 (1~3주차)

## 0. 리포 준비
```
git init && git add . && git commit -m "docs: spec, plan, patterns, ADR 0001-0008"
gh repo create young1ll/<이름> --private --source=. --push
```
이름은 4주차 전에 확정(계획서 §9). 임시로 `kb-workbench` 가능.

## 1. 골격 (1주차)
> CLAUDE.md와 docs/spec/architecture-patterns-v0.md §1을 읽고, Go 모듈 골격을 만들어줘.
> cmd/kb, cmd/kb-server, internal/{core,app,port,adapter}, spec/ 디렉터리.
> golangci-lint + depguard로 import 규칙(§1)을 강제하는 설정을 넣고, 빈 데몬/서버가 기동되게 해줘.
> 아직 기능은 만들지 마. 뺀 것 목록을 지켜.

## 2. 코어 (2주차)
> architecture-patterns §2·§3을 구현해줘: Change·Constraint·Scope·Gate 애그리게이트의 Decide/Apply,
> 이벤트 타입과 버전, 커맨드 파이프라인(메모리 원장). 자율 전이표와 범위 해석(§2.2 ScopeKey)은 테이블 테스트로.
> Go 구조체에서 JSON Schema를 생성해 spec/schema에 쓰는 make 타깃을 추가해줘.

## 3. 원장 (3주차)
> port.Ledger를 정의하고 SQLite(modernc) 구현을 §4.2 스키마로 만들어줘. 해시 체인·멱등 키·낙관적 동시성.
> SQLite와 메모리 구현이 같은 계약 테스트를 통과하게 해줘.

## 진행 규칙
- 매 주 끝에 `docs/adr/`에 바뀐 결정을 추가.
- 스펙과 다르게 구현해야 하면 먼저 ADR 초안을 쓰고 멈춰서 물어봐.
