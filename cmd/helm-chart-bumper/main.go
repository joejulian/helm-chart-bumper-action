package main

import (
	"bytes"
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/joejulian/helm-chart-bumper-action/internal/chart"
	"github.com/joejulian/helm-chart-bumper-action/internal/directives"
	"github.com/joejulian/helm-chart-bumper-action/internal/gitutil"
	"github.com/joejulian/helm-chart-bumper-action/internal/helmdeps"
	"github.com/joejulian/helm-chart-bumper-action/internal/imageresolver"
	"github.com/joejulian/helm-chart-bumper-action/internal/logutil"
	"github.com/joejulian/helm-chart-bumper-action/internal/yamlutil"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
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

		verbosity = flag.Int("v", 0, "Verbosity level. Set -v 6 for debug logs.")
	)
	flag.Parse()

	log := newLogger(*verbosity)
	defer func() { _ = log.Sync() }()

	ctx := logutil.WithLogger(context.Background(), log)
	log = logutil.FromContext(ctx).With(zap.String("func", "main"))

	log.Debug("parsed flags",
		zap.String("base", *basePath),
		zap.String("baseRef", *baseRef),
		zap.String("baseRefPath", *baseRefPath),
		zap.String("repo", *repoRoot),
		zap.String("cur", *curPath),
		zap.Bool("write", *write),
		zap.Bool("updateImages", *updateImages),
		zap.Bool("updateDeps", *updateDeps),
		zap.String("scanGlob", *scanGlob),
		zap.Int("v", *verbosity),
	)

	if *curPath == "" || (*basePath == "" && *baseRef == "") || (*basePath != "" && *baseRef != "") {
		log.Error("invalid arguments",
			zap.String("usage", "helm-chart-bumper (--base path/to/base/Chart.yaml | --base-ref <git-ref> [--base-ref-path path/in/repo/Chart.yaml]) --cur path/to/cur/Chart.yaml [--repo path/to/repo] [--write] [--update-images] [--update-deps]"),
		)
		os.Exit(2)
	}

	var baseBytes []byte
	var err error
	if *baseRef != "" {
		p := *baseRefPath
		if p == "" {
			p = *curPath
		}
		log.Debug("reading base chart from git ref",
			zap.String("repo", *repoRoot),
			zap.String("ref", *baseRef),
			zap.String("path", p),
		)
		baseBytes, err = gitutil.ReadFileAtRef(ctx, *repoRoot, *baseRef, p)
		if err != nil {
			log.Error("failed reading base chart from git ref", zap.Error(err))
			os.Exit(2)
		}
	} else {
		log.Debug("reading base chart from file", zap.String("path", *basePath))
		baseBytes, err = os.ReadFile(*basePath)
		if err != nil {
			log.Error("failed reading base chart from file", zap.Error(err))
			os.Exit(2)
		}
	}

	chartDir := filepath.Dir(*curPath)
	log.Debug("computed chart directory", zap.String("chartDir", chartDir))

	// Optional: update images and/or deps on disk first (so subsequent reads are consistent).
	anyFileWritten := false
	if *write {
		log.Debug("write mode enabled")
		if *updateImages {
			written, err := updateImagesInChartDir(ctx, chartDir, *scanGlob)
			if err != nil {
				log.Error("update images failed", zap.Error(err))
				os.Exit(2)
			}
			anyFileWritten = anyFileWritten || written
			log.Debug("update images completed", zap.Bool("anyWritten", written))
		}
		if *updateDeps {
			written, err := updateDepsInChartYAML(ctx, chartDir)
			if err != nil {
				log.Error("update deps failed", zap.Error(err))
				os.Exit(2)
			}
			anyFileWritten = anyFileWritten || written
			log.Debug("update deps completed", zap.Bool("anyWritten", written))
		}
	}

	curBytes, err := os.ReadFile(*curPath)
	if err != nil {
		log.Error("failed reading current chart", zap.Error(err), zap.String("path", *curPath))
		os.Exit(2)
	}

	baseMeta, err := chart.LoadMeta(baseBytes)
	if err != nil {
		log.Error("failed parsing base chart metadata", zap.Error(err))
		os.Exit(2)
	}
	curMeta, err := chart.LoadMeta(curBytes)
	if err != nil {
		log.Error("failed parsing current chart metadata", zap.Error(err))
		os.Exit(2)
	}

	lvl := chart.ComputeChangeLevel(baseMeta, curMeta)
	log.Debug("computed change level",
		zap.String("baseVersion", baseMeta.Version),
		zap.String("baseAppVersion", baseMeta.AppVersion),
		zap.String("curVersion", curMeta.Version),
		zap.String("curAppVersion", curMeta.AppVersion),
		zap.String("level", string(rune(lvl))),
	)

	ast, err := yamlutil.ParseBytes(curBytes)
	if err != nil {
		log.Error("failed parsing current chart yaml", zap.Error(err))
		os.Exit(2)
	}

	changed, err := chart.ApplyChartVersionBump(ast, lvl)
	if err != nil {
		log.Error("failed applying chart version bump", zap.Error(err))
		os.Exit(2)
	}
	log.Debug("applied chart version bump", zap.Bool("changed", changed))

	out, err := yamlutil.Render(ast)
	if err != nil {
		log.Error("failed rendering chart yaml", zap.Error(err))
		os.Exit(2)
	}

	didWriteChart := false
	if *write && changed {
		outBytes := []byte(out)
		// Don’t touch the file if the rendered bytes are identical.
		if !bytes.Equal(curBytes, outBytes) {
			log.Debug("writing updated Chart.yaml", zap.String("path", *curPath))
			if err := os.WriteFile(*curPath, outBytes, 0o644); err != nil {
				log.Error("failed writing updated Chart.yaml", zap.Error(err))
				os.Exit(2)
			}
			didWriteChart = true
		} else {
			log.Debug("rendered Chart.yaml identical; skipping write")
		}
	}

	if !*write {
		// Tool contract: emit resulting Chart.yaml to stdout.
		fmt.Print(out)
	}

	writeGithubOutputChanged(ctx, anyFileWritten || didWriteChart)
	log.Debug("done", zap.Bool("changed", anyFileWritten || didWriteChart))
}

func newLogger(verbosity int) *zap.Logger {
	cfg := zap.NewProductionConfig()
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	cfg.Level = zap.NewAtomicLevelAt(levelForVerbosity(verbosity))
	// In debug, make it easier to correlate logs with code.
	if verbosity >= 6 {
		cfg.EncoderConfig.CallerKey = "caller"
		cfg.EncoderConfig.EncodeCaller = zapcore.ShortCallerEncoder
		cfg.Development = true
	}
	log, err := cfg.Build(zap.AddStacktrace(zapcore.ErrorLevel))
	if err != nil {
		// As a last resort. If zap can't build, we still need *some* output.
		return zap.NewNop()
	}
	return log
}

func levelForVerbosity(v int) zapcore.Level {
	// Convention for this repo:
	// -v 0 : info+error (quiet)
	// -v 6+: debug
	if v >= 6 {
		return zapcore.DebugLevel
	}
	return zapcore.InfoLevel
}

func updateDepsInChartYAML(ctx context.Context, chartDir string) (bool, error) {
	log := logutil.FromContext(ctx).With(zap.String("func", "updateDepsInChartYAML"), zap.String("chartDir", chartDir))
	chartPath := filepath.Join(chartDir, "Chart.yaml")
	log.Debug("resolving dependency updates", zap.String("chartPath", chartPath))

	resolved, err := helmdeps.ResolveLatestDependencies(ctx, chartPath)
	if err != nil {
		return false, err
	}
	log.Debug("resolved dependency candidates", zap.Int("count", len(resolved)))
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
		log.Debug("dependency resolution",
			zap.String("name", r.Name),
			zap.Int("index", r.Index),
			zap.String("repo", r.Repository),
			zap.String("old", r.OldVersion),
			zap.String("new", r.NewVersion),
		)
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
		log.Debug("no dependency versions changed")
		return false, nil
	}

	out, err := yamlutil.Render(ast)
	if err != nil {
		return false, err
	}
	outBytes := []byte(out)
	if !bytes.Equal(b, outBytes) {
		log.Debug("writing updated Chart.yaml deps", zap.String("path", chartPath))
		if err := os.WriteFile(chartPath, outBytes, 0o644); err != nil {
			return false, err
		}
		return true, nil
	}
	log.Debug("rendered Chart.yaml identical after deps update; skipping write")
	return false, nil
}

func updateImagesInChartDir(ctx context.Context, chartDir, globCSV string) (bool, error) {
	log := logutil.FromContext(ctx).With(zap.String("func", "updateImagesInChartDir"), zap.String("chartDir", chartDir), zap.String("scanGlob", globCSV))
	globs := splitCSV(globCSV)
	log.Debug("expanded scan globs", zap.Strings("globs", globs))

	files := map[string]struct{}{}
	for _, g := range globs {
		pattern := filepath.Join(chartDir, g)
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return false, err
		}
		log.Debug("glob matches", zap.String("pattern", pattern), zap.Int("matches", len(matches)))
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
		fileLog := log.With(zap.String("file", p))
		dirs, err := directives.ScanFileForImageDirectives(ctx, p)
		if err != nil {
			return false, err
		}
		fileLog.Debug("scanned for bump directives", zap.Int("directives", len(dirs)))
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
			dLog := fileLog.With(
				zap.Int("line", d.Line),
				zap.String("yamlPath", d.YAMLPath),
				zap.String("image", d.Image),
				zap.String("strategy", d.Strategy),
				zap.String("constraint", d.Constraint),
				zap.String("tagRegex", d.TagRegex),
				zap.Bool("allowPrerelease", d.AllowPrerelease),
				zap.String("platform", d.Platform),
			)

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
				dLog.Debug("resolving digest from tag", zap.String("tagPath", tagPath), zap.String("tag", tag))
				digest, err := imageresolver.ResolveDigest(ctx, d.Image, tag, d.Platform, nil)
				if err != nil {
					return false, fmt.Errorf("%s:%d: %w", p, d.Line, err)
				}
				newValue = digest
			case "literal", "regex", "semver":
				dLog.Debug("resolving tag")
				tag, err := imageresolver.ResolveTag(ctx, d.Image, strings.ToLower(strategy), d.Constraint, d.TagRegex, d.AllowPrerelease, nil)
				if err != nil {
					return false, fmt.Errorf("%s:%d: %w", p, d.Line, err)
				}
				newValue = tag
			default:
				return false, fmt.Errorf("%s:%d: unknown strategy %q", p, d.Line, d.Strategy)
			}

			dLog.Debug("resolved new value", zap.String("current", d.CurrentText), zap.String("new", newValue))
			c, err := yamlutil.SetString(ast, d.YAMLPath, newValue)
			if err != nil {
				return false, fmt.Errorf("%s:%d: failed to set %s: %w", p, d.Line, d.YAMLPath, err)
			}
			fileChanged = fileChanged || c
		}

		if !fileChanged {
			fileLog.Debug("no changes to apply")
			continue
		}

		out, err := yamlutil.Render(ast)
		if err != nil {
			return false, err
		}
		outBytes := []byte(out)
		if !bytes.Equal(b, outBytes) {
			fileLog.Debug("writing updated file")
			if err := os.WriteFile(p, outBytes, 0o644); err != nil {
				return false, err
			}
			anyWritten = true
		} else {
			fileLog.Debug("rendered file identical; skipping write")
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
	if !strings.HasPrefix(p, "$.") {
		return p
	}
	idx := strings.LastIndex(p, ".")
	if idx <= 1 {
		return "$"
	}
	return p[:idx]
}

func writeGithubOutputChanged(ctx context.Context, changed bool) {
	log := logutil.FromContext(ctx).With(zap.String("func", "writeGithubOutputChanged"), zap.Bool("changed", changed))
	outPath := os.Getenv("GITHUB_OUTPUT")
	if outPath == "" {
		log.Debug("GITHUB_OUTPUT not set; skipping")
		return
	}

	f, err := os.OpenFile(outPath, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		log.Debug("failed opening GITHUB_OUTPUT", zap.Error(err), zap.String("path", outPath))
		return
	}
	defer f.Close()

	if changed {
		_, _ = fmt.Fprintln(f, "changed=true")
		return
	}
	_, _ = fmt.Fprintln(f, "changed=false")
}
