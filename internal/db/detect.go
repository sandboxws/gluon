package db

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/sandboxws/gluon/internal/dsn"
	"github.com/sandboxws/gluon/internal/find"
)

// Kind is the source that produced a candidate.
//
// It is part of the answer, not a detail. "gluon thinks postgres" with no
// reason is the shrug this codebase refuses to have, and a compose service is a
// materially weaker claim than a .env the application actually reads.
type Kind string

const (
	KindEnvFile  Kind = "envfile"
	KindConfFile Kind = "conffile"
	KindCompose  Kind = "compose"
	KindOnDisk   Kind = "ondisk"
	KindGoSource Kind = "source"
)

// Provenance is where a value came from, precisely enough to open the file and
// point at it.
type Provenance struct {
	File string
	Line int
	Key  string
	Kind Kind
	Note string
}

func (p Provenance) String() string {
	s := p.File
	if p.Line > 0 {
		s += fmt.Sprintf(":%d", p.Line)
	}
	if p.Key != "" {
		s += "  " + p.Key
	}
	return s
}

// Rank is the tier a candidate sits in, and the whole of the automatic
// ordering.
type Rank int

const (
	// RankApp is a file the application itself reads at run time. It is the
	// strongest claim available: it is what the program would connect to.
	RankApp Rank = iota + 1
	// RankInfra describes infrastructure rather than configuration. A compose
	// service says a database could exist here, not that this program uses it.
	RankInfra
	// RankOnDisk is a SQLite file nothing referred to. It is unambiguously a
	// database, and nothing in the project says it is the one.
	RankOnDisk
)

func (r Rank) String() string {
	switch r {
	case RankApp:
		return "configuration"
	case RankInfra:
		return "infrastructure"
	default:
		return "found on disk"
	}
}

// SecretRef says how to obtain a connection string later without gluon holding
// one now. Exactly one form is written into a config.
type SecretRef struct {
	Env  string
	File string
	Key  string
}

// A Candidate is one answer, with the evidence for it.
type Candidate struct {
	DSN dsn.DSN
	// From is every place this same target was seen, nearest first. Two
	// sources agreeing is corroboration, not ambiguity, so they merge into one
	// candidate rather than becoming two to refuse between.
	From     []Provenance
	Secret   SecretRef
	Rank     Rank
	Depth    int
	Concerns []string
}

// Detection is what a detection run found, and what it looked at.
type Detection struct {
	Chosen     *Candidate
	Candidates []Candidate
	Ambiguous  bool
	// Note is the one documented tie-break, when it fired. Empty otherwise.
	Note string
	// Searched is every file read, and Roots every directory looked in.
	//
	// Both are load-bearing rather than decorative: without them "found
	// nothing" and "did not look" are the same output, and a project with no
	// .env at all would produce the same report as one where the walk stopped
	// early. Roots matters most in the empty case, which is precisely when
	// Searched has nothing to show.
	Searched []string
	Roots    []string
	Ceiling  string
	Drivers  []Driver
	// Stamp fingerprints what Searched and Roots said at the moment this
	// detection was made, so a caller holding an old one can ask whether it is
	// still the answer without redoing the search. See Stamp.
	Stamp string
}

// Stamp fingerprints the files a detection read and the directories it looked
// in.
//
// It is what lets a cached detection outlive a build-list change: recomputing
// it is one os.Stat per file detection actually opened — bounded by maxFiles —
// against a walk of every ancestor and every .env under the module, plus
// parsing whatever that turns up.
//
// Both halves are load-bearing. The files catch an edited .env or compose file.
// The directories catch the case the files cannot see at all: a file detection
// looked for and did not find is in no list, so a project that grows its first
// .env would otherwise keep answering from a cache that never read one — and a
// directory's mtime moves when an entry appears in it.
//
// Size and mtime rather than content: this runs on the way to answering :db,
// and hashing every candidate file would be most of the cost the cache exists
// to avoid. A file rewritten to the same length within one filesystem tick is
// the miss that buys that, and :reload is the answer to it.
func Stamp(searched, roots []string) string {
	h := sha256.New()
	stat := func(kind byte, paths []string) {
		sorted := append([]string(nil), paths...)
		sort.Strings(sorted)
		for _, p := range sorted {
			st, err := os.Stat(p)
			if err != nil {
				// A file that has been removed is as much a change as one that
				// was edited, and it has to hash differently from one that is
				// there and empty.
				fmt.Fprintf(h, "%c\x00%s\x00gone\n", kind, p)
				continue
			}
			if st.IsDir() {
				// A directory's size is not a fact about its entries on every
				// filesystem; its mtime is.
				fmt.Fprintf(h, "%c\x00%s\x00%d\n", kind, p, st.ModTime().UnixNano())
				continue
			}
			fmt.Fprintf(h, "%c\x00%s\x00%d\x00%d\n", kind, p, st.Size(), st.ModTime().UnixNano())
		}
	}
	stat('f', searched)
	stat('d', roots)
	return hex.EncodeToString(h.Sum(nil))
}

// maxFiles bounds the work. A truncated search that says nothing is a shrug, so
// Detection records when this fires.
const maxFiles = 2000

// envFileGlobs, composeNames and confNames are the files worth opening.
//
// The names get a file *considered*; what makes it a match is always its
// content. That split is invariant 12: the name is a cheap filter, never the
// evidence.
var (
	envFileGlobs = []string{".env", ".env.local", ".env.development", ".env.dev", ".env.example"}
	composeNames = []string{"compose.yaml", "compose.yml", "docker-compose.yml", "docker-compose.yaml"}
	confStems    = []string{"config", "app", "settings", "local", "development", "dev", "database"}
	confExts     = []string{".yaml", ".yml", ".toml", ".json"}
	confDirs     = []string{".", "config", "configs", ".config", "deploy", "etc"}
)

// Detect finds the databases a project uses.
//
// root is the host module's directory. requires is the build list, which says
// which drivers are available — evidence, never a candidate: a project that
// requires lib/pq and configures nothing is a different answer from one with a
// DSN and no driver, and :query needs both halves.
func Detect(root string, requires []string) Detection {
	res := Detection{Drivers: DriversIn(requires)}

	// Absolute from here on. The up-pass resolves ancestors to absolute paths
	// and the down-pass walks from whatever it was given, so a relative root
	// makes the same file arrive under two spellings — which defeats the dedup
	// and reports one compose file as two sources.
	if abs, err := filepath.Abs(root); err == nil {
		root = abs
	}

	ceiling, hasRepo := find.Repo(root)
	if hasRepo {
		res.Ceiling = ceiling
	}

	var cands []Candidate
	seen := map[string]bool{}
	files := 0

	consider := func(path string, depth int) {
		if files >= maxFiles || seen[path] {
			return
		}
		seen[path] = true
		files++
		res.Searched = append(res.Searched, path)
		cands = append(cands, scanFile(path, depth)...)
	}

	// The up-pass reads each ancestor's own entries and never descends into
	// them. A compose file at the repository root belongs to the module below
	// it; a .env in a sibling service does not, and that sibling is exactly the
	// lookalike invariant 12 is about.
	ancestors := []string{root}
	if hasRepo {
		ancestors = find.Ancestors(root, ceiling)
	}
	for depth, dir := range ancestors {
		res.Roots = append(res.Roots, dir)
		for _, name := range candidateNames() {
			p := filepath.Join(dir, name)
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				consider(p, depth)
			}
		}
	}

	// The down-pass is rooted at the module, not the repository, and stops at a
	// nested go.mod.
	_ = find.Walk(root, 3, func(p string, d fs.DirEntry) error {
		if files >= maxFiles {
			return fs.SkipAll
		}
		if interesting(filepath.Base(p)) {
			consider(p, 0)
			return nil
		}
		// Every other file gets the cheap SQLite probe: a database called
		// notes.txt is still a database.
		if isProbablyData(p) && dsn.IsSQLiteFile(p) {
			files++
			abs, err := filepath.Abs(p)
			if err != nil {
				abs = p
			}
			cands = append(cands, Candidate{
				DSN:  dsn.FromFile(abs, nil),
				From: []Provenance{{File: p, Kind: KindOnDisk}},
				Rank: RankOnDisk,
			})
		}
		return nil
	})

	if files >= maxFiles {
		res.Note = fmt.Sprintf("stopped after %d files — pin one in gluon.toml if this is wrong", maxFiles)
	}

	cands = append(cands, scanGoSource(root)...)
	res.Candidates = merge(cands)
	choose(&res)
	// Last, so it fingerprints everything the search touched. Taken after the
	// reads rather than before: a file changed while the walk was running
	// stamps as it was left, and the next check sees the difference.
	res.Stamp = Stamp(res.Searched, res.Roots)
	return res
}

func candidateNames() []string {
	var out []string
	out = append(out, envFileGlobs...)
	out = append(out, composeNames...)
	for _, stem := range confStems {
		for _, ext := range confExts {
			out = append(out, stem+ext)
		}
	}
	return out
}

func interesting(name string) bool {
	for _, n := range envFileGlobs {
		if name == n {
			return true
		}
	}
	for _, n := range composeNames {
		if name == n {
			return true
		}
	}
	ext := strings.ToLower(filepath.Ext(name))
	stem := strings.TrimSuffix(name, filepath.Ext(name))
	for _, e := range confExts {
		if ext != e {
			continue
		}
		for _, s := range confStems {
			if strings.EqualFold(stem, s) {
				return true
			}
		}
	}
	return false
}

// isProbablyData keeps the SQLite probe from opening every source file. It is a
// cost filter, not the test: the header is still what decides.
func isProbablyData(p string) bool {
	switch strings.ToLower(filepath.Ext(p)) {
	case ".go", ".md", ".txt", ".json", ".yaml", ".yml", ".toml", ".sql", ".mod", ".sum":
		return false
	}
	if st, err := os.Stat(p); err != nil || st.Size() < int64(len(sqliteMagicLen)) {
		return false
	}
	return true
}

var sqliteMagicLen = "SQLite format 3\x00"

// scanFile dispatches one file to whichever reader its shape calls for.
func scanFile(path string, depth int) []Candidate {
	base := filepath.Base(path)
	switch {
	case strings.HasPrefix(base, ".env"):
		return scanEnvFile(path, depth)
	case isComposeName(base):
		return scanCompose(path, depth)
	default:
		return scanConfFile(path, depth)
	}
}

func isComposeName(name string) bool {
	for _, n := range composeNames {
		if name == n {
			return true
		}
	}
	return false
}

// merge unions candidates that name the same target.
//
// Two sources agreeing is corroboration, not ambiguity. A compose file names
// the superuser and the app's .env names the app user, for one database — and
// treating that as two answers to refuse between would break the common case
// permanently. dsn.Target excludes credentials for exactly this reason.
func merge(in []Candidate) []Candidate {
	byTarget := map[string]*Candidate{}
	var order []string
	for i := range in {
		c := in[i]
		t := c.DSN.Target()
		if existing, ok := byTarget[t]; ok {
			existing.From = append(existing.From, c.From...)
			existing.Concerns = append(existing.Concerns, c.Concerns...)
			if c.Rank < existing.Rank {
				existing.Rank = c.Rank
				existing.DSN = c.DSN
				existing.Secret = c.Secret
			}
			if c.Depth < existing.Depth {
				existing.Depth = c.Depth
			}
			continue
		}
		cp := c
		byTarget[t] = &cp
		order = append(order, t)
	}
	out := make([]Candidate, 0, len(order))
	for _, t := range order {
		out = append(out, *byTarget[t])
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Rank != out[j].Rank {
			return out[i].Rank < out[j].Rank
		}
		if out[i].Depth != out[j].Depth {
			return out[i].Depth < out[j].Depth
		}
		return out[i].DSN.Target() < out[j].DSN.Target()
	})
	return out
}

// secondary is the single name-based rule gluon applies, and it says so when it
// fires.
//
// Shape decides whether a value is a DSN; this decides only which of two values
// that already parse is the project's main one. It is invariant 13's one kind of
// exception — a single documented tie-break, announced when it fires — and it
// exists because DATABASE_URL beside TEST_DATABASE_URL is the ordinary
// case, and refusing every time would make the feature feel broken rather than
// careful.
var secondary = []string{
	"TEST", "SHADOW", "REPLICA", "READONLY", "READ_ONLY",
	"ANALYTICS", "LEGACY", "OLD", "BACKUP", "MIGRATE", "MIGRATION", "DIRECT",
}

// isSecondary matches whole underscore-separated elements, so LATEST_URL is not
// TEST_URL.
func isSecondary(key string) (string, bool) {
	for _, part := range strings.Split(strings.ToUpper(key), "_") {
		for _, s := range secondary {
			if part == s {
				return s, true
			}
		}
	}
	return "", false
}

// choose picks the answer, or refuses.
func choose(res *Detection) {
	if len(res.Candidates) == 0 {
		return
	}
	top := res.Candidates[0].Rank
	var tier []Candidate
	for _, c := range res.Candidates {
		if c.Rank == top {
			tier = append(tier, c)
		}
	}
	if len(tier) == 1 {
		res.Chosen = &tier[0]
		return
	}

	// The one documented demotion, applied once and reported.
	var primary []Candidate
	var demoted []string
	for _, c := range tier {
		key := ""
		for _, p := range c.From {
			if p.Key != "" {
				key = p.Key
				break
			}
		}
		if word, is := isSecondary(key); is {
			demoted = append(demoted, key+" ("+word+")")
			continue
		}
		primary = append(primary, c)
	}
	if len(primary) == 1 {
		res.Chosen = &primary[0]
		res.Note = "took " + firstKey(primary[0]) + "; de-prioritised " + strings.Join(demoted, ", ")
		return
	}
	res.Ambiguous = true
}

func firstKey(c Candidate) string {
	for _, p := range c.From {
		if p.Key != "" {
			return p.Key
		}
	}
	if len(c.From) > 0 {
		return c.From[0].File
	}
	return c.DSN.Target()
}
