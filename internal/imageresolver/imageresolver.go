package imageresolver

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"

	"github.com/Masterminds/semver/v3"
	"github.com/google/go-containerregistry/pkg/authn"
	"github.com/google/go-containerregistry/pkg/crane"
	"github.com/google/go-containerregistry/pkg/name"
	"github.com/google/go-containerregistry/pkg/v1"
	"github.com/google/go-containerregistry/pkg/v1/remote"
)

// Options control registry access and selection.
//
// Full image repository path is required (e.g. ghcr.io/org/app). No implicit docker.io.
type Options struct {
	Keychain authn.Keychain
	Context  context.Context
}

type cand struct {
	tag string
	ver *semver.Version
}

func defaultOptions() Options {
	return Options{Keychain: ghcrKeychain{fallback: authn.DefaultKeychain}, Context: context.Background()}
}

// ResolveTag returns the selected tag for an image based on strategy.
//
// strategy: semver|regex|literal
//
// - semver: choose highest semver tag (optionally constrained). Excludes prereleases unless allowPrerelease=true.
// - regex: filter tags by tagRegex. If regex has a capture group containing a semver, ordering uses that.
// - literal: requires tagRegex that matches exactly one tag; that tag is returned.
func ResolveTag(imageRepo, strategy, constraint, tagRegex string, allowPrerelease bool, opts *Options) (string, error) {
	if imageRepo == "" {
		return "", fmt.Errorf("image repository must be provided")
	}
	if !strings.Contains(imageRepo, "/") || !strings.Contains(imageRepo, ".") {
		// Keep this strict; user requested full path always.
		return "", fmt.Errorf("image repository must be a full path like ghcr.io/org/image: %q", imageRepo)
	}
	if opts == nil {
		o := defaultOptions()
		opts = &o
	}

	strategy = strings.TrimSpace(strategy)
	if strategy == "" {
		strategy = "semver"
	}

	craneOpts := []crane.Option{crane.WithAuthFromKeychain(opts.Keychain), crane.WithContext(opts.Context)}
	tags, err := crane.ListTags(imageRepo, craneOpts...)
	if err != nil {
		return "", err
	}
	if len(tags) == 0 {
		return "", fmt.Errorf("no tags found for %s", imageRepo)
	}

	switch strategy {
	case "semver":
		return pickSemverTag(tags, constraint, allowPrerelease)
	case "regex":
		if tagRegex == "" {
			return "", fmt.Errorf("strategy=regex requires tagRegex")
		}
		return pickRegexTag(tags, tagRegex, allowPrerelease)
	case "literal":
		if tagRegex == "" {
			return "", fmt.Errorf("strategy=literal requires tagRegex")
		}
		return pickLiteralTag(tags, tagRegex)
	default:
		return "", fmt.Errorf("unknown strategy: %q", strategy)
	}
}

// ResolveDigest resolves the manifest digest for imageRepo:tag.
// If platform is non-empty (e.g. linux/amd64), it selects that platform in an index.
func ResolveDigest(imageRepo, tag, platform string, opts *Options) (string, error) {
	if imageRepo == "" || tag == "" {
		return "", fmt.Errorf("image repository and tag are required to resolve digest")
	}
	if opts == nil {
		o := defaultOptions()
		opts = &o
	}

	refStr := imageRepo + ":" + tag
	ref, err := name.ParseReference(refStr)
	if err != nil {
		return "", err
	}

	remoteOpts := []remote.Option{remote.WithAuthFromKeychain(opts.Keychain), remote.WithContext(opts.Context)}
	if platform != "" {
		plat, err := parsePlatform(platform)
		if err != nil {
			return "", err
		}
		remoteOpts = append(remoteOpts, remote.WithPlatform(*plat))
	}

	desc, err := remote.Get(ref, remoteOpts...)
	if err != nil {
		return "", err
	}
	return desc.Descriptor.Digest.String(), nil
}

func parsePlatform(p string) (*v1.Platform, error) {
	parts := strings.Split(p, "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid platform %q, expected os/arch (e.g. linux/amd64)", p)
	}
	return &v1.Platform{OS: parts[0], Architecture: parts[1]}, nil
}

func pickSemverTag(tags []string, constraint string, allowPrerelease bool) (string, error) {
	var c *semver.Constraints
	if strings.TrimSpace(constraint) != "" {
		cc, err := semver.NewConstraint(constraint)
		if err != nil {
			return "", fmt.Errorf("invalid constraint %q: %w", constraint, err)
		}
		c = cc
	}

	cands := make([]cand, 0, len(tags))
	for _, t := range tags {
		v, err := semver.NewVersion(t)
		if err != nil {
			continue
		}
		if !allowPrerelease && v.Prerelease() != "" {
			continue
		}
		if c != nil && !c.Check(v) {
			continue
		}
		cands = append(cands, cand{tag: t, ver: v})
	}
	if len(cands) == 0 {
		if c != nil {
			return "", fmt.Errorf("no semver tags match constraint %q", constraint)
		}
		return "", fmt.Errorf("no semver tags found")
	}

	sort.Slice(cands, func(i, j int) bool { return cands[i].ver.LessThan(cands[j].ver) })
	bestVer := cands[len(cands)-1].ver
	bestTags := make([]string, 0, 2)
	for _, it := range cands {
		if it.ver.Equal(bestVer) {
			bestTags = append(bestTags, it.tag)
		}
	}
	if len(bestTags) == 1 {
		return bestTags[0], nil
	}
	// Prefer no 'v' prefix when multiple tags map to same semver.
	sort.Strings(bestTags)
	for _, t := range bestTags {
		if !strings.HasPrefix(t, "v") {
			return t, nil
		}
	}
	return bestTags[0], nil
}

func pickRegexTag(tags []string, tagRegex string, allowPrerelease bool) (string, error) {
	re, err := regexp.Compile(tagRegex)
	if err != nil {
		return "", fmt.Errorf("invalid tagRegex %q: %w", tagRegex, err)
	}

	// If regex has at least one capturing group, try to parse group 1 as semver.
	useCaptureSemver := re.NumSubexp() >= 1
	cands := make([]cand, 0)
	for _, t := range tags {
		m := re.FindStringSubmatch(t)
		if m == nil {
			continue
		}
		if useCaptureSemver {
			v, err := semver.NewVersion(m[1])
			if err != nil {
				continue
			}
			if !allowPrerelease && v.Prerelease() != "" {
				continue
			}
			cands = append(cands, cand{tag: t, ver: v})
		} else {
			cands = append(cands, cand{tag: t, ver: nil})
		}
	}
	if len(cands) == 0 {
		return "", fmt.Errorf("no tags match tagRegex %q", tagRegex)
	}

	if useCaptureSemver {
		sort.Slice(cands, func(i, j int) bool { return cands[i].ver.LessThan(cands[j].ver) })
		bestVer := cands[len(cands)-1].ver
		bestTags := make([]string, 0, 2)
		for _, it := range cands {
			if it.ver.Equal(bestVer) {
				bestTags = append(bestTags, it.tag)
			}
		}
		sort.Strings(bestTags)
		return bestTags[len(bestTags)-1], nil
	}

	sort.Strings(candsTags(cands))
	return candsTags(cands)[len(cands)-1], nil
}

func pickLiteralTag(tags []string, tagRegex string) (string, error) {
	re, err := regexp.Compile(tagRegex)
	if err != nil {
		return "", fmt.Errorf("invalid tagRegex %q: %w", tagRegex, err)
	}
	matches := make([]string, 0)
	for _, t := range tags {
		if re.MatchString(t) {
			matches = append(matches, t)
		}
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("no tags match tagRegex %q", tagRegex)
	}
	if len(matches) > 1 {
		sort.Strings(matches)
		return "", fmt.Errorf("tagRegex %q matched multiple tags; make it more specific (e.g. anchor with ^$). Matches: %v", tagRegex, matches)
	}
	return matches[0], nil
}

func candsTags(cs []cand) []string {
	o := make([]string, 0, len(cs))
	for _, c := range cs {
		o = append(o, c.tag)
	}
	return o
}

// ghcrKeychain tries standard Docker credentials first, then falls back to GITHUB_TOKEN
// for ghcr.io. This avoids having to require a docker login step for public GHCR,
// while still working with private repos when GITHUB_TOKEN has access.
type ghcrKeychain struct {
	fallback authn.Keychain
}

func (g ghcrKeychain) Resolve(resource authn.Resource) (authn.Authenticator, error) {
	// Try default first.
	if g.fallback != nil {
		a, err := g.fallback.Resolve(resource)
		if err == nil {
			// Default keychain may return anonymous; that's fine.
			return a, nil
		}
	}

	if resource.RegistryStr() != "ghcr.io" {
		return authn.Anonymous, nil
	}
	tok := os.Getenv("GITHUB_TOKEN")
	actor := os.Getenv("GITHUB_ACTOR")
	if tok == "" || actor == "" {
		return authn.Anonymous, nil
	}
	return authn.FromConfig(authn.AuthConfig{Username: actor, Password: tok}), nil
}
