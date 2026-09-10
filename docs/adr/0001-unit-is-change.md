# 0001. 제품의 단위는 코드가 아니라 변경(Change)이다
Status: accepted · Date: 2026-09-10
## Context
런타임 우선, 문서 우선을 차례로 검토했으나 둘 다 표면이었다. AI가 코드를 쓰는 팀에서 지속되는 것은 의도·제약·책임뿐이다.
## Decision
지속 객체 = Intent · Constraint · Accountability(Judgment/Decision/Outcome). 코드·문서·런타임은 실현체이며 닻으로만 연결된다. Change = 하네스 또는 실현체의 변화.
## Consequences
코드가 재생성돼도 제약과 책임 기록은 같은 ID를 유지한다. 코드 닻은 실현체 링크로 격하된다.
