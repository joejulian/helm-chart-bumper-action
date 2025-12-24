package chart

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/joejulian/helm-chart-bumper-action/internal/semverutil"
	"github.com/joejulian/helm-chart-bumper-action/internal/yamlutil"
)

func TestLoadMeta(t *testing.T) {
	in := []byte("name: x\nversion: 1.2.3\nappVersion: 1.2.4\ndependencies:\n  - name: redis\n    version: 19.0.0\n")
	m, err := LoadMeta(in)
	if err != nil {
		t.Fatalf("LoadMeta: %v", err)
	}
	if m.Name != "x" || m.Version != "1.2.3" || m.AppVersion != "1.2.4" {
		t.Fatalf("unexpected meta: %#v", m)
	}
	if len(m.Dependencies) != 1 || m.Dependencies[0].Name != "redis" {
		t.Fatalf("unexpected deps: %#v", m.Dependencies)
	}
}

func TestReadChartYAML(t *testing.T) {
	dir := t.TempDir()
	want := "name: x\nversion: 0.1.0\n"
	if err := os.WriteFile(filepath.Join(dir, "Chart.yaml"), []byte(want), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	got, err := ReadChartYAML(dir)
	if err != nil {
		t.Fatalf("ReadChartYAML: %v", err)
	}
	if string(got) != want {
		t.Fatalf("got %q want %q", string(got), want)
	}
}

func TestComputeChangeLevel_UsesMaxOfAppVersionAndDeps(t *testing.T) {
	base := Meta{AppVersion: "1.2.3", Dependencies: []Dependency{{Name: "redis", Version: "19.0.0"}}}
	cur := Meta{AppVersion: "1.3.0", Dependencies: []Dependency{{Name: "redis", Version: "20.0.0"}}}
	if got := ComputeChangeLevel(base, cur); got != semverutil.MajorChange {
		t.Fatalf("got %v want %v", got, semverutil.MajorChange)
	}
}

func TestApplyChartVersionBump(t *testing.T) {
	ast, err := yamlutil.ParseBytes([]byte("name: x\nversion: 1.2.3\nappVersion: 1.2.3\n"))
	if err != nil {
		t.Fatalf("ParseBytes: %v", err)
	}
	changed, err := ApplyChartVersionBump(ast, semverutil.PatchChange)
	if err != nil {
		t.Fatalf("ApplyChartVersionBump: %v", err)
	}
	if !changed {
		t.Fatalf("expected changed=true")
	}
	ver, ok, err := yamlutil.GetString(ast, "$.version")
	if err != nil || !ok {
		t.Fatalf("GetString: ok=%v err=%v", ok, err)
	}
	if ver != "1.2.4" {
		t.Fatalf("version got %q want %q", ver, "1.2.4")
	}
}
