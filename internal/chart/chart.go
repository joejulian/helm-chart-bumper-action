package chart

import (
	"fmt"
	"os"
	"path/filepath"

	yaml "github.com/goccy/go-yaml"
	"github.com/joejulian/helm-chart-bumper-action/internal/semverutil"
	"github.com/joejulian/helm-chart-bumper-action/internal/yamlutil"
)

type Dependency struct {
	Name       string `yaml:"name"`
	Version    string `yaml:"version"`
	Repository string `yaml:"repository"`
}

type Meta struct {
	Name         string       `yaml:"name"`
	Version      string       `yaml:"version"`
	AppVersion   string       `yaml:"appVersion"`
	Dependencies []Dependency `yaml:"dependencies"`
}

func LoadMeta(chartYAML []byte) (Meta, error) {
	var m Meta
	if err := yaml.Unmarshal(chartYAML, &m); err != nil {
		return Meta{}, err
	}
	return m, nil
}

func ReadChartYAML(chartDir string) ([]byte, error) {
	p := filepath.Join(chartDir, "Chart.yaml")
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// ComputeChangeLevel determines the bump level using your rules based on changes in:
// - appVersion
// - dependency versions (by name)
func ComputeChangeLevel(base, cur Meta) semverutil.ChangeLevel {
	lvl := semverutil.Compare(base.AppVersion, cur.AppVersion)

	baseDeps := map[string]string{}
	for _, d := range base.Dependencies {
		baseDeps[d.Name] = d.Version
	}
	for _, d := range cur.Dependencies {
		if old, ok := baseDeps[d.Name]; ok {
			lvl = semverutil.Max(lvl, semverutil.Compare(old, d.Version))
		}
	}
	return lvl
}

// ApplyChartVersionBump sets $.version in Chart.yaml AST.
func ApplyChartVersionBump(ast *yamlutil.File, lvl semverutil.ChangeLevel) (bool, error) {
	curVer, ok, err := yamlutil.GetString(ast, "$.version")
	if err != nil {
		return false, err
	}
	if !ok {
		return false, fmt.Errorf("Chart.yaml missing version")
	}
	newVer, err := semverutil.BumpChartVersion(curVer, lvl)
	if err != nil {
		return false, err
	}
	if newVer == curVer {
		return false, nil
	}
	return yamlutil.SetString(ast, "$.version", newVer)
}
