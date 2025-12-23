package yamlutil

import "testing"

func TestSetStringPreservesComment(t *testing.T) {
	in := []byte(`# chart comment
name: test
version: 1.2.3 # inline
appVersion: 2.0.0
`)
	f, err := ParseBytes(in)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := SetString(f, "$.version", "1.2.4")
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatalf("expected change")
	}
	out, err := Render(f)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(out, "# chart comment") {
		t.Fatalf("expected header comment preserved, got:\n%s", out)
	}
	if !contains(out, "# inline") {
		t.Fatalf("expected inline comment preserved, got:\n%s", out)
	}
	if !contains(out, "version: 1.2.4") {
		t.Fatalf("expected new version, got:\n%s", out)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && (func() bool { return stringsContains(s, sub) })())
}

// small wrapper so we don't import strings in every test file via gofmt moving it.
func stringsContains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
