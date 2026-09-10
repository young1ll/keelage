# 0006. 범위는 트리가 아니라 제품×팀 격자 + 소유 지도다
Status: accepted · Date: 2026-09-10
## Context
한 제품을 여러 팀이 분담하는 조직에서 "조직→팀→제품" 트리는 틀리다.
## Decision
Scope = org × product × team × (repo, path) × person. Ownership은 CODEOWNERS 가져오기로 초기화. 제약은 넓은 범위 우선(상위가 구속), 컨텍스트는 좁은 범위 우선(하위가 구체화). 격자는 엔진 안에, UI는 "제품 헌법/우리 팀/내 것" 세 층만.
## Consequences
타 팀 소유 닻을 건드리면 그 팀 판단자 없이 settle 불가. 팀 간 충돌은 제품 범위 인박스로.
