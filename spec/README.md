# spec/ — 공개 계약

| 경로 | 내용 | 원천 |
|---|---|---|
| `schema/*.json` | 지속 객체·Change·이벤트 JSON Schema | **생성물** — `internal/core`의 Go 구조체에서 `make schema`로 생성. 손으로 고치지 않는다 |
| `openapi.yaml` | `keelage-server` API | 손 작성(design-first) |
| `hooks.md` | 훅 계약(도구별 매핑은 `adapters/`) | 손 작성 (6–7주차) |
| `mcp.md` | MCP 리소스·도구 계약 | 손 작성 (6–7주차) |

CI는 `make schema` 결과가 커밋본과 다르면 실패한다.
