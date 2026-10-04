package repl

import (
	"errors"
	"os"
	"strings"

	"github.com/sandboxws/gluon/internal/state"
)

const historyLimit = 5000

// history is persisted line-per-entry, which is why multi-line constructs are
// flattened before they get here — see flattenForHistory.
type history struct {
	items []string
	// idx == len(items) means "not browsing"; draft holds what was being typed
	// when browsing started, so Down can restore it.
	idx   int
	draft string
	path  string
}

func loadHistory() *history {
	h := &history{path: historyPath()}
	if h.path != "" {
		if b, err := os.ReadFile(h.path); err == nil {
			for _, line := range strings.Split(string(b), "\n") {
				if line != "" {
					h.items = append(h.items, line)
				}
			}
		}
	}
	if n := len(h.items); n > historyLimit {
		h.items = h.items[n-historyLimit:]
	}
	h.idx = len(h.items)
	return h
}

func (h *history) add(s string) {
	if s == "" {
		return
	}
	// Consecutive duplicates are noise when scrolling back.
	if n := len(h.items); n > 0 && h.items[n-1] == s {
		h.idx = len(h.items)
		return
	}
	h.items = append(h.items, s)
	h.idx = len(h.items)
	// Append eagerly so a crash does not lose the session's history.
	if h.path == "" {
		return
	}
	f, err := os.OpenFile(h.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	f.WriteString(s + "\n")
}

func (h *history) reset() { h.idx = len(h.items); h.draft = "" }

func (h *history) prev(current string) (string, bool) {
	if len(h.items) == 0 || h.idx == 0 {
		return "", false
	}
	if h.idx == len(h.items) {
		h.draft = current
	}
	h.idx--
	return h.items[h.idx], true
}

func (h *history) next() (string, bool) {
	if h.idx >= len(h.items) {
		return "", false
	}
	h.idx++
	if h.idx == len(h.items) {
		return h.draft, true
	}
	return h.items[h.idx], true
}

// search finds the most recent entry containing q, case-insensitively.
func (h *history) search(q string) string {
	if q == "" {
		return ""
	}
	lq := strings.ToLower(q)
	for i := len(h.items) - 1; i >= 0; i-- {
		if strings.Contains(strings.ToLower(h.items[i]), lq) {
			return h.items[i]
		}
	}
	return ""
}

// searchFrom continues a search past the entry currently matched, so repeated
// Ctrl-R walks backwards through matches.
func (h *history) searchFrom(q, after string) string {
	if q == "" {
		return after
	}
	lq := strings.ToLower(q)
	start := len(h.items) - 1
	if after != "" {
		for i := len(h.items) - 1; i >= 0; i-- {
			if h.items[i] == after {
				start = i - 1
				break
			}
		}
	}
	for i := start; i >= 0; i-- {
		if strings.Contains(strings.ToLower(h.items[i]), lq) {
			return h.items[i]
		}
	}
	return after
}

// save trims the file back to the limit. Entries are appended as they happen,
// so this is only about bounding growth.
func (h *history) save() {
	if h.path == "" || len(h.items) <= historyLimit {
		return
	}
	trimmed := h.items[len(h.items)-historyLimit:]
	os.WriteFile(h.path, []byte(strings.Join(trimmed, "\n")+"\n"), 0o600)
}

// errNoHistory is history that could not be read at all — no state directory,
// or a file that will not open. It is distinct from a search that matched
// nothing, because "there is no such line" and "gluon could not look" are
// different answers and only one of them is about what you typed.
var errNoHistory = errors.New("no history file to search")

// searchHistory is every persisted line containing q, case-insensitively, in
// the order the file holds them.
//
// It reads the file rather than the history the TUI has in memory: Core has no
// driver, and the file is the whole of what "the persisted history" means — the
// piped driver keeps no history at all and must still be able to list what a
// pattern would bring in.
//
// Lines beginning with ":" are skipped. History holds meta commands as typed,
// and a meta command is not a session entry — bringing ":reset" back in would
// clear the session it was being brought into, and ":q" would end it. What
// :replay is for is the code you wrote.
func searchHistory(q string) ([]string, error) {
	if strings.TrimSpace(q) == "" {
		return nil, errors.New("nothing to search for")
	}
	path := historyPath()
	if path == "" {
		return nil, errNoHistory
	}
	b, err := os.ReadFile(path)
	if err != nil {
		// A history file that is not there yet is a session that has not
		// written one — the same answer as one that will not open, because in
		// both cases there is nothing to search rather than nothing to find.
		return nil, errNoHistory
	}

	lq := strings.ToLower(q)
	var out []string
	for _, line := range strings.Split(string(b), "\n") {
		if line == "" || strings.HasPrefix(strings.TrimSpace(line), ":") {
			continue
		}
		if strings.Contains(strings.ToLower(line), lq) {
			out = append(out, line)
		}
	}
	return out, nil
}

// historyPath follows XDG: history is state, not config and not data. The walk
// itself is internal/state's, so the next file gluon keeps there finds it ready.
func historyPath() string { return state.File("history") }
