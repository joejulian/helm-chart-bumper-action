package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/joejulian/helm-chart-bumper-action/internal/chart"
	"github.com/joejulian/helm-chart-bumper-action/internal/yamlutil"
)

func main() {
	var (
		basePath = flag.String("base", "", "Path to base Chart.yaml")
		curPath  = flag.String("cur", "", "Path to current Chart.yaml")
		write    = flag.Bool("write", false, "Write updated Chart.yaml back to --cur")
	)
	flag.Parse()

	if *basePath == "" || *curPath == "" {
		fmt.Fprintln(os.Stderr, "usage: helm-chart-bumper --base path/to/base/Chart.yaml --cur path/to/cur/Chart.yaml [--write]")
		os.Exit(2)
	}

	baseBytes, err := os.ReadFile(*basePath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
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
	if *write {
		if changed {
			if err := os.WriteFile(*curPath, []byte(out), 0o644); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
		}
		return
	}
	fmt.Print(out)
}
