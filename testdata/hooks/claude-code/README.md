# Claude Code 훅 시퀀스 (시뮬레이터 입력)

`*.jsonl`: 한 줄에 훅 호출 하나 — `{"event": "<HookEventName>", "input": {...stdin JSON...}}`.
`$ROOT`는 재생 시 픽스처 리포의 절대 경로로 치환된다(Claude Code는 file_path를 항상 절대 경로로 준다).
`*.expected.jsonl`: 같은 줄 번호의 기대 stdout (빈 줄 = 출력 없음). `go test ./cmd/keelage -run Hook -update`로 재생성.

출처: code.claude.com/docs/en/hooks 의 입력 스키마(확인 2026-09-10)로 합성. 실제 세션 녹화본으로 교체하면
`internal/adapter/claudecode/VERSION`의 확인 날짜를 함께 올린다.
