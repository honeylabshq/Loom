// Package buildinfo names the commit this binary was built from, so an
// operator can tell which code is running without reading file dates.
//
// Three sources, first match wins:
//
//  1. -ldflags "-X github.com/honeylabshq/Loom/internal/buildinfo.commit=<sha>",
//     which the deploy build sets.
//  2. commit.txt, which `git archive` fills in through export-subst
//     (.gitattributes), so a build from an exported tree with no .git still
//     carries its commit.
//  3. The VCS stamp the Go toolchain embeds when it builds inside a git
//     checkout (vcs.revision, with -dirty when vcs.modified is true).
//
// Nothing found gives "unknown".
package buildinfo

import (
	_ "embed"
	"regexp"
	"runtime"
	"runtime/debug"
	"strings"
	"time"
)

// commit is set at link time; empty when the build did not set it.
var commit string

//go:embed commit.txt
var archived string

var started = time.Now().UTC()

var shaRe = regexp.MustCompile(`^[0-9a-f]{7,40}(-dirty)?$`)

// Commit is the commit the binary was built from, or "unknown".
func Commit() string {
	return resolve(commit, archived, vcsRevision)
}

func resolve(linked, archive string, vcs func() (string, bool)) string {
	if c := strings.TrimSpace(linked); shaRe.MatchString(c) {
		return c
	}
	// A tree that did not come from git archive holds the literal "$Format:%H$".
	if c := strings.TrimSpace(archive); shaRe.MatchString(c) {
		return c
	}
	if rev, modified := vcs(); shaRe.MatchString(rev) {
		if modified {
			return rev + "-dirty"
		}
		return rev
	}
	return "unknown"
}

func vcsRevision() (string, bool) {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "", false
	}
	var rev string
	var modified bool
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			modified = s.Value == "true"
		}
	}
	return rev, modified
}

// Started is when this process started, in UTC.
func Started() time.Time { return started }

// GoVersion is the toolchain the binary was built with.
func GoVersion() string { return runtime.Version() }
