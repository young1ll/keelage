package supply

import "testing"

func TestClassifyPrompt(t *testing.T) {
	yes := []string{
		"아니, 캐시는 빼고 다시 해줘", "아니야 그게 아니라 재시도를 없애", "그거 말고 다른 방식으로", "retry 대신 큐를 써",
		"No, keep the old signature", "not that file", "Don't touch the vault", "do it with a queue instead", "Actually, no — revert that",
		"undo that change", "하지 마 그건", "원래대로 돌려놔",
	}
	for _, p := range yes {
		if ok, sig := ClassifyPrompt(p); !ok || sig == "" {
			t.Errorf("%q must read as redirect", p)
		}
	}
	no := []string{
		"", "add a cap parameter to fee", "이제 테스트도 추가해줘", "Nothing else, thanks", "notify the user on failure",
		"the annotation is fine", "rather, let's also add logging", // 'rather' alone is not 'rather than'
	}
	for _, p := range no {
		if ok, sig := ClassifyPrompt(p); ok {
			t.Errorf("%q must not read as redirect (%s)", p, sig)
		}
	}
}

func TestIsRevertCommand(t *testing.T) {
	yes := []string{"git checkout -- src/a.ts", "git checkout HEAD -- a.ts", "git restore src/a.ts", "git reset --hard", "git revert HEAD", "git stash", "cd repo && git checkout -- .", "git checkout main"}
	for _, c := range yes {
		if !IsRevertCommand(c) {
			t.Errorf("%q must be a revert", c)
		}
	}
	no := []string{"git status", "git commit -m x", "git stash list", "git stash pop", "git diff", "npm test", "git checkout -b feature", "git log"}
	for _, c := range no {
		if IsRevertCommand(c) {
			t.Errorf("%q must not be a revert", c)
		}
	}
}
