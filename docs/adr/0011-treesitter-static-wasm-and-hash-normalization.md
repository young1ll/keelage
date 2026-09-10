# 0011. tree-sitter는 런타임+문법을 정적 wasm 하나로 묶어 wazero로 돌리고, 해시 정규화 규칙을 고정한다
Status: accepted · Date: 2026-09-10
## Context
계획서 §2는 "tree-sitter wasm 문법 + wazero(CGO 없음)"라고만 했다. 공식 배포 wasm(web-tree-sitter의 `tree-sitter.wasm` + 문법 side module)은 Emscripten 동적 링킹을 전제해 wazero에서 로더를 재구현해야 한다. 패턴 §7의 해시 규칙("시그니처 정규화 문자열", "S-표현에서 주석·공백 제거")도 구체 규칙이 필요했다.
## Decision
- **빌드**: tree-sitter 런타임 C 소스(npm `tree-sitter` vendor) + `tree-sitter-typescript`의 typescript/tsx parser.c·scanner.c + 우리 C 심(`csrc/keelage_ts.c`)을 clang `--target=wasm32-wasi` + wasi-libc로 **reactor 모듈 하나**로 링크한다(`scripts/build-treesitter-wasm.sh`, 3MB, 리포에 커밋, 버전은 `VERSION`). 심은 데이터만 넘긴다(트리·매치의 평면 덤프); 콜백 없음. 언어 추가 = 문법 소스 추가 + 재빌드.
- **시그니처** = 선언 시작부터 본문(`@body`) 직전까지의 텍스트를 정규화한 것 + export 여부. 정규화: 공백 연속을 하나로, **구두점에 인접한 공백은 제거**(`add( a : number )` ≡ `add(a:number)`), 단어 사이 공백은 유지.
- **본문** = `@body` 서브트리의 S-표현. 주석 노드 제외, 공백 없음, **잎 노드는 텍스트를 포함**(리터럴·식별자 변경이 본문 변경으로 잡히도록).
- **파일 닻**(`code://path`, 파싱 언어) = 심볼 표면 해시: 시그니처 목록 / 본문 목록 / 파일 바이트. generic 언어는 세 층 모두 파일 해시(보수적).
- 심볼 선택은 언어별 `queries/*.scm`(`@symbol @name @body`)로, 이름 한정은 바깥 심볼로(`Class.method`).
- **이동**은 같은 시그니처 해시가 한 닻에서 사라지고 정확히 한 닻에서 나타날 때만 자동 기록. 모호하면 기록하지 않는다.
- verify는 `(ref, 닻)` 멱등 키를 써서 같은 커밋에서 재실행해도 이벤트가 늘지 않는다(커밋 묶음).
## Consequences
CGO·emscripten·node 없이 `go build`만으로 데몬이 만들어진다. wasm 재빌드에는 clang·wasi-libc·npm이 필요하며 CI는 빌드하지 않고 커밋본을 쓴다. 파싱 지연은 wazero 컴파일 캐시(`~/.keelage/cache/wazero`)로 흡수한다. 이름 변경은 이동이 아니라 소실+신규로 보인다(의도).
