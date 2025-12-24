package gitutil

import (
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

// ReadFileAtRef reads the blob at repoRelativePath from the git repository at repoRoot,
// resolved at the given ref.
//
// repoRelativePath must use forward slashes (like paths stored in git).
//
// Examples:
//
//	ReadFileAtRef(".", "HEAD~1", "charts/foo/Chart.yaml")
//	ReadFileAtRef(".", "refs/remotes/origin/main", "charts/foo/Chart.yaml")
func ReadFileAtRef(repoRoot, ref, repoRelativePath string) ([]byte, error) {
	repo, err := git.PlainOpenWithOptions(repoRoot, &git.PlainOpenOptions{DetectDotGit: true})
	if err != nil {
		return nil, fmt.Errorf("open git repo at %q: %w", repoRoot, err)
	}

	// Git stores paths with forward slashes regardless of OS.
	p := filepath.ToSlash(repoRelativePath)
	p = strings.TrimPrefix(p, "./")
	if p == "" {
		return nil, errors.New("empty repoRelativePath")
	}

	hash, err := resolveRevision(repo, ref)
	if err != nil {
		return nil, err
	}

	commit, err := repo.CommitObject(*hash)
	if err != nil {
		return nil, fmt.Errorf("resolve commit for ref %q: %w", ref, err)
	}

	file, err := commit.File(p)
	if err != nil {
		return nil, fmt.Errorf("read %q at ref %q: %w", p, ref, err)
	}

	r, err := file.Reader()
	if err != nil {
		return nil, fmt.Errorf("open reader for %q at ref %q: %w", p, ref, err)
	}
	defer r.Close()

	b, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read %q at ref %q: %w", p, ref, err)
	}
	return b, nil
}

func resolveRevision(repo *git.Repository, ref string) (*plumbing.Hash, error) {
	// Try user-provided ref as-is.
	try := []string{ref}

	// Common conveniences: allow origin/main or main.
	if strings.HasPrefix(ref, "origin/") {
		try = append(try, "refs/remotes/"+ref)
	}
	if !strings.HasPrefix(ref, "refs/") {
		try = append(try, "refs/heads/"+ref)
		try = append(try, "refs/remotes/origin/"+ref)
	}

	var lastErr error
	for _, cand := range try {
		h, err := repo.ResolveRevision(plumbing.Revision(cand))
		if err == nil {
			return h, nil
		}
		lastErr = err
	}

	return nil, fmt.Errorf("unable to resolve git ref %q (tried %v): %w", ref, try, lastErr)
}
