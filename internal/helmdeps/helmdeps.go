package helmdeps

import (
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/joejulian/helm-chart-bumper-action/internal/logutil"

	"go.uber.org/zap"

	"github.com/Masterminds/semver/v3"
	"helm.sh/helm/v3/pkg/chartutil"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/getter"
	"helm.sh/helm/v3/pkg/repo"
)

// ResolvedDep is the result for one Chart.yaml dependency.
type ResolvedDep struct {
	Index int
	Name  string
	OldVersion string
	NewVersion string
	Repository string
}

// ResolveLatestDependencies resolves latest versions for Chart.yaml dependencies using Helm's repo index
// handling (HTTP(S) only).
//
// For each dependency:
// - If the dependency version is a semver constraint, choose the highest version satisfying it.
// - Otherwise, choose the highest semver version available.
//
// Non-semver versions in the index are ignored.
func ResolveLatestDependencies(ctx context.Context, chartYAMLPath string) ([]ResolvedDep, error) {
	log := logutil.FromContext(ctx).With(zap.String("func", "helmdeps.ResolveLatestDependencies"), zap.String("chartYAMLPath", chartYAMLPath))
	log.Debug("loading Chart.yaml for dependency resolution")
	meta, err := chartutil.LoadChartfile(chartYAMLPath)
	if err != nil {
		return nil, err
	}
	if len(meta.Dependencies) == 0 {
		return nil, nil
	}

	settings := cli.New()
	getters := getter.All(settings)

	indexCache := map[string]*repo.IndexFile{}

	var out []ResolvedDep
	for i, dep := range meta.Dependencies {
		if dep == nil {
			continue
		}
		log.Debug("considering dependency", zap.Int("index", i), zap.String("name", dep.Name), zap.String("repo", dep.Repository), zap.String("versionExpr", dep.Version))
		repoURL := strings.TrimSpace(dep.Repository)
		if repoURL == "" {
			continue
		}
		u, err := url.Parse(repoURL)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			// For now, only HTTP(S). OCI chart deps could be added later.
			continue
		}

		idx, ok := indexCache[repoURL]
		if !ok {
			cr, err := repo.NewChartRepository(&repo.Entry{URL: repoURL}, getters)
			if err != nil {
				return nil, err
			}
			indexPath, err := cr.DownloadIndexFile()
			if err != nil {
				return nil, err
			}
			idx, err = repo.LoadIndexFile(indexPath)
			if err != nil {
				return nil, err
			}
			indexCache[repoURL] = idx
		}

		cvs := idx.Entries[dep.Name]
		if len(cvs) == 0 {
			continue
		}

		bestTag, err := pickBestSemver(cvs, dep.Version)
		if err != nil {
			return nil, fmt.Errorf("dependency %s: %w", dep.Name, err)
		}
		if bestTag == "" {
			continue
		}
		if bestTag == dep.Version {
			continue
		}
		out = append(out, ResolvedDep{Index: i, Name: dep.Name, OldVersion: dep.Version, NewVersion: bestTag, Repository: repoURL})
	}
	return out, nil
}

func pickBestSemver(versions repo.ChartVersions, versionExpr string) (string, error) {
	// Parse constraint if possible.
	var c *semver.Constraints
	if strings.TrimSpace(versionExpr) != "" {
		if cc, err := semver.NewConstraint(versionExpr); err == nil {
			c = cc
		}
	}

	type cand struct {
		tag string
		ver *semver.Version
	}
	cands := make([]cand, 0, len(versions))
	for _, cv := range versions {
		if cv == nil {
			continue
		}
		v, err := semver.NewVersion(cv.Version)
		if err != nil {
			continue
		}
		if c != nil && !c.Check(v) {
			continue
		}
		cands = append(cands, cand{tag: cv.Version, ver: v})
	}
	if len(cands) == 0 {
		return "", nil
	}
	sort.Slice(cands, func(i, j int) bool {
		return cands[i].ver.LessThan(cands[j].ver)
	})
	return cands[len(cands)-1].tag, nil
}
