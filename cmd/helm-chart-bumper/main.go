package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joejulian/helm-chart-bumper-action/internal/chart"
	"github.com/joejulian/helm-chart-bumper-action/internal/directives"
	"github.com/joejulian/helm-chart-bumper-action/internal/helmdeps"
	"github.com/joejulian/helm-chart-bumper-action/internal/imageresolver"
	"github.com/joejulian/helm-chart-bumper-action/internal/gitutil"
	"github.com/joejulian/helm-chart-bumper-action/internal/yamlutil"
)

func main() {
	var (
		basePath    = flag.String("base", "", "Path to base Chart.yaml")
		baseRef     = flag.String("base-ref", "", "Git ref to read the base Chart.yaml from (e.g. 'refs/remotes/origin/main' or 'HEAD~1')")
		baseRefPath = flag.String("base-ref-path", "", "Repository-relative path to base Chart.yaml when using --base-ref (defaults to --cur)")
		repoRoot    = flag.String("repo", ".", "Path to the git working tree (used with --base-ref)")
		curPath     = flag.String("cur", "", "Path to current Chart.yaml")
		write       = flag.Bool("write", false, "Write updated files back to disk")

		updateImages = flag.Bool("update-images", false, "Update image versions based on '# bump:' directives in Chart.yaml and values*.yaml")
		updateDeps   = flag.Bool("update-deps", false, "Update Chart.yaml dependencies to latest versions from their Helm repositories")
		scanGlob     = flag.String("scan-glob", "Chart.yaml,values*.yaml", "Comma-separated glob(s) relative to the chart directory to scan for '# bump:' directives")
	)
	flag.Parse()

	if *curPath == "" || (*basePath == "" && *baseRef == "") || (*basePath != "" && *baseRef != "") {
		fmt.Fprintln(os.Stderr, "usage: helm-chart-bumper (--base path/to/base/Chart.yaml | --base-ref <git-ref> [--base-ref-path path/in/repo/Chart.yaml]) --cur path/to/cur/Chart.yaml [--repo path/to/repo] [--write] [--update-images] [--update-deps]")
		os.Exit(2)
	}

	var baseBytes []byte
	var err error
	if *baseRef != "" {
		p := *baseRefPath
		if p == "" {
			p = *curPath
		}
		baseBytes, err = gitutil.ReadFileAtRef(*repoRoot, *baseRef, p)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	} else {
		baseBytes, err = os.ReadFile(*basePath)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
	}

	chartDir := filepath.Dir(*curPath)

	// Optional: update images and/or deps on disk first (so subsequent reads are consistent).
	anyFileWritten := false
	if *write {
		if *updateImages {
			written, err := updateImagesInChartDir(chartDir, *scanGlob)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
			anyFileWritten = anyFileWritten || written
		}
		if *updateDeps {
			written, err := updateDepsInChartYAML(chartDir)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
			anyFileWritten = anyFileWritten || written
		}
	}

	curBytes, err := os.ReadFile(*curPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	baseMeta, err := chart.LoadMeta(baseBytes)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	curMeta, err := chart.LoadMeta(curBytes)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	lvl := chart.ComputeChangeLevel(baseMeta, curMeta)
	ast, err := yamlutil.ParseBytes(curBytes)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	changed, err := chart.ApplyChartVersionBump(ast, lvl)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	out, err := yamlutil.Render(ast)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	didWriteChart := false
	if *write && changed {
		outBytes := []byte(out)
		// Don’t touch the file if the rendered bytes are identical.
		if !bytes.Equal(curBytes, outBytes) {
			if err := os.WriteFile(*curPath, outBytes, 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
			didWriteChart = true
		}
	}

	if !*write {
		fmt.Print(out)
	}

	writeGithubOutputChanged(anyFileWritten || didWriteChart)
}

func updateDepsInChartYAML(chartDir string) (bool, error) {
	chartPath := filepath.Join(chartDir, "Chart.yaml")
	resolved, err := helmdeps.ResolveLatestDependencies(chartPath)
	if err != nil {
		return false, err
	}
	if len(resolved) == 0 {
		return false, nil
	}
	b, err := os.ReadFile(chartPath)
	if err != nil {
		return false, err
	}
	ast, err := yamlutil.ParseBytes(b)
	if err != nil {
		return false, err
	}
	changed := false
	for _, r := range resolved {
		if r.NewVersion == "" || r.NewVersion == r.OldVersion {
			continue
		}
		p := fmt.Sprintf("$.dependencies[%d].version", r.Index)
		c, err := yamlutil.SetString(ast, p, r.NewVersion)
		if err != nil {
			return false, fmt.Errorf("Chart.yaml dependency %q: %w", r.Name, err)
		}
		changed = changed || c
	}
	if !changed {
		return false, nil
	}
	out, err := yamlutil.Render(ast)
	if err != nil {
		return false, err
	}
	outBytes := []byte(out)
	if !bytes.Equal(b, outBytes) {
		if err := os.WriteFile(chartPath, outBytes, 0o644); err != nil {
			return false, err
		}
		return true, nil
	}
	return false, nil
}

func updateImagesInChartDir(chartDir, globCSV string) (bool, error) {
	globs := splitCSV(globCSV)
	files := map[string]struct{}{}
	for _, g := range globs {
		matches, err := filepath.Glob(filepath.Join(chartDir, g))
		if err != nil {
			return false, err
		}
		for _, m := range matches {
			// Only regular files.
			st, err := os.Stat(m)
			if err != nil {
				return false, err
			}
			if st.Mode().IsRegular() {
				files[m] = struct{}{}
			}
		}
	}

	anyWritten := false
	for p := range files {
		dirs, err := directives.ScanFileForImageDirectives(p)
		if err != nil {
			return false, err
		}
		if len(dirs) == 0 {
			continue
		}

		b, err := os.ReadFile(p)
		if err != nil {
			return false, err
		}
		ast, err := yamlutil.ParseBytes(b)
		if err != nil {
			return false, err
		}

		fileChanged := false
		for _, d := range dirs {
			// Full image path is required.
			if d.Image == "" {
				return false, fmt.Errorf("%s:%d: bump directive missing required image=<full repo path>", p, d.Line)
			}
			strategy := d.Strategy
			if strategy == "" {
				strategy = "semver"
			}

			var newValue string
			switch strings.ToLower(strategy) {
			case "digest":
				// Resolve digest from sibling tag.
				parentPath := parentYAMLPath(d.YAMLPath)
				tagPath := parentPath + ".tag"
				tag, ok, _ := yamlutil.GetString(ast, tagPath)
				if !ok || strings.TrimSpace(tag) == "" {
					return false, fmt.Errorf("%s:%d: strategy=digest requires a sibling 'tag' key (looked for %s)", p, d.Line, tagPath)
				}
				digest, err := imageresolver.ResolveDigest(d.Image, tag, d.Platform, nil)
				if err != nil {
					return false, fmt.Errorf("%s:%d: %w", p, d.Line, err)
				}
				newValue = digest
			case "literal":
				tag, err := imageresolver.ResolveTag(d.Image, "literal", d.Constraint, d.TagRegex, d.AllowPrerelease, nil)
				if err != nil {
					return false, fmt.Errorf("%s:%d: %w", p, d.Line, err)
				}
				newValue = tag
			case "regex":
				tag, err := imageresolver.ResolveTag(d.Image, "regex", d.Constraint, d.TagRegex, d.AllowPrerelease, nil)
				if err != nil {
					return false, fmt.Errorf("%s:%d: %w", p, d.Line, err)
				}
				newValue = tag
			case "semver":
				tag, err := imageresolver.ResolveTag(d.Image, "semver", d.Constraint, d.TagRegex, d.AllowPrerelease, nil)
				if err != nil {
					return false, fmt.Errorf("%s:%d: %w", p, d.Line, err)
				}
				newValue = tag
			default:
				return false, fmt.Errorf("%s:%d: unknown strategy %q", p, d.Line, d.Strategy)
			}

			c, err := yamlutil.SetString(ast, d.YAMLPath, newValue)
			if err != nil {
				return false, fmt.Errorf("%s:%d: failed to set %s: %w", p, d.Line, d.YAMLPath, err)
			}
			fileChanged = fileChanged || c
		}

		if !fileChanged {
			continue
		}

		out, err := yamlutil.Render(ast)
		if err != nil {
			return false, err
		}
		outBytes := []byte(out)
		if !bytes.Equal(b, outBytes) {
			if err := os.WriteFile(p, outBytes, 0o644); err != nil {
				return false, err
			}
			anyWritten = true
		}
	}
	return anyWritten, nil
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out = append(out, p)
	}
	return out
}

func parentYAMLPath(p string) string {
	// Expect $.a.b.c
	if !strings.HasPrefix(p, "$." ) {
		return p
	}
	idx := strings.LastIndex(p, ".")
	if idx <= 1 {
		return "$"
	}
	return p[:idx]
}

func writeGithubOutputChanged(changed bool) {
	outPath := os.Getenv("GITHUB_OUTPUT")
	if outPath == "" {
		return
	}
	f, err := os.OpenFile(outPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		return
	}
	defer f.Close()

	if changed {
		fmt.Fprintln(f, "changed=true")
		return
	}
	fmt.Fprintln(f, "changed=false")
}
