# 0005. 에이전트를 만들지 않는다
Status: accepted · Date: 2026-09-10
## Context
Work 면이 에이전트 실행까지 맡으면 제품 규모가 두 배가 되고 OpenClaw·Grok Bot·Claude Code와 경쟁하게 된다.
## Decision
Work는 작업과 컨텍스트 묶음을 기존 에이전트에 넘기고 결과를 Change로 회수한다. 승인은 승인 브로커(`/gates`)로 기록만 맡는다.
## Consequences
첫 가치는 에이전트 루프 안의 훅 개입이며, 훅 없는 에이전트는 MCP 도구·git 신호·`/gates`로 붙는다.
