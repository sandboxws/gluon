package release

import (
	"sort"
	"strconv"
	"strings"
)

// parseAPI reads one $GOROOT/api/go1.N.txt.
//
// The grammar, from $GOROOT/api/README and from every file in a 1.27
// distribution:
//
//	pkg <import path>[ (<goos>-<goarch>[-cgo])], <declaration>[ #<issue>]
//
// Two things in the real files that a first attempt gets wrong. A line may not
// be a declaration at all — go1.20.txt:444 is the comment "# freebsd riscv64
// port" — so anything not starting with "pkg " is skipped rather than parsed
// into a Symbol with an empty package. And the platform tag is not decoration:
// 8,864 of go1.20.txt's 9,165 lines are the same syscall declarations repeated
// once per GOOS/GOARCH, so they are collapsed to one Symbol carrying the list.
// Uncollapsed, that release reports nine thousand additions and cannot be read.
func parseAPI(text string) ([]Symbol, error) {
	type key struct{ pkg, decl string }
	seen := map[key]int{}
	var out []Symbol

	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "pkg ") {
			continue
		}
		head, decl, found := strings.Cut(line[len("pkg "):], ", ")
		if !found || decl == "" {
			continue
		}
		pkg, platform := cutPlatform(head)
		decl, issue := cutIssue(decl)

		k := key{pkg, decl}
		if i, ok := seen[k]; ok {
			if platform != "" {
				out[i].Platforms = append(out[i].Platforms, platform)
			}
			continue
		}
		seen[k] = len(out)
		s := Symbol{Pkg: pkg, Decl: decl, Issue: issue}
		if platform != "" {
			s.Platforms = []string{platform}
		}
		out = append(out, s)
	}

	for i := range out {
		sortStrings(out[i].Platforms)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Pkg != out[j].Pkg {
			return out[i].Pkg < out[j].Pkg
		}
		return out[i].Decl < out[j].Decl
	})
	return out, nil
}

// cutPlatform splits "syscall (linux-386)" into its path and its platform.
func cutPlatform(head string) (pkg, platform string) {
	head = strings.TrimSpace(head)
	open := strings.IndexByte(head, ' ')
	if open < 0 || !strings.HasSuffix(head, ")") {
		return head, ""
	}
	rest := head[open+1:]
	if !strings.HasPrefix(rest, "(") {
		return head, ""
	}
	return head[:open], strings.TrimSuffix(strings.TrimPrefix(rest, "("), ")")
}

// cutIssue splits the trailing "#12345" off a declaration.
//
// It is required from go1.19.txt onward and absent before, so a missing one is
// the file's age rather than a parse failure — Symbol.Issue is 0 and Symbol.URL
// returns nothing to print.
func cutIssue(decl string) (string, int) {
	i := strings.LastIndex(decl, " #")
	if i < 0 {
		return decl, 0
	}
	n, err := strconv.Atoi(decl[i+2:])
	if err != nil {
		return decl, 0
	}
	return strings.TrimSpace(decl[:i]), n
}

func sortStrings(s []string) { sort.Strings(s) }

// sortReleases orders newest first, which is the order a reader wants: the
// question is almost always about the release they have or the one before it.
func sortReleases(r []Release) {
	sort.SliceStable(r, func(i, j int) bool { return Compare(r[i].Version, r[j].Version) > 0 })
}
