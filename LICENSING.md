# Licensing

keelage는 두 가지 라이선스로 배포된다 (계획서 §1·ADR 0016).

| 대상 | 라이선스 | 파일 |
|------|----------|------|
| `keelage` 데몬·CLI, `internal/core`·`port`·`app`·`merkle`, 어댑터, 스펙·스키마·도구 — 아래 표에 없는 모든 것 | **Apache License 2.0** | [`LICENSE`](LICENSE) |
| `keelage-server`: `cmd/keelage-server/**`, `deploy/**`, 서버 전용 어댑터 `internal/adapter/postgres/**`·`internal/adapter/githubapp/**`·`internal/adapter/github/**` | **Functional Source License 1.1, Apache 2.0 Future License (FSL-1.1-Apache-2.0)** — 각 버전은 공개 2년 뒤 Apache 2.0이 된다 | [`cmd/keelage-server/LICENSE.md`](cmd/keelage-server/LICENSE.md) |

FSL은 서버를 경쟁 서비스로 제공하는 것만 막고, 내부 사용·셀프호스트·수정·연구는 허용한다. 서버가 쓰는 공용 패키지(`internal/app` 등)는 Apache 2.0이다.
