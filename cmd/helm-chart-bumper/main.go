package main

import (
	"bytes"
	"flag"
	"fmt"
	"os"

	"github.com/joejulian/helm-chart-bumper-action/internal/chart"
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
		write       = flag.Bool("write", false, "Write updated Chart.yaml back to --cur")
	)
	flag.Parse()

	if *curPath == "" || (*basePath == "" && *baseRef == "") || (*basePath != "" && *baseRef != "") {
		fmt.Fprintln(os.Stderr, "usage: helm-chart-bumper (--base path/to/base/Chart.yaml | --base-ref <git-ref> [--base-ref-path path/in/repo/Chart.yaml]) --cur path/to/cur/Chart.yaml [--repo path/to/repo] [--write]")
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
	didWrite := false
	if *write && changed {
		outBytes := []byte(out)
		// Don’t touch the file if the rendered bytes are identical.
		if !bytes.Equal(curBytes, outBytes) {
			if err := os.WriteFile(*curPath, outBytes, 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
			didWrite = true
		}
	}
	if !*write {
		fmt.Println(out)
	}
	writeGithubOutputChanged(didWrite)
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
