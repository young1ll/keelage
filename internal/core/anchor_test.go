package core

import "testing"

func TestParseAnchor(t *testing.T) {
	ok := []struct {
		in   string
		want Anchor
	}{
		{"code://src/a.ts#foo", Anchor{SchemeCode, "src/a.ts", "foo"}},
		{"code://src/a.ts", Anchor{SchemeCode, "src/a.ts", ""}},
		{"route://GET /users/{id}", Anchor{SchemeRoute, "GET /users/{id}", ""}},
		{"ui://Button", Anchor{SchemeUI, "Button", ""}},
		{"schema://User", Anchor{SchemeSchema, "User", ""}},
		{"infra://terraform:aws_s3_bucket.logs", Anchor{SchemeInfra, "terraform:aws_s3_bucket.logs", ""}},
		{"doc://01J", Anchor{SchemeDoc, "01J", ""}},
		{"contract://billing/fee", Anchor{SchemeContract, "billing/fee", ""}},
	}
	for _, c := range ok {
		got, err := ParseAnchor(c.in)
		if err != nil {
			t.Errorf("%s: %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%s: got %+v want %+v", c.in, got, c.want)
		}
		if got.String() != c.in {
			t.Errorf("%s: canonical %s", c.in, got.String())
		}
		if !got.Equal(c.want) {
			t.Errorf("%s: Equal false", c.in)
		}
	}
	bad := []string{
		"", "src/a.ts", "code://", "code:///abs/path", "code://../x", "code://a/./b", "code://a.ts#x#y",
		"route://get /x", "route://GET x", "route://GET", "ui://A B", "doc://a#b", "zzz://x", "code://a\nb",
	}
	for _, in := range bad {
		if _, err := ParseAnchor(in); err == nil {
			t.Errorf("%q must fail", in)
		}
	}
	if !(Anchor{SchemeCode, "a.ts", ""}).IsFile() || (Anchor{SchemeCode, "a.ts", "f"}).IsFile() {
		t.Error("IsFile")
	}
}
