package semverutil

import "testing"

func TestCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want ChangeLevel
	}{
		{"1.2.3", "1.2.3", NoChange},
		{"1.2.3", "1.2.4", PatchChange},
		{"1.2.3", "1.3.0", MinorChange},
		{"1.2.3", "2.0.0", MajorChange},
		{"v1.2.3", "1.2.4", PatchChange},
		{"not-semver", "1.2.3", NoChange},
	}
	for _, c := range cases {
		if got := Compare(c.a, c.b); got != c.want {
			t.Fatalf("Compare(%q,%q)=%v want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestBumpChartVersion(t *testing.T) {
	if got, _ := BumpChartVersion("1.2.3", PatchChange); got != "1.2.4" {
		t.Fatalf("patch bump got %s", got)
	}
	if got, _ := BumpChartVersion("1.2.3", MinorChange); got != "1.3.0" {
		t.Fatalf("minor bump got %s", got)
	}
	if got, _ := BumpChartVersion("1.2.3", MajorChange); got != "2.0.0" {
		t.Fatalf("major bump got %s", got)
	}
}
