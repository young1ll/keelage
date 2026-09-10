# 0008. 코어는 Go, 12주는 데몬 완주·서버 최소
Status: accepted · Date: 2026-09-10
## Context
1인 + AI. 개발자 대상 운영 도구의 배포 특성(단일 바이너리·작은 이미지·동시 연결)이 v0부터 보인다.
## Decision
Go 단일 바이너리(데몬·서버). 12주: 데몬 전체 + 서버 최소(원장·sync·/gates·GitHub App 코멘트·OIDC). 뺀 것: 데몬 머클·TUI·파일 감시·AI 프로바이더 구현·Cursor 어댑터·Python/Go tree-sitter·웹 UI·Rejected 투영 정정·서버 클론 계산.
## Consequences
첫 가치는 Claude Code 훅에 종속됨을 명시하고 훅 없는 경로를 같은 주에 만든다.
