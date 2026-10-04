# gluon roadmap

Read this before extending gluon. It records what is built, what comes next,
what was rejected and why, and the measured facts that make several of these
decisions non-obvious. Everything marked **verified** was checked against a
real run or the Go source, not assumed.

---

## Shipped since v10: screenshots taken, never drawn (2026-09-23)

The site had transcripts and no pictures, and the pictures a launch needs — a
post, a link's card — are the ones a transcript cannot be: colour, a view, the
hint drawn beside a line, the startup screen. VHS renders into a terminal of
its own, not the one people use, and its three tapes predated scratchpads. The
one place the guides drew something — what tab offers — had to say it was an
illustration.

### A shot is a file, and the window is the tool's alone

`tapes/<id>.shot` says where a shot stands (`Host shop`, `Serve`), what it
types (`Line`, `Type`, `Keys`, `Paste`), what it waits for, and when it is
captured; its `Alt` line is the image's text wherever the image is shown.
`tools/shots` — a module of its own, with no dependencies, so the root
`go.mod` does not change — takes every shot in one new window of the Ghostty
already running: the release build, a gluon that has never run, a fresh copy
of `testdata/shop`, and each of the site's two palettes. `just promo` composes
what the docs and a post need, and `{% shot "id" %}` puts one on a page with
both palettes' images, of which the page shows the one it is in.

It touches no window but its own. Its AppleScript is embedded templates that
take values only as argv and are linted before they run: none can say `quit`,
type into an application, run a shell, or name a window by its position. A
window is ours only if our `new window` returned its id and that id was not
open before; it is sized through System Events by a nonce title, floated,
and closed by its program exiting — by id only once nothing runs in it, so no
confirmation is ever waiting. It opens only when the screen is live and
nobody has typed for twenty seconds, because a new window takes the keyboard.

**Verified, and the reasons for the shapes above:**

- Ghostty 1.3's dictionary opens a window from a surface configuration, sends
  bytes through `perform action "text:…"` and `"csi:…"`, pastes with
  `input text`, and closes a window by id. It has no bounds property.
- Closing a live window asks first; a window whose process exits closes
  without asking. So teardown is gluon quitting, and a window is closed by id
  only with no process in it.
- `shell-integration = nushell` adds nushell's arguments to whatever command a
  window runs. `/bin/sh` rejects them, and Ghostty keeps the window open, empty,
  to show why — so the wrapper is exec'd from `nu -c` when that is configured.
- Ghostty applies a title from its terminal's output in its own time: a
  wrapper having printed one is not the window having it.
- Behind a screen saver or a lock, System Events sees no window at all, and a
  covered window is not drawn — a capture would be of a frame from before.
- The prompt's cursor blinks, and its blink resets only when it moves. Four
  captures a third of a blink apart cannot all land in one half of it, and of
  two that differ, the brighter is the one with the cursor lit.

### What the screenshots found

Six things no transcript could show, each fixed with its test in its own
commit:

- The startup screen's plugins row — fourteen names in the shop, eighty-four
  columns — was carried on by the terminal at column 0, through the label
  column. A row now wraps under its own value, at a space.
- `:time` reported a row of `gofmt 0s` on a one-line call: phases are
  collected for the whole package, and the signature hint type-checks on every
  keystroke. A line now starts its account empty.
- `:db`'s table and `:query`'s footer printed a database file's absolute path,
  the widest thing in the table. A file under home is written from `~`, which
  is what the transcripts have always shown; `-json` keeps the whole path.
- A view's note was drawn as one line and cut by the terminal: `:since`'s
  stopped at "rather". It wraps to the view's width, and the table gives up the
  rows it takes.
- A paste was echoed in whatever order `tea.Batch` finished its prints — a
  pasted `p.Dist2()` above the `p := Point{3, 4}` it needs. The echoes are a
  sequence now, ahead of the evaluation; a typed line's echo is sequenced
  ahead of its answer too.
- `:scratch`'s view named the scratch directory by its absolute path, where
  the settings view beside it already shortened its file.

### Surfaced, and left for a decision

- A value's table wider than the terminal is drawn at its natural width and
  soft-wrapped into broken rules — the response of the shop's `Orders/Get`,
  104 columns, at 100. The renderer knows no width. Fitting a table to one is a
  design question — a cell truncated, a tree instead — so the gRPC shot shows
  the listing and not the call.
- The MCP shot needs an agent session with the server approved, and is taken
  by hand; `tapes/README.md` says how.

---

## Shipped since v10: a documentation site, and transcripts nobody typed (2026-09-23)

v9 made the README the manual — 2,231 lines by the end — and the site one page
beside it, both typed by hand. Two things were wrong with that. A manual in one
file cannot be navigated: 32 of 83 commands were ever shown being typed, and a
reader looking for `:buf` read past `:since` to find it. And a manual typed by
hand drifts in the one place nobody re-reads: the site's database tab showed a
postgres that was never there, its MCP tab listed ten of thirty-three tools, a
`:drop` still said `1 entries left`, and a `live` tab advertised a feature that
is not built.

### The site is generated, and checked by `just test`

`site/` is what is written and `docs/` is what GitHub Pages serves, from main's
`/docs` with no workflow. `internal/docgen` renders one into the other from
`TestDocs` in `cmd/gluon` — `just docs` runs it with `-update`, `just test`
without, and a page that says something the code no longer does fails the
build. The same run lints every page — elements closed in order, no id twice,
every image with its text and size — follows every link to a file and every
fragment to an id, and follows the README's absolute links into the site the
same way. The reference half is the registries, read (the previous entry's
`Reference()`); fifteen guides are the other half, written by hand.

### Every command is worked through, and the build says which is not

A guide includes a transcript with `{% session "name" %}`, and the renderer
notes every command a guide page shows being typed. `TestDocs` fails on a
command in the registry that no guide works through, naming it; `:q` and
`:clear` are the two exempt, because they act on the terminal and print
nothing. A guide links a command, a flag, a setting or a plugin through a
helper that fails the build on a name the registry does not have, and each
command's reference entry links back to the guides that work it.

### A transcript is recorded, never typed

`site/sessions/<name>.gl` is lines for the prompt and `<name>.sh` lines for a
shell. `just docs-sessions` runs each through a real `Core` — drawn the way a
terminal draws, through `RenderAsTerminal` with plain styles, so a value is the
table the prompt shows and not the line a pipe gets — or through `sh` with the
gluon the checkout builds, and writes `<name>.out`. Rendering reads the `.out`
and runs nothing, so `just docs` is fast and the same bytes every time;
recording is behind the `sessions` build tag and runs when a script or gluon
changes. The README's transcripts are spliced from the same recordings.

The recorder stands in for everything a terminal would have done: it writes
what the script says was typed into the editor a line opens (inside a
buffer's regions, as a person would), carries a `:settings` change of form into
the next line as the TUI does, seeds the history `:replay` searches, and runs a
project's sessions in a copy of `testdata/shop` with its servers started. It
keeps the Go proxy off, so a recording fetches nothing. The machine's own paths
are written as a reader's would read — `~/src/shop`, `~/.config/gluon` — and a
table a shorter path was written into is narrowed back to what its cells need,
so its rules still meet.

A page says what the terminal did that its text cannot: a line that opens a
view is badged, a line that opened an editor shows what was written in it, and a
long answer is clipped on the page with how many lines were left out while the
`.out` keeps all of them.

### `testdata/shop`

A small service that exists to be looked at: an HTTP API on chi, a gRPC server
with reflection, SQLite through sqlx with goose migrations, viper settings, a
cobra CLI, a samber/do container, decimal prices, msgpack events and ent's table
set — one of each library a plugin describes, so every plugin command has real
code to answer about. Its seed and its servers are deterministic, so a session
recorded against it reads the same whenever it is recorded. It is also where
the screenshots will stand.

### What recording found

Ten bugs, each fixed with its test in its own commit before the guides:

- `:undo` and `:drop` said `1 entries left`.
- `--help` on a command whose plugin was not active printed one line saying the
  command could not run, where `:help` on it printed its page.
- `:conf` marked a relative SQLite path *redacted* — `RedactValue` compared the
  display form with the input, and resolving the path changed it — though a file
  path carries no secret.
- `gluon init -local` wrote the absolute path a SQLite file had on this machine
  into a `gluon.toml` meant to travel with the project.
- The runtime's goroutine note joined the last line of output that had no final
  newline: `:grpc` named a method's response type `Order[gluon waited 139µs …]`.
- `:plugins`' fixed-width columns ran the longest module paths into the column
  after them.
- After `:get gorm.io/driver/postgres`, `postgres.Open` resolved to whichever
  package named postgres goimports found first in the module cache. The
  packages of the modules a session added now settle a qualifier before
  goimports is asked; a name two of them share, and a standard library name,
  are still goimports' to answer.
- The drain waited its full two seconds on every line after a `sql.Open`, for
  the goroutine `database/sql` keeps until `Close`. It now waits only for
  goroutines package main created.
- The terminal's renderer took a copy of the plugins' hooks when it was
  installed — before `-host`, `:use` or `:get` could activate anything — so no
  third-party renderer ever applied: a `uuid.UUID` drew as sixteen integers.
- A valid `sql.NullString` holding the text `NULL` drew exactly as a SQL `NULL`.

And one of the docs' own: `.gitignore`'s `*.out` matched the recordings, so a
fresh checkout would have failed `TestDocs` on the first one.

### Surfaced, and left for a decision

- `:query -w` on a statement that returns no rows reports `no rows · 0 rows` —
  an `UPDATE` that changed a row says nothing about it. Reporting rows affected
  means running a write through `Exec`, and adds a field to the `-json`
  envelope; the database guide says what `-w` reports meanwhile.
- The first `:race` of every session says the instrumented standard library
  "was compiled for this run", which is true of the first on a machine and not
  of the rest: the note keys on the session's binary, and the build cache may
  already hold the library.
- A line runs in the session's own directory, not the project's, so a relative
  path in the project's code is not found. That is invariant 1's spirit and it
  stays; the project guide says so, with the shop reading `$SHOP_CONFIG`.

---

## Shipped since v10: help that answers for every command (2026-09-23)

`:help :http` printed a summary and a paragraph, and the paragraph was the only
place `:http`'s flags were written down — apart from one error message. Eighteen
builtins had no detail, none had an example, and nothing answered `:http --help`.
Asking was not even safe: `:scratch -h` opened a scratchpad named `h`, `:undo -h`
dropped an entry, `:q -h` quit, and `:get -h` ran `go get -- -h`, because each
command read the token as its own argument.

### Help is answered before a command runs

`meta()` asks `cmdspec.AsksForHelp` on the whole argument before `Run`. `--help`
and `?` are never Go and never SQL, so every command answers them. `-h` and
`-help` are the negation of a variable, so for a command whose argument is a Go
expression they stay Go — and when that fails to compile, the error says
`:t --help is its help`. Deciding `-h` by asking the checker whether `h` is bound
was considered and rejected (below, and invariant 5).

### Arg did not move

A command's argument was one field, `Arg`, and `Arg` is load-bearing: a `<` in
it makes a bare call a usage error, an MCP argument required and a TOML
argument required, and `:help`'s listing is aligned on it with four builtins
already at the gutter. The long form is new data — `internal/cmdspec.Spec`, a
leaf package because both the registry and the plugin command type declare one
— and `TestArgAgreesWithUsage` holds the two together. Every builtin and every
plugin command declares its kind, operands, flags with a line of help each,
examples and neighbours. The kind replaced `sqlCommands` and `plainArgCommands`.

### Why the parsers were not rewritten

A shared flag parser would have to reproduce every edge the fifteen parsers were
built around — flags that must lead so `-nums` stays a negation, `-srcX` a name,
`:http`'s flags after its operands, `:since`'s anywhere — and the risk of the
rewrite would land exactly there. Three tests give the protection instead:
`TestEveryExampleParses` runs each example through its command's own parser;
`TestParsersSpeakOnlyDeclaredFlags` reads each parser's source with `go/ast` for
the flags it spells and requires them declared, and each declared flag spelled;
and every visible flag must appear in an example, which is the proof it parses.

### One page, four ways in

`help.go` builds the page once — synopsis, summary, operands and flags, examples,
detail, see-also, aliases, the MCP tool — and `:help <cmd>`, `<cmd> --help`,
`gluon -e` and the help view all print it. An inactive plugin's command has a
page, and typing it says which plugin it needs instead of "unknown command". A
usage error names `<cmd> --help`.

### An example goes on the prompt, not run

A bare `:help` at a terminal opens a view of every command. Choosing an example
puts its line on the prompt through Ctrl-R's accept path and submits nothing —
an example may fetch or publish, and the reader's Enter is the submission.
`ModalChoice.Fill`, `ModalSpec.Opened` and `ModalEntry.More` are the three fields
it took; entry text pages, which also mended cut-off `:settings` rows.
`updateModal` had been reading the closing line from the view before the key
that closed it, which would have lost a chosen line.

### Completion past the first word, and a hint beside the line

The completion hook saw only a meta command's first argument word. It now hands
the whole argument to the command's declaration: `Spec.At` reads the line the
way the parser does and says what the word under the cursor is, and
`Spec.FlagsFor` and the value sources answer it — flags where they are read,
HTTP methods in the case being typed (invariant 16), themes, settings and their
values, scratchpads, snapshots. `Spec.Rest` is the same reading as a hint: the
rest of the usage, from the element under the cursor on, in the slot the call
signature uses.

### Cost, measured

A flag position costs about 1.3µs and the hint about 2.2µs, against a 12–22µs
keystroke. Every value set that costs more than a map lookup is cached where it
changes: names derived from the build list at `:get` and `:use`, the scratchpads
at `:scratch`.

### Also in this change

- `:guide` printed nothing for a TOML plugin without a guide, because every
  file-backed plugin is a Guider; it now says the plugin ships none, and
  "no plugin named x" is a separate answer.
- `:plugins` lists each plugin's commands under its name.
- MCP tool descriptions carry the synopsis, the flags and the examples as expr.
  A static tool marks a flag that runs code as the prompt's alone, and
  `TestStaticToolsStripEveryFlagThatRuns` holds that `stripDynamic` knows every
  such flag.
- `repl.Reference()` is every builtin and every plugin's commands, each provider
  kept: the one reading of the registry the docs site will generate from.

---

## Shipped since v10: the startup screen (2026-09-13)

`gluon`'s front door was four unstyled `fmt.Printf` calls — the only
`fmt.Print*` in the TUI driver, and the only user-facing surface in the program
that reached none of the sixteen roles or twenty-three palettes:

```
gluon 0.0.0-20260907221914-8053da02ce95+dirty · go1.27.0
:help for commands · tab completes · ctrl-r searches history · :q to quit

opening scratchpad default…
scratchpad default — 1 entry restored; attached to github.com/acme/inventory-api
```

A pseudo-version owning line one. A progress line stacked above its own answer
and never taken back. "scratchpad default" twice on consecutive lines. And the
most useful fact on the screen — which module this session can see — arriving
after a semicolon, because `loadPad` joins its notes with `"; "`.

It now answers one question instead of four half-questions: *what does gluon
know about where I am*.

```
  █▀▀ █   █ █ █▀█ █▄ █
  █▄█ █▄▄ █▄█ █▄█ █ ▀█   dev · 8053da0 · dirty · go1.27.0
                         a REPL and scratch runner for Go

  scratchpad  default · 1 entry restored
  host        github.com/acme/inventory-api
  plugins     pgx, gorm
  theme       gruvppuccin-mocha

  :help for commands · tab completes · ctrl-r searches history · :q to quit
```

### A row appears only where there is something to say

That is the design and not the polish. A screen reporting `database  none`
every morning is furniture, and the difference between furniture and news is
whether the row is absent or is present saying nothing. So a bare directory on
defaults is four lines and a project is as many as it has facts. The theme is
named only when it is not the default. The plugins are the ones the project's
build list activated — the stdlib seven are active in every session and so
distinguish none.

### The wordmark is `prompt`, and there is no seventeenth role

`TestBuiltinsAreComplete` requires every shipped theme to set every role, so a
new one would be twenty-three file edits, plus the readability floors, the
picker's swatches, `RoleStyles` and an `options.go` family row. Against that:
`prompt` already *is* the identity colour. `go.toml` calls it "the Go accent"
and `import.go` records the pairing as "the prompt has always been the colour a
type is". A wordmark in `Theme.Prompt` is gluon drawn in the colour it already
signs its name in, and it moves with the user's theme for nothing.

### The transient became the row it was about

`opening scratchpad default…` was a line gluon could not take back, and
invariant 19 makes scrollback the transcript. Now the scratchpad's own row is
drawn unfinished — `scratchpad  opening default…` — and is overwritten where it
stands, so the line does not move, it gains text.

The rewrite is a carriage return and a space pad, deliberately **not**
`x/ansi`'s `EraseEntireLine`. That constant is already in `go.sum` through
bubbletea and promoting it would be one line of `go.mod`, but there is not a
single raw escape literal in non-test Go code and constraint K asks for an issue
first. A row thirty columns wide cannot have wrapped on a terminal wide enough
to be drawing a wordmark on. The guard is `term.IsTerminal(os.Stdout)` and not
`Theme.Colour`: `ui.New` is handed **stdin** for the REPL, because that is what
decides raw mode there, and a carriage return goes to **stdout**. The two differ
exactly when output is redirected, which is the case the guard exists for.

### `-host` stopped paying for the package index

The change that actually removed time. `Core.Attach` called `use`, which calls
`ev.Index()` — the walk `internal/host/host.go` describes as "the ~0.3s that
moved off the attach and onto first use". `use`'s own comment names the case it
must not happen in: *"a startup that attaches without being typed is what stops
paying."* `Attach` **was** that case and was paying. Behind `indexOnce` the walk
still happens on the first line that needs the index, so nothing is saved twice
— it just stopped happening in front of a screen that had nothing to say for the
duration. `host.Walks()` exists for this assertion and
`TestStartupAttachDoesNotWalkTheIndex` makes it, without a clock.

### The database row is configured-only, and the benchmark is why

Invariant 22 permits detection at a scratchpad open, so a *detected* database on
the screen was legal, and the intent was to pay for it and say so.
`BenchmarkDetect` said otherwise: `db.Detect` is bounded by depth and by
`maxFiles = 2000` rather than by anything that stays small, and it costs 2.0ms
over twelve directories, 13.5ms over a hundred and eight, and **66ms over twelve
hundred**. A 15ms budget breaks at about a hundred and twenty directories, which
is an ordinary service repository.

Spending 66ms in front of a prompt to name a database nobody asked about is a
trade gluon makes nowhere else. So the row names configured `[[database]]`
entries, which is free, and `detect()`'s comment — "only `:db`, `:query` and
`:conf` ask" — stays true unedited. Invariant 25 is a property of the type: a
password cannot reach a field built from a driver name and the *name* of where
the string lives. Invariant 13 is beside it — two entries are a count, because
gluon does not pick between databases.

### `Result.Out` did not move a byte

`PadOpen` is filled in beside the sentence rather than the sentence being
derived from it. Deriving was the tidier design and the riskier trade: `Out` is
read by pipes, `gluon -e`, the `-json` envelopes and every MCP tool (invariant
30), and the way to keep frozen bytes frozen is not to rewrite the code that
produces them. `TestThePadSentenceIsUnchanged` is the pin that was missing
before — nothing had ever asserted on that sentence.

### What is new

- `banner = "full" | "compact" | "off"`, one row in `Options()` and nowhere
  else. `NextRun`, because the screen is drawn before the program starts.
- `internal/repl/banner.go` — pure, because the screen is printed before
  `tea.NewProgram` and so is unreachable by both tiers in *Testing the UI*. A
  builder returning a string is what makes it testable at all.
- A development build shortens its own version on the screen and only there:
  `dev · 8053da0 · dirty`, via `module.IsPseudoVersion` from the already-required
  `golang.org/x/mod`. `gluon --version` prints the whole thing, because that one
  is asked on purpose by somebody filing a bug.

---

## Shipped since v10: `editor = "builtin"` — an editor you run from (2026-09-13)

`:edit`, `:scratch -edit` and `:buf` all hand the terminal to a program through
`tea.ExecProcess`, and while it has it gluon cannot draw a byte. So the loop was
always write, quit, read, reopen. `editor = "builtin"` is gluon's own
full-screen editor, and `Ctrl-E` runs the document and puts the answer in a pane
without closing it.

### The vertical axis is the only thing `vim.go` was missing

`2026-09-05-add-vim-input` rejected `o`, `O` and every vertical motion, and gave
the same reason three times: the prompt holds one physical line. A document has
lines, so the reason does not transfer — and everything else does. `vimState` is
embedded whole; `wordFwd`, `findChar`, `operate`, `vimPut` and `vimMotion` are
called unchanged on `lines[row]`. `vimBuf` gained no field and `vimKey` gained
no key, which is what keeps `w` stopping where `w` stops in both places.

Three things a document needs that a line does not, each found by a test:

- **`curswant`.** Vertical motion has to aim for a remembered column, not the
  one the clamp left it in. Without it a short line passed over drags the cursor
  left permanently, because the clamp that keeps it on a character is the only
  memory of where it was.
- **Undo is decided by diffing, not by listing which keys are edits** — except
  `u` itself, which has to be answered *before* the recorder sees it. Recorded,
  it pushes what it just took back onto the ring it took it from and clears the
  redo ring it had just filled: one undo, and redo is gone.
- **The doubled operators are linewise.** `vim.go`'s `doubled()` empties the
  line, which is right for a prompt holding exactly one and wrong for a document
  where `dd` takes the line out.

### Running in place is offered where running *replaces* the session

`:edit` and `:scratch -edit` go through `Core.Reload`, which replaces the
session and replays it — so running twice leaves what running once left. That
idempotence is what makes a key pressed every few seconds safe, and it is the
whole reason those two surfaces got the key and `:buf` did not. `:buf -run`
calls `SubmitBatch`, which appends; a second press would leave two copies of
everything. `ZZ` runs it once on the way out, and the footer says so rather than
offering a key that quietly duplicates the reader's work.

### The replay's output was computed and then dropped

`submitAll` collected every construct's output, handed it to `swapSession`, and
`swapSession` returned `Result{}` on success. `Reload` then answered `reloaded
(7 entries)` and nothing else — which nobody noticed for as long as the only
thing on the other side of it was a prompt. An editor that runs the session
without closing itself has nothing else to show, so `swapSession` now returns
what the build said and `Reload` joins it with its receipt.

**Reload had to be batched, and the measurement is why.** `submitAll` runs one
`Submit` per construct, so replaying an eight-entry session cost eight builds:
**4.2s against 0.64s** for the same eight through one batch. Once a run is a
keystroke rather than a session-ending act, linear rebuild cost is the
difference between a key you press and one you do not. `submitGrouped` batches
consecutive code constructs and flushes at each meta command — the shape
`runOneShot` already uses for `gluon -e`, because a meta answers against the
session as the lines before it left it. `submitAll` stays as it is: it is
`SubmitScript`'s engine, and a scripted caller wants line-by-line semantics.

### Two bugs the scratchpad surface turned up, both older than this change

- **`:scratch -edit` could never reload.** It hands over the pad's own session
  file, and `Reload` read it back with `session.Unmarshal` — which is right to
  refuse a directive it was not taught, and the pad file opens with
  `//gluon:pad 1`. Every pad that had ever been written answered `unknown
  directive` and changed nothing. `scratch.ParsePad` is now the one parser for
  those bytes, and `ReadPad` goes through it too.
- **A reload made gluon disown its own pad.** `padStandDown` compares the file's
  size and mtime against what the session last wrote, and a file gluon handed to
  an editor fails that test by construction — so the next line silently stopped
  being written down. A reload gluon asked for now re-records the baseline.

### A run in flight had no one to drain the queue behind it

`resultMsg` drains `m.queue`; `bufferRunMsg` did not. A `:q` typed while a run
was still building waited for a result that was never coming, because that run
*was* the last message the session would send. Found by an integration test
whose last two keystrokes were `ZQ` and `:q`.

### The view hands back work; it never touches `Core`

`modal_entry.go:157`'s rule, with one more field. `modal.run` is a line the user
could have typed; a document also has to reach disk before anything can read it,
so `bufAsk` carries the text and the model writes it — off the goroutine holding
the keyboard, on the one evaluations already run on. `bufferRunMsg` is its own
message for `modalEditMsg`'s reason: the ordinary path prints, and printing
under the alt screen is printing into a void. It carries `[]bufMark` rather than
text, because a gutter needs line numbers and parsing them back out of a
rendered diagnostic would tie the view's coordinates to the shape of a sentence.

### The cursor is the mode, and a drawn cursor is a cell

bubbletea v1 hides the terminal's own cursor for the life of the program and
gives a model no way to place it, so both surfaces draw their own. That makes
shape available — `bubbles/cursor` renders a reversed cell and nothing else —
but it means the shape has to fit in a cell, which decides the rest:

- **Both shapes take the cell they are in: reversed for normal, underlined for
  insert.** The first attempt drew insert's cursor as `▏` (U+258F) in a cell of
  its own, standing before the character the next keystroke pushes right, so
  that nothing of the line was ever hidden. What that cost was the line: every
  column from the cursor rightward moved one place the moment you pressed `i`,
  a space before the cursor read as two, and `esc` — which steps one character
  left, as it does in vim — read as a jump rather than a step. A cursor that
  moves the text is a worse cursor than one that borrows a cell, and an
  underline borrows the cell without hiding what is in it.
- **Neither shape survives `NO_COLOR`.** Under termenv's `Ascii` profile every
  attribute is dropped, `Reverse(true)` and `Underline(true)` alike, so on that
  terminal neither cursor is drawn — which is the terminal upstream's own block
  was already invisible on. The mode is still said there, by `[i]` and `[n]`,
  which are text. `inputView` still keeps its three structural bail-outs (a
  scroll window, an echo mode, an unfocused input) and drops the two about
  colour in insert: a *colour* turned off in the theme is not a terminal that
  cannot underline, and the two modes still have to be told apart.
- **A cursor that is a character breaks screen-scraping; one that is a style
  does not.** `TestProgramCompletion` waited for `appleCount` in the output and
  got `appleCo▏unt`, because it read the developer's own `config.toml` and that
  developer has `mode = "vim"` set. The underline leaves the text alone, so that
  particular break is gone — the test stays hermetic anyway, because the *prompt*
  still reads back as `gluon[i]>` rather than `gluon>`.
- **The ghost keeps its first character either way.** The block shows the
  character it covers and the underline never covered one, so a suggestion is
  never misread — and `hintAfter` counts the same cells in both modes, which is
  what keeps the signature from wrapping the line onto a second row.
- **`paintSpans` needed `NoTabConversion`** for the same reason `cursorOver` has
  always had it: `lipgloss.Render` turns a tab into four spaces, a terminal
  turns it into however many the next stop is, and the cursor's cell is the only
  one on the line that goes through `Render`. Without it the block cursor
  dragged every indented line four columns left as it reached the indent — which
  is every line of Go a session holds.

### `builtin` is a value of `editor`, not a key of its own

`add-buffer-view` proposed `buffer.editor`. There is already an `editor` setting
whose question is *which editor*, and `builtin` is an answer to it — so it costs
no new table, no new `config.Option`, no new completion row, and one setting
governs all three commands because all three go through `Result.Edit`.

It is read from config alone, and that is the one place it departs from
`EditorAt`'s chain. There the environment wins, because a `$VISUAL` exported for
this shell is more specific than a file. Here the question is not which program
but *whether* one — which nobody expresses by exporting an environment variable,
and which an already-exported `$EDITOR` would otherwise silently veto on every
machine where one is set.

### Still open in this area

- **`:buf` cannot run in place.** It needs a session mark to truncate back to,
  plus the import-cache snapshot `CheckBlock` and `EvalTransient` both take.
- **`EditRefusal` would remove a pad's session file, and is saved by an
  accident.** `pipe.go:141` removes `res.Edit` whenever `EditThen` is empty,
  and `:scratch -edit` hands over the pad's own file with an empty one — the
  distinction `Reload` draws by filename and that one does not. It is
  unreachable today because the only drivers that call it, the piped loop and
  `gluon -e`, open no scratchpad at all, so `:scratch -edit` answers `this
  session is not in a scratchpad` long before it names a file. **Checked, not
  assumed.** It stays worth writing down: what makes it safe is a property of a
  different subsystem, and nothing links the two.
- **No `f`/`t` in visual mode.** They are line-local and reach the line machine,
  which would apply a pending operator to a range the selection does not know
  about.

---

## Shipped since v9: `:routes` for seven frameworks (2026-08-30)

`:routes` went from three frameworks to seven — chi, gin and echo joined by
fiber, hertz, iris and gorilla/mux. No new dependency: gluon links none of them,
and each plugin activates off the session's own build list. Two things were
learned, and one earlier entry is superseded.

### The column set is fixed first, and each framework is fitted to it

The alternative — letting every plugin report whatever its API hands over — is
what produces seven tables that look alike and cannot be compared, under one
command name that then means seven things. The columns are method, path and
handler name, formatted through a single constant that both the generated child
source and gluon's own tests use, so a width cannot drift between frameworks.

A framework that cannot supply a column reports it as unavailable (`—`) rather
than blank. Blank reads as "this route has no handler", which is never true;
chi, fiber and gorilla/mux hand over the `http.Handler` value and never its
name. It is the distinction `internal/db/detect.go` draws between "found
nothing" and "did not look", applied to a cell — and the reason there is a
column header at all.

Three of the seven fill every column: gin, echo and hertz record the handler
name at registration, and iris keeps it as `MainHandlerName`.

`gorilla/mux` is the second case, after gorm's `:sql`, that justifies plugin
commands being Go rather than TOML templates. `Router.Walk` takes a callback and
returns an error, so its rewrite is a `func() string { ... }()` that accumulates
rows — which a TOML `rewrite` template cannot express. That is exactly the
boundary `internal/plugin/toml.go` draws.

This supersedes "Three plugins wanted one command name" (v7) on the count only.
The tie-break is unchanged and now tested: registry order in `Builtin()` decides,
`:plugins` names the owner and the displaced, and all 21 pairs are asserted in
both build-list orders so a reordering is a deliberate act rather than a merge
artefact. The three original plugins keep their positions and every framework
added since is appended, so no existing session's answer changes.

### A non-goal asserted from memory is not a non-goal

hertz was proposed as out of scope on the grounds that it had no public route
enumeration API. It has one: `(*route.Engine).Routes() RoutesInfo` at
`pkg/route/engine.go:1044` in v0.10.6, returning `RouteInfo{Method, Path,
Handler}` — every column this change asks for, which is more than chi, fiber or
gorilla/mux can supply. `server.Hertz` embeds `*route.Engine`, so it is reachable
from the value an application actually holds.

The claim had been written from memory rather than checked against the module,
and a non-goal justified that way is indistinguishable from one that is correct —
the whole point of recording a rejection is that nobody re-derives it. Each of
the seven APIs was compiled and run against its real module before its rewrite
was written, which is the cost of the rule that gluon does not link what it
describes: nothing else can type-check these lines.

---

## Shipped since v10: `:bench a, b` (2026-09-07)

A gap noted while building `:bench` and never closed: it measured one
expression, so comparing a brute force against an optimised version meant
running it twice and holding two numbers in your head.

Two expressions separated by a **top-level** comma are now measured in one
evaluation and reported side by side with the ratio. The splitter is
`splitTop`, the one `:diff` and `:impl` already use, so a comma inside
brackets, parentheses, braces or a literal is not the separator; a single Go
expression has no top-level comma to lose, because a tuple is the only shape
that would and `:bench` has always refused those.

`benchSource` is reused rather than reimplemented, and emits the same measured
body twice. Every trap ROADMAP records about it is a trap this change would
otherwise have walked into a second time — above all **the sink must not be an
interface**: assigning a non-pointer to one allocates and corrupts the very
allocs/op being measured. Two expressions get two sinks, one per closure,
precisely because a shared sink would have to be an interface to hold both.

One evaluation rather than two, which is `:diff`'s argument with more force: two
evaluations would build twice and replay the session twice, and a measurement is
more sensitive to that than a comparison is. The rows come back grouped,
`opts.runs()` per expression in the order given, so the split is arithmetic and
the child describes nothing extra.

A quantity whose left-hand value is zero prints `n/a` rather than an infinity —
16 B/op against 0 B/op says that one of them allocates, not a number. It is
ASCII because `benchTable` pads with `len()` and a multi-byte dash would come
out two columns short.

Constraint I holds both ends: `TestBenchSourceForOneExpressionIsUnchanged` pins
the single-expression program byte for byte, and
`TestRenderBenchSingleRunIsUnchanged` pins its two lines of output.

---

## Shipped since v10: `:since` — the release record the toolchain already ships (2026-09-05)

"What is new in the Go I have?" was a question gluon could not answer, and the
obvious implementation — carry a copy of go.dev's release notes — is the one
this repository would have rejected for the reason it rejects everything of that
shape: a second copy of a fact is a thing that can be wrong, and constraint G
says `:get` is the only command that goes online.

It turned out not to be necessary. Every distribution ships the release record
as data.

### The api files are a citation, not a scrape

`$GOROOT/api/go1.N.txt` is every standard library addition for release N, one
declaration per line, and `$GOROOT/api/README` states that from `go1.19.txt`
onward **every line must end in the issue number of the proposal that accepted
it**. Verified: across 1.19–1.27 exactly one line has no `#nnnnn`, and it is the
comment `# freebsd riscv64 port` at `go1.20.txt:444`. So the `go.dev/issue/N`
beside a declaration is read off the disk, printed and never fetched — `:doc
-url`'s posture, for `:doc -url`'s reason.

`$GOROOT/doc/godebug.md` is the other half: the behaviour changes, one paragraph
each, naming the GODEBUG setting that reverts them.

### The platform collapse is not tidying

`go1.20.txt` is 9,165 lines of which **8,864** carry a `(linux-386)`-style tag —
the same `syscall` declarations repeated once per GOOS/GOARCH. Collapsed to one
entry per declaration carrying the platform list, that release is 4,684.
Uncollapsed it reports nine thousand additions and cannot be read at all, so the
collapse is what makes the release viewable rather than a nicety on top of one.
`go1.1.txt` is 97% platform-tagged and makes the same point louder.

Reading all 157,054 lines of a 1.27 distribution costs **46.9ms** (darwin/arm64,
M1 Pro) — a tenth of one evaluation — so it is parsed once and cached whole. A
lazy per-release split would buy nothing: the listing needs a count from every
file.

### The curated half, and the rule that keeps it honest

No api file describes a change to the *language*. Generics, range-over-func,
per-iteration loop variables and generic type aliases appear in none of them, and
they are the half worth running. So eight notes are written by hand — and a
hand-written claim about a Go release is exactly the thing this repository does
not take on trust.

The rule: **a note exists only where a compiling test pins it.** Every snippet is
assembled through `session.Classify` and `render.Main` — the REPL's own path —
and built with a `go.mod` at the directive it claims; a note saying `below =
"error"` must be *rejected* one minor down, and one saying `"behaves
differently"` must still build there and **print something else**, which is run
and compared rather than asserted. `internal/release/release_integration_test.go`
is all three, and `internal/repl/since_integration_test.go` runs each snippet in
a real session, because compiling is not the same as the REPL accepting it a
construct at a time.

Absence is reported as absence. `Release.Curated` is whether anyone looked, kept
separately from `len(Lang)`, so the language column can print `—` for "nobody
wrote notes" and `none` for "someone checked and there were none" — the
distinction `:doc -examples` already draws between finding nothing and not having
searched. Go 1.25, 1.26 and 1.27 are uncurated, and say so.

### Two things the tests found that memory would not have

**Generic type aliases are gated at `go1.23`, not `go1.24`.** They are a Go 1.24
feature — 1.23 needed `GOEXPERIMENT=aliastypeparams` — but the language version
the compiler enforces is `go1.23`, so a current toolchain accepts them under
`go 1.23` and rejects them under `go 1.22`. The note is filed under the release
and carries the directive, and says why the two differ; the first draft had
`needs = "1.24"` and the test rejected it.

**gluon's own runtime does not build below `go 1.21`.** `render.RuntimeFiles()`
calls `clear` (gated at 1.21) and `unsafe.StringData` (1.20). A note about Go
1.18 must be failed by Go 1.18 rather than by gluon's floor, so the directive
check compiles the user's rendered code against a stub printer; the behaviour
check, which needs real output, uses the real runtime and skips below the floor.
The floor is a named constant with that reasoning attached, because it is a real
limit on what a session can be asked to run.

### What the first version got wrong, and the check that now catches it

It shipped with notes for 1.18 through 1.24 and nothing after, and the answer to
"what did Go 1.27 bring" was a dash meaning *nobody wrote this up*. The dash was
honest and useless: Go 1.27 added **generic methods**, which is exactly the kind
of thing somebody opens this command to find.

The fix is not more diligence. `$GOROOT/src/go/types` ships with the
distribution, and the type checker knows precisely which syntax it admits at
which language version. `internal/release/gates.go` parses that, and
`TestEveryGatedVersionIsWrittenUpOrWaived` fails the build when a release gates
something no note requires — so the next Go release is a red test rather than a
user's discovery. Three gated versions had no note when it was first run: 1.26
and 1.27, now written; 1.9, 1.13, 1.14 and 1.17 are waived in a literal somebody
had to edit, with the reason on each.

Three things that had to be got right for that parser to be worth having:

**Three helpers gate, not one.** `verifyVersionf` and `versionErrorf` name the
feature in a format string; `allowVersion` is a silent permission check whose
caller words the error itself. Collecting only the named ones gives a version set
that is short.

**`allowVersion` is sometimes a parameter, not a method.** Inside
`rangeKeyVal` the checker is passed in as `allowVersion func(goVersion) bool` and
called bare, so a match on `*ast.SelectorExpr` alone misses `go1_22` and
`go1_23` — which is to say it misses **range-over-int and range-over-func**, the
two gates a release explorer most needs to see. A first draft did exactly that
and reported ten gated versions instead of eleven.

**So the gates are a check, not a display.** A list that confidently omits
range-over-func is worse than no list, and it cannot be made complete: Go 1.27's
generalised inference is a real language change that no gate mentions at all,
because relaxing what the checker will infer needs no version test. The gates
are exact about *which versions* gate something — all three helpers name a
version — and only a subset about *what*. They are read for the completeness
test, and shown only as a floor for a release nobody has written up, labelled as
what they are.

The reverse check earns its place too.
`TestAGatedNoteRequiresAVersionTheCheckerActuallyGates` is what would have caught
the generic-type-alias mistake without a compiler round-trip: a note may not
require a directive the checker gates nothing at.

### A language change is not always gated by a directive

Writing 1.26 and 1.27 up turned a two-way distinction into a three-way one.
`Note.Below` was `"error"` or `"behaves differently"`; both assume the go
directive decides. Two of the five new notes do not fit:

- **Go 1.26's self-referential constraints** — `type Adder[A Adder[A]]` — compile
  under `go 1.18` on a 1.27 toolchain. The old restriction was an implementation
  limit, and lifting it gates nothing.
- **Go 1.27's generalised inference** — `BinOp(Pick)` without the explicit type
  argument — the same.

So `"toolchain only"` is the third value, and it is not a label: `-run` measures
those against `Evaluator.Toolchain()` rather than `Evaluator.Lang()`, because
telling somebody to raise a `go` directive that was never in the way is worse
than saying nothing. In a session attached to a `go 1.21` module, `generic
methods` is refused and `inference-on-conversion` runs — which is correct, and is
the whole content of the distinction.

`TestAToolchainOnlyNoteIsNotGatedByAnyDirective` builds those snippets at
`go 1.21`, far below the release rather than one minor below, because the claim
is that *no* directive gates them and one step down could pass by accident. What
cannot be checked on one machine is which toolchain first accepted them — there
is only ever one installed — and the notes say where that attribution comes from
instead of implying it was measured.

An empty notes file now means something too: somebody checked and the language
did not change. Go 1.19 and Go 1.25 have one each, and
`TestAnEmptyNoteFileMeansTheCheckerGatesNothingThere` holds the claim against
the checker.

### `-run`, and the two numbers that can be short

A note's row offers **run**, and the choice hands back `:since 1.23 -run
range-over-func` rather than the snippet. Invariant 19's shape — one path decides
what running a note means — and also a mechanical necessity: `ModalChoice.Run` is
submitted a line at a time and a snippet is many, so `submitAll` is the engine
and the command is the handle.

The directive in force is the *host's* when one is attached, not the toolchain's,
so a 1.27 toolchain still rejects range-over-func in a session on a `go 1.21`
module. `Evaluator.Lang()` names that number, and the refusal says which of the
two is short and how to move it — `host.buildable()`'s argument, applied to a
feature instead of a module: `GOTOOLCHAIN=local` turns the alternative into a
compiler error naming a temp path the reader never wrote.

`:since` is eval-tier over MCP for that flag alone. Invariant 28 puts the tier on
the command rather than the argument, and splitting it into a browsing half and a
running half to recover the static tier would be two commands where one flag
does.

---

## Shipped since v10: `:scratch` — a session with a name and a tomorrow (2026-09-05)

`Session` was `struct { Entries []Entry }` and nothing more. It had never
reached the disk, so every `gluon` started empty and the work you were in the
middle of yesterday was gone. A scratchpad is that session given a name and a
home: one directory under `scratch.Root()` holding `session.gluon` — the entries
as typed, with the pins — and gluon lands on `default`.

### The objection this had to answer

`internal/repl/bookmark.go` refused persistence in writing, for a *bookmark*, on
the grounds that it "would make the session durable for one command and not the
others, which means a location, an eviction policy and a second format competing
with the one `:save` already writes."

Read literally that is an argument for this change and against the half-measure.
A scratchpad makes the session durable for **every** command — `:pin`, `:drop`,
`:undo`, `:get`, `:use` all land in the same file — which is the inconsistency
the comment was right to refuse. The three named costs each have an answer:

- **The location** is the tree `internal/scratch` already owns.
- **There is no eviction policy** because a pad exists only when somebody typed
  its name, and `:scratch -rm` ships in the same change, so the tree is not one
  you can only add to. Snapshots stay per-process for the other half of this:
  `:branch` auto-names, and durable auto-named snapshots are exactly the thing
  that *would* need one.
- **The sidecar is not a program**, so it competes with nothing. It is `:edit`'s
  buffer given a name and a home, and the directory becomes a Go module only
  when `:save` runs there — which is why the file carries an extension the
  toolchain ignores.

### What was measured

**Reopening costs one build, not one per entry.** The replay installs the
entries wholesale and calls `ev.Eval` once, through `swapSession` — `:restore`'s
path, for `:restore`'s reason: it is the one place that knows how to put the
previous entries back when the new ones will not run.
`TestReopeningAPadCostsOneBuildNotOnePerEntry` counts builds through the
evaluator's own phase timing rather than by watching the clock.

**Persisting costs nothing on a line that changed nothing.** The dedupe key is
the rendered bytes, not `Core.gen`: `gen` is bumped at the top of `Submit` for
every submission, `:help` included, because it exists to invalidate completion
caches and deliberately over-counts. Rendering is string concatenation over the
entries, it happens in the evaluation goroutine's defer — never on the keystroke
path — and `TestNothingIsWrittenUntilTheEntriesChange` holds it.

### Two things that are properties rather than promises

`:get` stays the only command that reaches the network because `Evaluator.Restore`
has no code path that could: `Get` is the one method that appends the user's real
`GOPROXY`, and `Restore` writes requirements into `go.mod` with `modfile` and
unions the recorded sums, so the next build runs under `GOPROXY=off` like every
other build. A module the cache no longer holds fails with the toolchain's own
words, and the failure is answered with the `:get` line that would bring it back.

And the padless surfaces are padless because they never install a pad — invariant
34, and `cmd/gluon/mcp.go`'s claim is stronger for it than it was before gluon
had a durable session at all.

---

## Shipped since v10: `:grpc` (2026-09-04)

A gRPC server's method set, and one unary call against it, from the prompt. The
plugin activates off `google.golang.org/grpc` in the session's own build list,
so gluon links neither it nor a protobuf runtime — `go.mod` is unchanged by the
whole change.

### A plugin command that dials needed a third way to be answered

`:http` is a builtin rather than a plugin command, and its own comment says why:
`Rewrite(arg string)` sees its argument and nothing else, so it cannot look up
an environment variable, cannot name the values that must not appear in the
child's output, and cannot reach `EvalLive` to keep the answer out of a cache
keyed on program text alone. But `:grpc` has to be a plugin — it must be absent
from a session with no gRPC, which is a property only activation gives.

So `plugin.Command` gained `Live *LiveCall` beside `Query *DBQuery`, and for the
same reason that one exists: the plugin *declares* the program, the imports it
needs and the variables it reads by name, and gluon owns the resolution. There
is still exactly one implementation of "the child gets the secret and nothing
else does" rather than one per plugin that wants a socket, and a plugin still
cannot do anything a typed line could not.

### Still open in this area — needs an issue first

`:proto <file>` was specified as conditional and is **not shipped**. Reading a
`.proto` off disk needs a compiler, and there is no `.proto` parser in
`google.golang.org/protobuf`: `protodesc.NewFile` and `protodesc.NewFiles` take
descriptors that protoc has already produced, `protoregistry.GlobalFiles` holds
only files whose generated Go is linked into the running binary, and
`descriptorpb` decodes a descriptor set and nothing else.

What it would require is **`github.com/bufbuild/protocompile`** (or the older
`github.com/jhump/protoreflect/desc/protoparse`) — a new dependency, and
therefore an issue before a commit. Shelling to `protoc` was the alternative and
is worse: a dependency on the user's machine that fails confusingly when absent,
and a code generator, which the non-goals already exclude. The finding is
recorded in full in `internal/plugins/rpc`'s package comment, where somebody
proposing the command will find it.

---

## Status: v10 — syntax highlighting, and themes as files

gluon paints Go, SQL, JSON and TOML everywhere it shows them, live as you type
and in scrollback, and the palette that does it is a file rather than a map in
`internal/ui`. The default is `go`, the colours the documentation site's
stylesheet uses for code, with the four roles a page of Go needed that a value
table never did.

### The default stopped being ANSI 0-15, and why that reverses v5

v5's `internal/ui` argued that the sixteen ANSI slots are the user's own theme,
so a truecolor default would override a palette someone chose. That was right
about the cost and wrong about the benefit.

It was wrong because sixteen slots cannot carry the distinctions source needs.
A keyword, a builtin call and a type are three different things; in ANSI they
became magenta, blue and cyan whose actual appearance gluon could not read, and
a screen of Go blurred. What gluon looked like was not "the terminal's" so much
as unspecified.

It was right that a chosen palette should not be overridden without a way back.
So `[theme] name = "terminal"` restores the old mapping exactly — the ten roles
that existed then keep the numbers they had, and `TestTerminalThemeIsTodaysPalette`
pins them — and gluon never selects it for you.

**It never selects it for you even on a 16-colour terminal, deliberately.**
`TERM=xterm` resolves to termenv's ANSI profile, and the nearest-slot search
runs against termenv's own reference table rather than the user's actual
palette — so auto-switching would make gluon's appearance depend on `$TERM` in
a way the user cannot see, cannot predict and cannot put in a bug report, while
`gluon doctor` still said `theme go`. `TERM=xterm` is also a very common default
on terminals that render truecolor perfectly well. Degradation is legible; a
hidden switch is not.

### Highlighting is byte-preserving, and that is structural

`internal/syntax` reports byte offsets and `Paint` is the only thing that
writes. It emits `src[a:b]` slices with `a` non-decreasing and gapless,
interleaved with fixed escape strings. Nothing formats, pads, wraps or
re-encodes. So a tokenizer that gets a class wrong produces a wrong colour, and
one that gets an extent wrong steals colour from its neighbour — neither can
produce a wrong byte. That is what lets the SQL, JSON and TOML scanners be
plausible rather than perfect, and it is why the rejected-YAML-scanner entry
below does not apply to them: that one was a *detector*, asked a question, whose
wrong answer was indistinguishable from a right one.

Three facts this depends on, none of them guessable:

- **`lipgloss.Style.Render` is not byte-preserving.** `tabWidthDefault = 4` and
  `maybeConvertTabs` runs on every path including the zero-props early return,
  so `Render("\tx")` is `"    x"`. It also rewrites `\r\n` to `\n` and pads
  every line of a multi-line string out to the widest. gofmt indents with tabs,
  so a highlighter that called Render would silently reformat `:src`. This is
  why a `syntax.Palette` is escape strings, derived once in `internal/ui` by
  rendering a sentinel byte and cutting there, and why `internal/syntax` does
  not link lipgloss at all.
- **`len(lit)` is not a token's length.** On invalid UTF-8 `go/scanner` consumes
  one byte and reports `utf8.RuneError`, whose literal is three; `scanComment`
  and `scanRawString` both call `stripCR`, so a comment holding `\r` is longer
  than its literal; and `tok.String()` for `ILLEGAL` is the seven-byte word
  "ILLEGAL". Extent is measured from the source, case by case, with a default
  that paints nothing.
- **An inserted semicolon can be positioned backwards.** When a newline falls
  inside a `/*...*/` comment, `Scan` synthesizes the SEMICOLON after the COMMENT
  token but at the newline *inside* it — behind the comment already emitted.

### The input line is gluon's own View, not a fork of textinput

`bubbles/textinput` paints the whole value with one `TextStyle` and offers no
seam: `TextStyle` is a struct rather than an interface, and `SetValue` is out of
the question because `Value()` is what gets submitted. What makes replacing
`View` cheap rather than a fork is that **gluon never sets `Width`** — so
`handleOverflow` always yields offset 0 and offsetRight len(value), and there is
no horizontal scroll window to reproduce. Everything else it needs is exported.

Anything the widget was not written for — a Width, an echo mode, an unfocused
input, a plain theme — falls through to upstream's own `View`. That bounds the
"our imitation drifted from theirs" risk to "colour disappears".

`textinput.SetValue` runs bubbles' runeutil sanitizer, so a typed line can never
contain a tab; the echo path is where tabs actually arrive, because a paste does
not go through `SetValue`.

### Two bugs found on the way

- **`Core.styles()` returned a coloured palette on the non-rich path.** `Rich`
  is set in exactly one place, `newModel`, so `gluon -e` from a terminal took
  that branch — and `gluon -e ':env PATH' -json` put `\u001b[1;36m` inside the
  JSON `text` field. A frozen envelope, invariant 21. It stayed invisible only
  because lipgloss's global renderer degrades on a non-TTY, which is exactly the
  implicit global `internal/ui`'s own doc says gluon does not rely on.
- **Bold was applied outside the colour gate.** gluon honours a set-but-empty
  `NO_COLOR` and termenv does not, so `NO_COLOR= gluon doctor` on a terminal
  emitted `ESC[1m`. `pretty.DefaultStyles` is now `PlainStyles` and paints
  nothing at all.

### The preview is the theme, installed

`:theme` opens a picker, and moving the selection does not draw a swatch beside
a list — it calls `applyTheme`, so the prompt, the highlighting, the value
tables, the picker's own list and its footer all become the palette being
considered. A swatch answers "what are these fifteen values"; only the session
answers "do I want to read Go in this", which is the question actually being
asked. Esc puts back the theme the picker opened in, so looking costs nothing.

It also made the seam obvious. Every style in `internal/repl` is package-level
and was set once at startup, which was the right shape while a palette could not
change — but four things hold copies: the input's prompt, its completion style,
the spinner, and `Core.Render`/`Core.Styles`. Invariant 32 is that list, and the
picker is what enforces it: a surface left out is visible the moment the
selection moves.

Two smaller things fell out of it. Keeping the choice means editing
`config.toml`, which is the file holding everything else the user configured —
so `SetThemeName` moves exactly one line, proves byte-wise that nothing else
changed and parse-wise that the result still selects the theme, and only then
renames over the original. And the echoed continuation prompt had been painted
in `prompt` while the live one used `dim`: two colours for one `...>`, visible
only once a palette made them differ.

`gluon theme` keeps `import`, which converts a file and has no business at a
prompt. The overlap between the two commands is deliberate: one is the form for
a shell, the other for a session you are in the middle of.

### The importer, and what it will not guess

`gluon theme import` reads `.tmTheme` (plist XML, via `encoding/xml` — no new
dependency, and `encoding/xml` never fetches the DOCTYPE's apple.com DTD) and
VS Code theme JSON. Real VS Code themes are JSONC, so a comment and
trailing-comma stripper is required rather than optional.

Two roles are deliberately never mapped. `search` has no counterpart — no scope
in any editor theme means "the reverse-i-search prompt". And `note` looks like
it maps to `invalid.deprecated`, but TextMate inheritance means a theme with
only an `invalid` rule would hand note the *error* colour: amber becomes red,
on the quiet annotations beside a value. `keyword.operator` is left out of
`punctuation` for the same class of reason — inheritance would let a generic
`keyword` rule paint every `:=` and `{` in keyword colour, which is loud in a
way the theme's author never chose.

### The third format guesses least, because it is not a highlighter

IntelliJ colour schemes read too, and they are the format that answers what
TextMate cannot. A `.tmTheme` describes a language; an IntelliJ scheme describes
a whole editor, so it has a caret, two line-number colours and a console with
its own error and warning foregrounds. `prompt` takes the caret — the theme's
own answer to "where you are typing" — and `dim` and `border` split the line
numbers: the bright one, on the caret's row, is furniture you read, and the
ordinary one is a rule you look past. Ten roles from `<attributes>`, three from
`<colors>`, `annotation` from gluon's own relationship to `comment`, and only
`search` left as a guess. `DEFAULT_OPERATION_SIGN` is refused for `punctuation`
exactly as `keyword.operator` is, and for the same reason.

The format is flat where TextMate is hierarchical, so there is no specificity to
arbitrate — only `baseAttributes`, an alias, followed with a bound because a
cycle in a file gluon did not write must not become a loop in gluon. Both XML
dialects are told apart by their root element rather than by a substring: a
`.tmTheme` is full of the word "scheme" inside scope names.

The five dark [Gruvppuccin](https://github.com/sandboxws/gruvppuccin) palettes
ship as built-ins, converted by that path rather than transcribed — the
conversion is code with a test, so it can be re-run when the source themes move.
Macchiato is not among them, and finding that out is the argument for converting
rather than copying: it differs from Mocha only in its editor background, gluon
takes no background because the terminal owns it, and the two therefore convert
to the same fifteen values. `TestBuiltinsAreDistinct` is now what says so — the
rule `File.Name` already stated about two names for one theme, enforced across
the shipped set.

### The canon, and what converting fifteen of them found

gluon now ships twenty-three themes: the seven above, `go-light`, and fifteen
converted from the editor themes people arrive already using — Dracula, Nord,
Monokai, Tokyo Night, Solarized, Gruvbox, Catppuccin, One and GitHub, in both
halves where the family has two. Six are for a light terminal, which gluon had
never shipped one of.

The theme files were the small half of the work. Converting fifteen real files
at once is the first time the importer had been asked anything it had not been
written against, and it could not read three of them at all:

- VS Code's own Monokai opens with six lines of `//` naming the greys it was
  built from. `detect` looked at the first non-space byte, found a slash, and
  refused the file — while `stripJSONC`, twenty lines away, existed for exactly
  this. Format detection now skips leading comments.
- Tokyo Night Light writes `"foreground": "#0f4b6e" //"#33635c"` before a
  closing brace. Whether a comma is trailing cannot be decided while the
  comments are still in the way, so `stripJSONC` is two passes now: the single
  pass looked ahead through the original bytes, kept the comma, stripped the
  comment, and handed `encoding/json` a trailing comma it had itself created.
- GitHub Dark Default writes one UI colour, `symbolIcon.constantForeground`, as
  an array. `map[string]string` refused the whole theme over a value gluon does
  not read. `colors` is decoded loosely now, the way `normaliseHex` already
  treats an alpha channel: one bad entry costs that entry.

The other twelve read, and converted badly, in four ways that are the same
mistake — taking a colour the source theme meant narrowly and applying it
everywhere:

1. **`ident` took the `variable` accent.** `toFile`'s own comment already said
   the right thing — "the editor's own foreground is what unscoped text is,
   which is what an identifier is in every language gluon paints" — and the
   scope lookup ran first and won. gluon's `ident` is every identifier a line of
   Go has, not the locals a `variable` rule means, so Dracula gave every one of
   them its pink, One its red, Catppuccin its maroon.
2. **`error` took a highlight's ink.** `invalid` is written the same way in
   theme after theme: ink on a red field. gluon takes no field, so the red that
   made it an error is precisely the half dropped and the half kept is the
   colour of ordinary output — Dracula `#F8F8F0`, Nord `#D8DEE9`, gruvbox
   `#fbf1c7`. A rule that carries its own background is no longer asked.
3. **A language's rule answered a question about every language.** A descendant
   selector is keyed on its last element, which is the scope being styled — but
   One Light's `source.elixir readwrite.module punctuation` is what punctuation
   looks like *in an Elixir module*, and gluon has one punctuation colour. The
   lookup is three passes now: context-free rules, then contextual ones, then
   anything beneath the most general candidate — that last one because gruvbox
   scopes `string.quoted.single` and never `string`, and a matcher that only
   walks up gave its strings the colour of ordinary text.
4. **A role resolved to the ground.** Tokyo Night's `panel.border` is `#101014`
   against a `#1a1b26` editor: a real rule between two lit panels in a window
   with its own chrome, and a line drawn in the dark on a terminal that has
   none.

Each fix is a test named for the theme that found it, because a converter's
bugs are only ever found by converting something and the something should be
written down.

### `appearance`, and the light half

A palette is foreground colours. That is why gluon can be a theme file at all —
the terminal owns the background — and it is also why a theme drawn for paper
is, on black, fifteen pale smears. gluon cannot find out which terminal you
have, so a theme says: `appearance = "dark" | "light" | "either"`.

`either` is `terminal`'s answer and only its answer, and it is a positive one
rather than "unset": ANSI slots resolve to whatever your terminal already chose,
so that one palette genuinely follows you onto either ground. The key is
optional in a theme you wrote and required of one gluon ships —
`TestBuiltinsSayWhichGroundTheyAreFor` — because a theme you wrote has nobody
to tell and one offered to a stranger through a list does.

Two things fell out of having it. The first is that the conversion reads the
ground from the source's *background* and only then from its label, which is
the opposite of the obvious order: Tokyo Night Light ships `"type": "dark"` over
an `#e6e7ed` page. A label can be wrong because nothing reads it; the background
is the ground the colours were actually chosen against, which is what the field
means.

The second is `go-light`. Five of gluon's fifteen roles describe a REPL rather
than a highlighter, so no editor theme can answer them and every conversion
falls back to gluon's own — which were chosen against black. Every light theme
imported before this had `search = #F5B843` on white, which is a smear rather
than a colour. `fill` picks its base by appearance, and gluon has an answer to
"what does this look like on a white terminal" written once in a file people can
read rather than six times across six imports.

`Readable` is the last piece and is used twice on purpose: the importer drops a
role that fails it and keeps gluon's own, so a converted theme cannot ship a
role that vanishes, and `TestBuiltinRolesReadOnTheirOwnGround` applies the same
bounds to the files themselves, which are hand-finished after conversion and
could otherwise put back exactly what the importer refused. It is a floor under
"can this be seen at all" and not a contrast standard — gluon does not know the
real background — so the bounds are grouped by what a role is *for*: a border is
drawn to be looked past, a comment recedes, and everything carrying meaning has
to read.

### `$PAGER` was the wrong shape for the item v2 left open

`:doc` now pages, closing "`:doc` does not page through `$PAGER`", which v2's
open list carried through three releases. It pages through the in-process modal
`:src` and `:guide` already use. The external pager was the part of the item
that was wrong, and it was the part written into it.

Spawning an interactive program under the alt screen is the situation invariant
19 was written about, and `:edit` already shows what it costs: `tea.ExecProcess`,
plus an outright refusal under the pipe driver, because a driver with no
terminal has none to hand over. The modal guarantees what an external pager
cannot be made to — nothing printed while it is open, exactly one line when it
closes, and `Result.Out` carrying the same text linearly, so a pipe and `gluon
-e` lose nothing. `pageable` was written for that, `:src` and `:guide` have used
it through two releases, and `:doc` was the one command that did not.

The lesson is the one the hertz entry above records in the other direction: an
open item that names a mechanism has already decided something, and the decision
ages worse than the need. What the item was actually about — "long documentation
scrolls away" — was answered by machinery that landed for a different reason.

The other two `:doc` flags follow from the same constraint that kept the pager
out. `:doc` is `Static: true`, which is what makes `go_doc` an MCP tool exposed
without `--eval` (invariant 28), so every form has to stay a file read or a
string build: `-examples` parses `example_test.go` files that are already on
disk, and `-url` builds a pkg.go.dev address from the build list and prints it.
Neither goes online, and `-url` does not open a browser — a side effect on the
user's desktop, triggered from a line that is also in the history file.

---

## Status: v9 shipped (2026-08-28)

The launch version: gluon compiles everywhere it claims to run, a pasted
batch costs one build, the inspectors work from `-e` and over MCP, and the
repo carries a LICENSE, CI, and release engineering. Four things were
learned.

### gluon did not compile on linux/arm64, and no test could have said so

`internal/gluonrt/print.go` called `syscall.Dup2` with no build tags. Dup2
does not exist on linux/arm64 — that syscall table was assembled after dup2
was superseded, so only dup3 is there — and not on Windows either. **Verified
by cross-compile**: `GOOS=linux GOARCH=arm64 go build ./...` failed at
print.go:155 and :163 while linux/amd64 built clean. Because print.go is both
compiled into gluon and embedded into every generated program, the break
reached gluon *and* its children: `go install` in a Docker container on Apple
Silicon — the most likely first contact for half a launch audience — died
mid-compile.

The gate now lives in two per-platform files, `fd_dup2.go` (darwin and the
BSDs) and `fd_dup3.go` (linux, where `Dup3(old, new, 0)` is dup2 except for
refusing `oldfd == newfd`, a case the gate never produces). `embed.go` embeds
both — build tags gate compilation, not embedding — and `FDSource(goos)`
picks by GOOS, which is sound because the child always compiles on the
machine gluon runs on. `render.Runtime()` became `RuntimeFiles()
map[string]string`, the checker parses the set (sorted, for stable fset
positions), and the temp module, `:save` and scratches all write both files.
Windows is explicitly unsupported at launch rather than silently stubbed: a
muting gate that quietly leaks replayed output on an untested platform is
worse for trust than an honest install line. CI's cross-compile job pins all
four supported targets forever, and `ubuntu-24.04-arm` runs the integration
tier so this class of break fails as a *runtime* job, not a compile loop.

### The unmute boundary was always a parameter; it just had one caller

Batching turned out to be one generalisation plus one split, not a feature.
`render.MainAs` unmuted "before the newest entry"; `MainFrom(s, imps, sink,
firstNew)` unmutes before the first entry ≥ firstNew that contributes code,
and MainAs is the `firstNew == len-1` special case — **byte-identical program
text for that case**, which the result cache (keyed on program text) and the
Plain corpus tests rest on. The boundary threads through `evalOpts.batchFrom`
as a *pointer*, because 0 is a real index (a batch into an empty session) and
a forgotten zero value must mean "newest", never "replay everything". The
constant fast path refuses batches outright: a constant answer for the newest
entry alone would swallow the earlier entries' output.

The split: one payload per printed entry was already the child's behaviour,
so `pretty.Parse` grew a grouped form. Flattening erases the line between
"two entries" and "one entry returning two values" — a tuple renders on one
comma-joined line, two entries render on two — so `ParseGroups` returns
`[][]Value` and `Parse` stays the flattening wrapper every single-payload
caller already meant. Measured: ten pasted statements went from ten
evaluations (~3.4s) to one (~330ms). The TUI queue holds `[][]string` now —
a typed line is a one-element batch — and a meta command inside a paste
flushes the run collected so far, because it answers against the session as
the lines before it left it. On a batch that fails to build, the whole batch
rolls back and lands one construct at a time, so the failure surfaces on the
entry that owns it and the others still run — a paste is not a transaction.

### `-e` was a second, worse driver, and deleting it fixed a wart

`runOneShot` classified lines itself, which is why `gluon -e ':t x'` was
rejected while `echo ':t x' | gluon` worked — the piped driver went through
`Core.Submit` and one-shot never did. It now builds a `repl.Core` like every
other driver: metas dispatch through the registry, code accumulates
multi-line constructs (`session.IsIncomplete`, same as everywhere) and lands
as one batch per run between metas. The README's own invocation is pinned
byte-for-byte by `cmd/gluon/oneshot_integration_test.go`, and the new
`metaJSON` envelope (`ok`, `command`, `text`) is a **new** envelope beside
the frozen ones — invariant 21 is a no-new-bytes-in-old-envelopes rule, and
new envelopes start their own add-only life.

### The command registry was already a tool manifest

`gluon mcp` serves the inspectors over stdio via the official
`github.com/modelcontextprotocol/go-sdk` (v1.7.0; one require, four indirect,
stdio only — the jwt dependency is its HTTP transport's, unlinked here). The
v8-era open questions, answered:

- **Does eval belong in the tool set? Opt-in, default off.** Tools are
  generated from the registry with one new field pair — `MCP` names the tool,
  `Static` marks the tier. Static tools (`go_type`, `go_methods`, `go_scope`,
  `go_layout`, `go_doc`, `session_source`) never build or run and are always
  exposed. Everything that evaluates — `go_eval`, `go_bench`, every plugin
  command (they run through `EvalTransient`, so `adapt` marks them eval-tier
  unconditionally) — appears only under `--eval`. Editing the client config
  is the asking, the same way `:get` is for the network. `go_eval`'s
  description states the replay semantics in the agent's face.
- **What is session state across calls? Per-process**, like every other
  surface. Server lifetime = client connection; `session_reset` starts over;
  a mutex serializes tool calls because `Core` is not concurrency-safe and
  the REPL's one-Submit-at-a-time promise holds here too.

Auto-attach diverges from the REPL's opt-in `-host` deliberately: an MCP
server is started *by* a project's config, so the working directory's module
is attached (`-host off` refuses), and the initialize instructions say which
module answered. One trap for the record: a client hanging up surfaces as
`server is closing: EOF` from `Run`, which is how every stdio session ends —
treat it as clean exit, not failure. `TestMCPTierIsIntentional` pins the
tier of every exposed command so joining the static tier is a deliberate act
in one reviewed place.

### Also in v9

- **LICENSE (MIT)**, chosen over Apache-2.0 for ecosystem familiarity — gore
  and the entire charmbracelet stack are MIT.
- **Version resolution**: ldflags stamp → `debug.ReadBuildInfo` (so
  `go install @latest` reports the tag) → `dev`. The hardcoded const was a
  lie waiting for its second release.
- **CI** (`.github/workflows/ci.yml`): ubuntu-latest, **ubuntu-24.04-arm**,
  macos-15; gofmt guard; race on linux/amd64; a module-cache warm step for
  the db fixture's `modernc.org/sqlite` (a *fixture* dep `go mod download`
  never fetches); and a **silent-skip guard** that greps the integration log
  for the skip messages, because the db tests skip-not-fail on a cold cache
  and a green CI that tested nothing is exactly the failure the tier exists
  to prevent.
- **goreleaser** (darwin/linux × amd64/arm64, CGO off, `-X main.version`
  only — no `-w -s`, symbols keep user panic reports readable and stripping
  measured at zero benefit), release workflow on `v*` tags, brew tap wired
  to `sandboxws/homebrew-tap`.
- **README restructured for a first-time visitor**: install and an honest
  comparison up top — naming **gore** for the first time (the closest
  relative; the differences are the fast path, fd-level replay muting,
  `internal/` attachment, the db layer, plugins) — the stale open-items list
  fixed, a trust paragraph pointing at
  the new SECURITY.md. The site gained the database section v8 never got,
  the MCP section, and the same stale-list fixes.
- **Community scaffolding**: CONTRIBUTING.md (the invariants are normative;
  "Permanently rejected" is explicitly the will-not-merge list), SECURITY.md
  (the trust model in four paragraphs), issue templates that require `gluon
  doctor` output, CODE_OF_CONDUCT.md.
- **VHS tapes** under `tapes/` for the hero, host-attach and db demos;
  rendering is editorial, not CI'd.

### Still open in this area

- The hero GIF needs `brew install vhs` and one render; the README embed is
  commented until then.
- The gore comparison ships without side-by-side numbers; measuring gore side
  by side in the Speed table's style is the upgrade.
- `gluon mcp` has no completion/paging niceties: a `go_doc` answer longer
  than a client's render window is the client's problem today.

---

## Status: v8 shipped (2026-08-28)

The database a project is standing next to: detected by shape, reported with its
provenance, and queryable through the host's own driver.

Four things were learned, and each generalises.

### The result cache is right for `:undo` and a lie for a query

`Eval` reads `e.cache.get(src)` keyed on **program text alone**, and
`internal/eval/cache.go` is already candid that "a cached program's side effects
do not fire again". For `:undo` that is arguably the better behaviour. For
`:query SELECT count(*)` it reports the row count from whenever you first asked,
as though it were current — the quiet wrongness this project rejected an
interpreter to avoid, arriving through a cache rather than an evaluator.

The fix is an explicit uncached path (`EvalLive`), not a nonce in the source.
A nonce would work and would cost a full recompile plus ~105ms of Gatekeeper on
every query, where re-execing the same binary is measured at 0.00s — and it
would fill a capped LRU with keys that can never hit again, evicting the `:undo`
history the cache exists for. **Not writing matters as much as not reading:** an
entry put there would be served to a later ordinary `Eval` of the same text.

### A secret should be impossible to print, not merely unprinted

`dsn.DSN` keeps its password unexported and defines `String()` as `Redacted()`.
That makes the careless paths — `%v` in an `fmt.Errorf`, a struct in a `-json`
envelope, a `t.Logf` while debugging — safe by construction rather than by
anyone remembering. `ConnectString()` is the one way through, and it is named so
a reviewer sees it.

The same shape applies to the wire: the generated program reads
`os.Getenv("GLUON_DB_DSN")`, so there is no secret in `<tmp>/main.go`, in `:src`,
in what `:save` writes, or in the build cache keyed to that text — and a build
error, which quotes source, cannot carry one either. That leaves exactly one
surface, the child's own output, which `mask` covers.

### Shape, not name, applied to a value

Invariant 12 was about directories. The database version is a value:
`DATABASE_URL=https://api.example.com` is a URL, and `FOO_BAR=postgres://…` is a
database. `dsn.Parse` is therefore never *given* the key's name — it cannot be
influenced by it rather than merely choosing not to be.

The one exception is deliberate and announced. `DATABASE_URL` beside
`TEST_DATABASE_URL` is the ordinary case, and refusing every time would make the
feature feel broken rather than careful, so exactly one documented list demotes
a secondary name and **says so when it fires** — invariant 13's one kind of
exception. Two genuinely different databases are
still refused with both listed.

Corroboration had to be distinguished from ambiguity for that to work at all.
`dsn.Target()` excludes credentials, so a compose file naming the superuser and
a `.env` naming the app user merge into one answer with two provenances.

### An integration test caught a write that was silently discarded

`:query -w` opened a transaction and deferred a `Rollback` — the ReadOnly flag
tracked `-w`, the rollback did not. `CREATE TABLE` reported success and the
table did not exist. Nothing in the unit tests could see it: the generated
source was correct Go, it parsed, and it ran.

A write now runs unwrapped. The transaction exists only for the read-only
guarantee, which is precisely what `-w` opted out of, and a transaction the user
did not ask for is behaviour that would then need explaining.

### Two smaller ones, both from running it

- **A raw `\x1f` in generated source compiles.** Go allows a control byte inside
  an interpreted string literal, so the wire separator being emitted raw passed
  every parse test and would only have shown up as invisible characters in
  `:src`. It is written as an escape now, with a test that scans for control
  bytes.
- **`mergeImports` deduped blank imports by name.** `importName` reports `"_"`
  for one, so a second driver import would have been dropped — and the symptom
  is `sql: unknown driver`, which blames the driver rather than the import that
  went missing.

### Still open in this area — closed

All three were closed by `polish-db-layer`.

- `:query` has no `-json` form. ~~The envelope would need to say what a NULL is,
  and the existing `-json` envelopes are a compatibility surface (invariant
  21).~~ Answered with a *new* envelope beside the frozen ones rather than a
  field grown on `metaJSON`: a NULL cell is JSON `null` and a cell holding the
  four letters is `"NULL"`. The column's declared type rides a new tagged line
  the old parser already ignored.
- Detection re-reads the project on the first `:db` after every `:use`. ~~That
  is the right place for the cost, but a project with a large `config/` tree
  pays it more than once per session.~~ The cache is keyed on the root *and* a
  stamp of what was read, so a build-list change no longer drops it while an
  edited `.env` still does. Invariant 22 is unmoved: detection still runs only
  when `:db`, `:query` or `:conf` asks.
- `gopkg.in/yaml.v3`'s upstream repository is archived. ~~The maintained line is
  `go.yaml.in/yaml/v3`. The YAML surface is confined to `scan_compose.go` and
  `resolve.go` so that migration stays two files.~~ It stayed two files. The
  old path is out of `go.mod` and out of the binary, and a test says so —
  the two modules are the same source, so nothing else could catch a relapse.

---

## Status: v7 shipped (2026-08-28)

Breadth: seven more plugins across three packs, on the model v6 built.

| pack | plugins |
|---|---|
| ids and numbers | `uuid`, `decimal` |
| database | `sql` (stdlib), `gorm` |
| web and CLI | `chi`, `gin`, `echo`, `cobra` |

Three things were learned adding them, and each generalises.

### `uuid` is the case a renderer is exactly right for

`uuid.UUID` is `[16]byte`, which the encoder sends as a list of sixteen byte
scalars. Everything a UUID *means* — the canonical form, the version, the
variant — is an encoding of bytes the child already sent. Nothing needs to run,
and there is no second implementation to drift.

Compare `decimal.Decimal`: a `*big.Int` and an exponent, both unexported, where
the big.Int is itself a sign and a slice of machine words. That gets a command.
**The test is whether the meaning is in the bytes or behind a method.**

### The gorm rewrite is why plugin commands are Go, not just templates

gorm builds SQL only in DryRun mode, and DryRun is a property of the session the
statement is built on — so it has to be injected at the *start* of a chain.
`{{.Arg}}.Session(...)` would be too late. `:sql` parses the argument with
go/parser, walks to the base, wraps it, and prints it back.

The subtlety, which a test caught: **the base is the receiver of the innermost
method call, not the deepest identifier.** In `app.DB.Model(&U{}).Count(&n)` the
receiver is `app.DB`; walking selectors all the way down wraps `app`, which
compiles and calls the wrong thing. That is the quiet-wrongness failure mode this
project rejected an interpreter to avoid, reappearing in a plugin.

### Three plugins wanted one command name

`chi`, `gin` and `echo` all want `:routes`. Namespacing them (`:chi.routes`)
would be ugly for a name only one of them will ever claim in practice, so the
first active registration wins — matching `Renders` — and `:plugins` reports the
loser. Silently dropping it would leave someone typing a command that belongs to
a framework they are not using.

Seven of them do now — see "Shipped since v9" above, which keeps this tie-break
and adds the test that pins it.

### A renderer has two positions, not one

Shipped after v7, and worth recording because the first version got the shape
wrong. A hook was a single function, applied only to a top-level value, so a
`time.Duration` on its own line read differently from the same value in a
struct field — the plugin was visibly half-applied.

The fix is not "call the same hook in cells". The two positions genuinely differ:
a top-level value owns its line, supplies its own type prefix and may take
several lines (an `*http.Response` draws a header table); a cell has already been
labelled by its column and has room for one line. So `Render` has `Rich` and
`Inline`, and a plugin supplying only `Rich` is simply not consulted for cells —
which is exactly the old behaviour, kept as the default.

Two things the cell path does not delegate to the plugin: the result is flattened
to one line and truncated at `maxCell`. A hook is not trusted with the table it
was drawn into, however it misbehaves.

`http` deliberately has no `Inline`. A response is a header table; there is no
useful one-line form, and inventing one would be worse than the struct.

### Completing a struct literal is a scan, not a parse

Shipped after v7, and the reason it is not a parse is the reason it was on the
list: the line is *incomplete by definition* — an unclosed brace is the whole
signal — so go/parser has nothing useful to say about it.

What a scan has to get right is which braces are real. Three cases, each of
which a first version got wrong and a test caught:

- A brace inside a string, a raw string, a rune literal or a comment is not a
  brace. `s := "Point{"` opens nothing.
- `if x {` is a block. The token before the brace is an identifier, so looking
  only at that finds a "type" called `x`; the word before *it* is what decides.
- `[]Point{` and `map[string]T{` have elements, not named fields. The character
  before the identifier is a bracket, and that is the tell.

`inspect.FieldNames` is deliberately separate from `MemberNames`: a composite
literal takes fields and nothing else, so offering a method there would be
offering something that does not compile. Fields come back in declaration order
rather than sorted, because that is the order they are written in.

Candidates carry their colon — `X: ` — since completion that stops one character
short of the next thing you have to type is completion that still needs a
keystroke. And a value position is not a key position: `Point{X: fo` offers
nothing, because a field name there would not compile.

Cost: ~400-800ns on top of an ordinary keystroke, against a 12-22µs budget.

### Still open in this area

- **Completion for call arguments.** The checker knows the signature; what is
  not obvious is what to offer. Parameter *names* are not what gets typed, so
  the useful version is probably the variables in scope whose type matches the
  parameter — which is a different mechanism from every other completion here.
- **`:inspect` is still capped at the encoder's 200 items.** Making that
  negotiable means touching `internal/gluonrt/print.go`, which invariants 6 and
  8 sit on top of.
- **No plugin can add a renderer from TOML**, since a renderer is a function
  over a value tree. That is the line between the two forms.

---

## Candidate: gluon as an MCP server

Not built, and recorded here because the case for it is specific rather than
general.

gluon's checker answers `:t`, `:m`, `:ls` and `:layout` from `go/types` in about
a millisecond, and `:bench` measures rather than estimates. Those are exactly the
questions coding agents currently answer by guessing. `gluon mcp` would expose
them over stdio — with `-host`, against the module being edited — and the
existing plugin registry would supply tool definitions for free, since a plugin
command is already a name, an argument spec and a summary.

What would need deciding first: whether `eval` belongs in the tool set at all
(it runs arbitrary code, and the session replays), and what a tool call means for
session state, which every existing surface treats as a per-process thing.

---

## Status: v6 shipped (2026-08-28)

Plugins. A library-aware layer that costs nothing until the session can see the
library, and nothing per line ever.

- **`internal/plugin`** — four capability interfaces (Importer, Commander,
  Renderer, Aliaser) plus a Guider, and a `Set` that decides which apply.
- **`internal/repl/command.go`** — the command registry the whole thing hangs
  off, replacing four hand-synced lists.
- **TOML plugins** in `~/.config/gluon/plugins/*.toml`.
- **The stdlib pack** — `http`, `json`, `time`, `slog`.
- **`:plugins`, `:guide`**, a plugins section in `gluon doctor -json`.

### The command registry was the prerequisite, not a tidy-up

Adding a meta command used to mean editing four lists that did not reference
each other: the `meta()` switch, the `help` const, the `metaCommands` slice, and
the README table. Three of those fail **silently** — a command missing from the
completion list is never offered, one missing from help is undiscoverable. That
was already friction at 23 commands. With plugins adding commands at runtime it
would not have worked at all.

`[]Command` is now the single source, and the tests are the point:
`TestEveryCommandIsInHelp`, `TestEveryCommandIsOffered`, `TestNamesAreUnique`
and `TestRequiredArgumentsReportUsage` assert that dispatch, help and completion
are all generated from it and cannot diverge.

The generated usage test immediately found a real inaccuracy: `:get` was
documented as `:get <module>` but bare `:get` lists the current requirements, so
the argument is optional. Help was fixed, not the behaviour.

**One cost to watch.** `MetaNames()` sits on the keystroke path whose entire
budget is 12-22µs, and building it costs ~1µs. It is cached, and
`invalidateCommands` is what says when the cache dies. Measured after: **16.5µs
with plugins, 16.9µs without** — indistinguishable, and inside budget.

### Renderers see structure; commands see values

This is the split the whole model turns on, and it falls directly out of the
rendering split from v2: the child *describes* a value as JSON and gluon formats
it, so a renderer has the description and never the value.

That is enough for a `time.Duration`, whose Repr is already its `String()` and
which just needs its total added. It is **not** enough for a `time.Time`:

```
gluon> time.Date(2026, 8, 28, 10, 30, 0, 0, time.UTC)
(time.Time) {wall:0 ext:63923508000 loc:nil}
```

`wall` and `ext` are an internal representation explicitly outside time's API.
Decoding them in a renderer would be the same class of mistake as shipping an
interpreter — a second implementation that can disagree with the real one. So
the answer to a `time.Time` is a **command**, which rewrites to source the child
runs, where the value is still real and `String()` is just a call.

Worth knowing for anyone adding a plugin: `slog` contributes no renderer for the
same reason (`slog.Value` is a struct of an interface, a uint64 and a pointer),
and `http.Header` gets one easily because a header genuinely *is* its structure.

### Why a plugin command is safe

A plugin command is a `Rewrite func(arg string) (string, error)` producing Go
source, run through `EvalTransient` — the path `:bench`, `:err` and `:esc`
already take. Three consequences, each of them the reason for the design:

1. **It can do nothing a typed line could not.** There is no privileged API.
2. **It cannot mutate the session** (invariant 14). Pinned by
   `TestPluginCommandDoesNotMutateTheSession`, which asserts the entry count and
   the import set are unchanged — `encoding/json` must not leak in from `:json`.
3. **gluon never links the library.** The user's own module supplies it, which is
   what makes a gorm plugin possible without gluon depending on gorm.

`Command.Text` exists because the first version of `:json` printed
`(string) "{\n  \"name\": \"Ada\"\n}"` — the value printer being correct and
useless, since the whole point of the command is the formatting inside the
string.

### Renderers are Rich-only, and that is load-bearing

`pretty.Plain` and the `-json` envelopes are compatibility surfaces: pipes,
`gluon -e`, justfiles and tests read them. A plugin that could change them could
break someone's script by being installed. So `pretty.RichWith` takes the hooks
and `Plain` has no equivalent — see invariant 21.

A hook returning false falls through **byte for byte**, which
`TestHooksFallThroughByteForByte` asserts over the existing corpus. Declining is
also how a hook says "I have nothing to add": the duration renderer declines on
`3s`, because `(time.Duration) 3s  = 3s` is noise.

### Activation, and where it is allowed to cost anything

A stdlib plugin is always active. A module plugin activates when its module is in
the build list — what `:get` added, or what the attached host requires.
Recomputing that reads go.mod, so it happens **only at `:get` and `:use`**: the
same two points invariant 18 already requires the caches be dropped at. Verified:
`Requires()` appears in `get` and `refreshPlugins` and nowhere near `Eval`.

Aliases are the deliberate exception — they come from every *known* plugin, not
the active ones, because `:get uuid` is by definition asking for a module that is
not in the build list yet.

---

## Status: v5 shipped (2026-08-28)

v5 is the shell. Nothing about evaluation changed; what changed is that there
is now somewhere to put things.

- **cobra + `charmbracelet/fang`** replace the hand-rolled `flag` dispatch.
  Styled help grouped by job, styled usage errors, `gluon completion <shell>`,
  `gluon man`, per-subcommand examples.
- **`internal/ui`** is one palette for the CLI, the REPL and the inspectors,
  configurable per role under `[theme]`, honouring `NO_COLOR` and
  `CLICOLOR_FORCE`. (v9 moved the defaults into theme *files* and made `go` the
  shipped one — see "Status: v9". The per-role `[theme]` overrides described
  here still work and still win.)
- **`gluon doctor -json`**, and the report rendered through the theme.
- **Modal views** — `:inspect` over a large collection, and paging for `:src`
  and `:doc`.
- **`gluon new`** asks for a topic when it has a terminal to ask in.

### The single-dash long flag, and why pflag would have broken the README

Every flag gluon has ever documented is single-dash long form: `-host`,
`-json`, `-flat`, `-list`. The standard library's `flag` accepts that. **pflag
does not** — it reads `-json` as the four shorthands `-j -s -o -n` and fails
with "unknown shorthand flag". Nothing in the type system says so; the failure
is at runtime, on exactly the invocations printed in the README.

`normalizeArgs` (`cmd/gluon/compat.go`) rewrites a leading single dash to a
double dash when the name is longer than one character and is a registered long
flag somewhere in the tree. It stops at `--`, so `gluon run ./x -- -flag`
passes `-flag` through untouched, and it leaves `-e` and `-v` alone so they stay
real shorthands. A lone `-` is still a value. Both spellings work afterwards,
which is the point: the README stays true and `--json` becomes available.

`cmd/gluon/compat_test.go` pins every invocation in the README.

### fang v0.4.3 does not build against a v1 bubbletea stack

fang v0.4.3 requires `lipgloss/v2` beta.3, which requires an older
`charmbracelet/x/ansi` than bubbletea v1.3.10 pins — `ansi.Style` has no
`SlowBlink`, and `Italic` took no argument. MVS resolves it to a graph that
does not compile, with the error pointing inside the module cache rather than
at anything gluon wrote. **fang v1.0.0** is the version that works: it moved to
`charm.land/lipgloss/v2` and builds against current `x/ansi`.

Cost, measured: **+3ms** on a constant-path `gluon -e` (104ms → 107ms, within
noise) and **+4.3 MB** of binary (11.7 → 16.0 MB). Startup is not where gluon's
latency lives; the toolchain is.

### The alt screen, and the rule that made it safe

`internal/repl/tui.go` said "no alt screen, ever", and the reason it gave was
real: `tea.Println` is a **no-op** while the alt screen is active, so a REPL
that entered it would stop putting results into scrollback — the thing a REPL
is for.

The constraint is about *printing*, not about the alt screen, so a modal
satisfies it by not printing:

> A modal enters with `tea.EnterAltScreen`, exits with `tea.ExitAltScreen`,
> prints nothing while it is up, and leaves exactly one `Summary` line in
> scrollback on close.

`ModalSpec` carries no widget — Core stays free of bubbles and keeps running on
a background goroutine, and the driver decides. `Result.Out` is **always** set
to the same information in linear form, so the piped driver prints that and
loses nothing: `:src` and `:doc` through a pipe are byte-identical to v4.

Two things worth knowing:

- **`:inspect` shows what the child sent, not what exists.** The encoder caps a
  collection at 200 items (`maxItems`, `internal/gluonrt/print.go`), and those
  are compile-time constants in embedded source that invariants 6 and 8 sit on
  top of. The view reports the shortfall rather than implying it holds
  everything. Making the cap negotiable is a real v6+ item, not a free one.
- **`?` cannot be a keybinding in the REPL.** It is a character people type.
  The discoverable keys went into the banner instead.

### Testing a modal without a pty

The pty limitation from v4 still holds. The model tests reach the alt-screen
and print messages by executing the returned `tea.Cmd` and walking
`tea.BatchMsg`; the message types are unexported, so the assertions match on
`%T` and read the print body by reflection. `internal/repl/modal_test.go` pins
the two halves of the rule directly: nothing is printed while open, exactly one
line on close.

---

## Status: v4 shipped (2026-08-28)

Working today:

- **REPL** (`gluon`) — Bubble Tea, inline (no alt-screen), results printed
  above the prompt via `tea.Println` so scrollback survives. Async evaluation
  on a `tea.Cmd` with a spinner; type-ahead is queued. Persistent history,
  `Ctrl-R` reverse search, multi-line constructs, `Ctrl-C` abandons the
  pending buffer, `Ctrl-D` quits.
- **One-shot** (`gluon -e '<code>'`, repeatable) — the `ruby -e` shape.
- **Piped** (`echo ... | gluon`) — same engine, no banner, no raw mode.
- Auto-import via goimports; `_ = x` unused-variable suppression.
- Auto-print of the trailing expression with a type-aware value printer.
- `:= `→` =` rewrite so retyping `x := 1` works.
- fd-level muting of replayed output.
- Meta: `:help :q :src :ls :undo :reset :clear :save`.
- `:save` writes a self-contained, runnable module (`main.go` + `gluonrt.go`
  + `go.mod`) under `~/.local/share/gluon/scratch/`.

Added in v2:

- **In-process type checking** (`internal/check`) against export data. A warm
  check is ~1ms against ~190ms for a build, and it decides most lines.
- **The inspector**: `:t [-v]` (Ruby's `.class`), `:m` (`.methods` plus
  interface satisfaction), `:slice` (the runtime triple and backing-array
  aliasing), `:doc` (`ri`).
- **Errors and panics point at the typed line**, with a caret under the
  column, via `/*line*/` directives.
- Constant expressions, declarations and zero-result calls no longer build.
- A non-zero exit is reported instead of silently printing nothing.

Added in v2.5:

- **`it` and `_1`..`_N`** — irb's `_`, carried across lines.
- **`:ls`** (pry's `ls`) — what is in scope, with types.
- **The value printer tells the truth about identity**: cycles, sharing, and
  unexported composite fields (which is what makes a `%w` chain visible).
- **`:layout`** — field offsets, padding, and what reordering would save.
- **`:err`** — the wrapped chain, one level per line.
- **`:bench`** — ns/op, B/op, allocs/op through a real `testing.B`.
- **`:esc`** — escape analysis, mapped to the line you typed.
- **`:doc -src`** (pry's `show-source`).
- **Goroutines get a bounded drain**, and gluon says that it waited.
- **`:hist` / `:drop` / `:load` / `:edit` / `:time`.**

Added in v3 — gluon reaches into the project it is run in:

- **`-host <dir>` / `:use`** — attach the session to a surrounding module and
  import its packages, `internal/` included.
- **`gluon new` / `run` / `watch`** — scratch scaffolding, one module per
  scratch.
- **`gluon doctor`** — what gluon detects here, pulled forward from v4.

Added in v4 — the REPL stops being a bare prompt:

- **Tab completion** — names in scope, fields and methods, package members,
  meta commands. Ghost text, tab to accept, ctrl-n/ctrl-p to cycle.
- **Config** at `~/.config/gluon/config.toml` — preloaded imports that
  genuinely skip goimports, editor, timeout, per-host rules.
- **`:get <module>`** — third-party dependencies, explicitly.
- **`-json`** on `-e`.
- **`gluon doctor` measures the Gatekeeper exemption** instead of guessing.

Package map:

| path | role |
|---|---|
| `cmd/gluon/main.go` | flags, subcommand dispatch, one-shot mode |
| `internal/session` | classification ladder, session state, `IsIncomplete` |
| `internal/render` | session → Go program, import cache, `Display` |
| `internal/eval` | temp module, build+exec, error rewriting, timings |
| `internal/gluonrt` | the value printer + fd gate, embedded into generated code |
| `internal/repl` | `Core` (driver-agnostic session logic), Bubble Tea UI, piped driver, history |
| `internal/pretty` | value model, plain renderer (pipes) and rich renderer (lipgloss tables) |
| `internal/check` | export-data importer, in-process `go/types`, trailing-expression classification |
| `internal/inspect` | `:t`, `:m`, `:slice`, `:ls`, `:layout`, `:err`, `:bench`, `:esc` rendering |
| `internal/host` | host module detection, the nested-module go.mod, the importable-package index |
| `internal/scratch` | the scratch tree: naming, scaffolding, listing, and the scratchpad — a named session on disk |
| `internal/config` | `~/.config/gluon/config.toml` |
| `internal/complete` | a partly typed line → whole-line completions |
| `internal/ui` | the palette: one theme for the CLI, the REPL and the inspectors |
| `internal/theme` | the theme file format, the twenty-three built-in palettes, and the `.tmTheme` / VS Code / IntelliJ importer |
| `internal/syntax` | source → coloured source, without changing a byte of it |
| `internal/plugin` | the plugin model: capabilities, activation, TOML loading |
| `internal/plugins` | the compiled-in plugins (`stdlib`, `ids`, `db`, `web`), and the list of them |

---

## The rendering split

The generated program **describes** values; gluon **formats** them. The child
encodes JSON with a hand-rolled writer and stays strictly stdlib — linking
lipgloss (or even `encoding/json`) into the temp module would add
network-resolved dependencies and link time to every single line.

Consequences worth remembering:

- `eval.Result.Output` is a wire format, not display text. Anything asserting
  on it must go through `pretty.Parse` + a renderer.
- `pretty.Plain` is a **compatibility surface** — pipes, `gluon -e`, and tests
  read it. Change it deliberately.
- `lipgloss/table` renders statically, which is what scrollback needs.
  `bubbles/table` is the interactive one: right for a future `:inspect` view
  that scrolls a large collection, wrong for printed output.
- Tables are drawn only for values with structure. Scalars stay inline.

Natural extensions: a `:inspect` command opening `bubbles/table` over a large
collection; `bubbles/viewport` for paging a long `:src`; configurable row caps;
syntax highlighting in the input via the suggestion machinery. (The last of
these shipped in v9, though not via the suggestion machinery — see below.)

## v2 — the type checker and the inspector (shipped)

Everything here is built. What follows is what was learned doing it, because
several facts here are not guessable and one of them is fragile.

### The importer (`internal/check`)

**`go/importer.ForCompiler(fset,"gc",nil)` cannot work.** **Verified:**
`/opt/homebrew/Cellar/go/1.27.0/libexec/pkg/` holds only `include` and `tool`
— **zero `.a` files**. Since Go 1.20 there is no pre-installed export data, so
it resolves nothing, not even `fmt`.

**`gcexportdata.NewImporter` is also unsuitable**, despite being the obvious
fit: it runs `go list` once *per import path* and ignores the caller's
environment, so gluon's hermetic env would not apply.

**`x/tools/go/packages` is correct but shells out on every call**, which is the
cost the checker exists to avoid.

What works: one `go list -deps -export -json=ImportPath,Export -- <paths>` when
the import set grows, then `gcexportdata.Read` per package into a cache that
lives as long as the session.

- **Verified:** 58 packages load in ~76ms; the first check is ~1.8ms and every
  later one is **21–32µs**, because the shared `*types.Package` cache means no
  export data is re-read.
- **List the import paths, not `.`** — `-export` on the main package tries to
  compile the very program the checker exists to avoid compiling, and fails
  exactly when the checker is needed most.
- **The injected runtime's imports count.** `gluonrt.go` is a sibling in the
  same package and imports fmt, os, reflect, sort, strconv, syscall, unicode
  and unicode/utf8. Checking only main.go's imports fails every check with
  "could not import fmt". The tests caught this immediately; they will again.
- `Error:` on `types.Config` is not optional — without it `Check` stops at the
  first problem. `DisableUnusedImportCheck: true`, because goimports owns
  imports.
- `GoVersion` is deliberately **unset**. go/types comes from the toolchain that
  built gluon; naming a higher version errors, and a lower one would reject
  syntax the real build accepts.

The checker is an optimisation and never an authority. Every failure path
returns "unavailable" and falls back to building, and `GLUON_NO_TYPECHECK=1`
disables it outright. Keep it that way: it is the one component that could
reject a program the compiler would have accepted.

### The constant fast path

`tail.TV.Value != nil` means the answer is already known, and the program never
runs. Three guards, each for a divergence that actually happens:

- **Only predeclared basic types** (`*types.Basic` after `types.Default`).
  `time.Nanosecond` is the constant 1 but prints `1ns`, because a named type
  brings its own `String` method.
- **Only when the newest entry is the expression being printed.** Rendering
  mutes everything before it, so an older print call is not this line's answer.
- **Only when the last run exited cleanly**, so `1+1` appended to a session
  that panics still panics.

The value is converted to a real Go value of its default type and passed to the
**same encoder the child uses**, exposed as `gluonrt.Payload` from a file that
is not part of the embedded `Source`. A second formatting implementation would
have drifted on the first interesting case: `'a'` is an untyped rune whose
default type is `int32`, which prints `97 'a'` where a plain integer prints
`97`. A differential test evaluates eighteen constants both ways and compares
bytes — keep it.

Note `constant.Value.String()` shortens floats (`0.333333`); `ExactString()`
gives the rational. Neither is used: the conversion goes through
`constant.Float64Val` so `%v` matches the child exactly.

### `/*line*/` directives — the fragile part

**Verified**: the directives survive gofmt *and* goimports, and
the compiler, go/types and runtime panic tracebacks all honour them.

It must be the `/*line*/` form. In `$GOROOT/src/go/scanner/scanner.go` the
guard is `(lit[1] == '*' || offs == s.lineOffset)` — the `//` form is honoured
only at **column 1**, and gofmt indents comments inside a function body.

One directive per entry is enough: positions after it advance normally, so line
2 of a multi-line entry reports as line 2 with no arithmetic.

**The trap:** gofmt moves the directives, and moves them differently by
context.

| context | what gofmt does | compensation |
|---|---|---|
| before a statement or expression | inserts one space, stays inline | column −1 (`render.ColumnShift`) |
| before a top-level declaration | treats it as a doc comment, **hoists it onto its own line** | line −1 (`render.DeclLineShift`) |

Both are named constants pinned by an integration test that renders, formats,
builds and asserts the caret lands on the right character in four shapes. If
gofmt's behaviour changes, that test fails loudly instead of every position
being quietly wrong. Do not delete it.

For a printed expression the directive goes **inside** the call —
`__gluonPrint(/*line ...*/x)` — so a column maps to the user's expression
rather than to the wrapper.

`render.Display` strips the directives, so `:src` and `:save` stay clean.

### The inspector (`internal/inspect`)

`:t`, `:m` and `:slice` all resolve the target the same way: the expression is
**appended to the session, checked, and popped**. That is what makes imports
work — goimports resolves the rendered program, so `:t strings.Builder` sees
strings even when the session never imported it — and it reuses the trailing
expression machinery, so void calls, tuples and types arrive already
classified.

`:m` prints the value and pointer method sets together but marks pointer-only
methods; merging them would teach the wrong thing about the receiver rule.
`types.NewMethodSet` brings promoted methods from embedded types for free.
Satisfaction is tested against `error`, a stdlib handful, and every interface
the session declared — an interface is not reported as satisfying itself.

`:slice` is the only one that has to run, since a pointer exists only at run
time. Sharing is **not** pointer equality: a slice taken from the middle of
another starts elsewhere while writing into the same array, so the test is
whether the `[ptr, ptr+cap)` ranges overlap. Cap, not len — appending within
cap writes into memory another slice can already see. Strings get headers too,
with offsets in bytes.

### Still open in this area

- `:t` on an interface reports the static type only. The dynamic type needs a
  run; printing the value already shows it, and the hint says so.
- `:m` on a generic type has not been exercised.

---

## v2.5 — the REPL itself (shipped)

Everything here landed before v3, because none of it is about plumbing gluon
into another project and all of it is about the REPL being worth living in.
What follows is what was learned, because three of these were not guessable.

### `it` and `_1`..`_N`

irb's `_`, which Go cannot spell: a bare underscore is the blank identifier.
`_1` is an ordinary identifier, so ordinals are real variables. `it` is not.

**`it` cannot be a variable, and the reason is replay.** The whole session is
re-rendered on every line, so an entry that says `it` is still in the program
long after it stopped being the newest one. A single `it := _2` before the
newest entry leaves every earlier reference undefined; re-binding before each
of them declares `it` repeatedly at whatever type that line's predecessor had,
`rewriteRedeclare` turns the second `:=` into `=`, and the compiler rejects the
assignment. So `it` is resolved at render time to the ordinal in scope where it
was typed and never reaches the compiler. Entry 2 renders `_1 * 10` and entry 3
renders `_2 + 1`, each with its own type.

Rejected: `var it any` (destroys `it * 2`); per-entry shadowing blocks (nests
the tail of `main`, and `check.Printed` scans `body.List` flat, so the trailing
print would vanish into a nested block and take the constant fast path with it).

**Emission is demand-driven** — `_N :=` is emitted only when some entry
references it. That is not just tidiness: it is what keeps the newest entry in
its original `__gluonPrint(expr)` shape for free, since nothing can follow it.
That shape matters, because `check.Printed` reads the type of whatever the
print call's argument is, and an identifier bound to a constant expression is a
*variable* whose `TV.Value` is nil. Binding the newest entry would silently
cost the constant fast path.

**The one real cost, and it is unavoidable:** referencing `it`/`_N` on a
constant does force a build. `_1 := 1 + 1; __gluonPrint(_1 + 1)` reads a
variable, so `verdictConst` does not fire — ~290ms instead of ~1ms. Correct,
never wrong, and measurable with `GLUON_TIMING=1`.

Ordinals count only entries that print a value, so declarations, statements and
zero-result calls consume none. They are stable under `:undo` because `Pop`
only truncates. A user who binds `it` or `_3` themselves owns the name from
that entry onward. A multi-value expression is refused by ordinal (`_1 is 2
values … bind them yourself: a, b := strconv.Atoi("12")`) rather than left to
the compiler, which would report an "assignment mismatch" against a line the
user did not just type; `Entry.Values` is learned from a *clean* check the way
`NoValue` is learned from a failed one.

`render.Synthetic` keeps `it` and `_N` out of `importsSatisfied` and
`importFixable`. Without it, `it.Field` looks like a package qualifier and buys
a ~135ms goimports pass to learn nothing.

### The value printer tells the truth about identity

A two-node cycle used to render as **four** distinct-looking nodes ending in a
hex address. The extra level was a real bug: the pointer branch recursed with
`depth` rather than `depth+1`, so a linked list got `maxDepth+1` levels.

Two sets, not one, because they are different lessons: `path` holds the
ancestors of the value being encoded (a hit there is a **cycle** — printing it
does not terminate), `done` holds everything finished (a hit there is
**sharing** — it terminates, but hides that mutating through one name is
visible through the other).

**The child cannot decide which labels to show.** It streams, and a shared
target is fully written before the reference to it is reached, so `st.used[id]`
is always false at the moment the target's bytes are emitted. The child
therefore labels everything it tracks and `pretty.resolveIDs` clears the ids
nothing points at. That is what keeps ordinary output label-free.

**Ids must be sequence numbers, never addresses**, and the state must be
created **per `__gluonPayload` call, never at package scope**. gluon's own
process calls `Payload` many times to answer constants while the child calls it
once; a shared counter would number the same value differently in the two and
break the byte-identity `TestConstantFastPathMatchesRunning` holds — and break
it intermittently, only after the first few constants. This is a sub-clause of
invariant 6.

`maxDepth` rose 3 → 6 now that cycles are safe, with `maxNodes = 2000` added:
depth and per-level width were each bounded, their product was not. And
`pretty.cell` now truncates at 120 runes — `maxRows` capped how many rows a
table had, nothing capped how wide one could get.

Unexported composite fields are read through
`reflect.NewAt(f.Type, unsafe.Pointer(fv.UnsafeAddr())).Elem()`, with an
addressable copy made at entry and at every map value (a map value is never
addressable). `UnsafeAddr` checks only the addressable bit, never the read-only
one, which is what makes this legal. It is on by default because `fmt`'s `%+v`
already reads these fields — that is how `msg:` was visible while `err:` was
`<unexported>` — so this shows the same information structurally rather than
flattened. `GLUON_NO_UNSAFE=1` is the escape hatch. An interface field is
described by what it holds, without which a `%w` chain still collapses into one
string via `Error()`.

### `:esc` must not report gluon's own printer

`__gluonPrint` is `func(vs ...any)`, and its parameter is tagged `leaking param
content: vs` — **verified**. Every argument to it is
heap-allocated. Rendering `:esc x` the normal way would blame the user for
gluon's wrapper, so escape mode swaps *every* printable expression in the
program for `_ = expr`, not just the analysed one; otherwise each replayed
entry reports a gluon-caused escape.

`_ = expr` is a neutral sink, not a no-op: `cmd/compile/internal/escape` gives
a blank LHS a discard hole, so the RHS is still analysed and still reported
while flowing nowhere. `v := expr` would work too but puts gluon's synthetic
name in the compiler's own words.

Other details that each earn their place:

- **`-gcflags` must be one argv element** (`"-e -m"`). Repeated `-gcflags`
  *replaces* rather than appends, so passing them separately silently drops
  `-e`. And never `all=`: the default pattern is command-line-packages-only,
  and `all=` would recompile the stdlib under different flags and destroy the
  warm cache.
- **`-o /dev/null`, not `<dir>/prog`.** `-gcflags` changes the action ID, so
  this is a separate cache entry; writing it over `prog` would cost the next
  ordinary line a fresh ~105ms Gatekeeper validation.
- **`-m`, not `-m=2`.** Level 2's extra output goes to *stdout* via `Printf`,
  is multi-line with indented continuations, and was 758 lines against 217 for
  one small package.
- **`splitEscape` must run before `explain`.** `explain` runs `diagRe` over the
  whole buffer, and with `-m` on, a failed build carries hundreds of escape
  lines that all match it. Every one would be reported as an error.
- Filtering is by the synthetic `gluon-in-N.go` filename, so `gluonrt.go`'s own
  ~40 escape messages fall out for free.

Escape analysis is context-dependent and `:esc` says so. `:bench`'s allocs/op
is the better answer to "does this allocate" — measured, and context-free.

### `:bench`

`testing.Benchmark` runs fine outside a test binary; no `testing.Init()`, no
flag parsing. Three details in the rendered form:

- **The sink must not be `any`.** Assigning a non-pointer to an interface
  allocates, which would corrupt the very allocs/op being measured. `s := expr`
  then `s = expr` reuses the expression's own type without having to name it.
- **`b.ResetTimer()` after the priming evaluation**, because at `b.N == 1` an
  uncounted extra evaluation is a 100% error.
- **Return four already-divided scalars**, not the `BenchmarkResult`: its `T`
  field is a `time.Duration` the encoder renders as `"1.19s"`, a string gluon
  would have to parse back. This keeps the child describing and gluon
  formatting.

Validated against real `go test -bench` on the same expressions: 31.26 vs
29 ns/op for `fmt.Sprint(1)` (which genuinely does not allocate), and
208 B/op 1 allocs/op for `strings.Repeat("ab", 100)` in both.

### Goroutines, and the drain

`go func(){ fmt.Println("x") }()` printed **nothing**: main returned before the
goroutine was scheduled, so a learner concluded goroutines were broken. A
bounded `runtime.NumGoroutine()` poll before main returns fixes it.

**It says what it did, every time.** Waiting is a divergence from real Go, and
silently papering over that is the same category of lie as an interpreter whose
semantics differ from the compiler.

This also exposed a latent bug: `pretty.Parse` truncated at the first newline
after the payload, so anything a goroutine printed *after* the value was
silently swallowed. Trailing output is now kept.

### `:ls`, `:layout`, `:err`, `:doc -src`

- `Info.Defs/Uses/Selections/Scopes` were populated on every check and read by
  nobody. `:ls` uses `Scopes`. **A function's scope is keyed by its
  `*ast.FuncType`, not by the body `*ast.BlockStmt`** — the body block is not
  in `Scopes` at all, and looking it up there returns nil.
- Filtering the injected runtime's own declarations out of `:ls` needs
  `Fset.PositionFor(pos, false)`. The adjusted position is rewritten by the
  `/*line*/` directives to the synthetic per-entry files, so the *unadjusted*
  filename is the only thing that says which real file a declaration came from.
- **`types.Config.Sizes` was nil, which go/types documents as meaning amd64**,
  while gluon builds for arm64. They agree on word size today so nothing was
  visibly wrong, but `:layout` is the first thing to read it and it is now set
  from `runtime.GOARCH`.
- `:err` needs no runtime code: the whole unwrap walk, including
  `Unwrap() []error` for `errors.Join`, is one expression that goimports
  resolves.
- `:doc -src` is one flag on the existing `go doc` shell-out.

### `:edit` edits the session, not the program

`:src` shows the rendered program and always should. `:edit` cannot: every
expression is wrapped in `__gluonPrint(…)`, and there is no way to write a bare
`len(x)` back as valid Go, so a round trip through the rendered form would
either leak the wrapper into the buffer or lose the expression. It hands over
the lines the user typed instead, which is also what pry's `edit` does. The old
entries go back if the edited session does not evaluate.

`:drop` refuses when a later line names a value the drop would renumber —
including a bare `it`, which resolves to whichever value came last. Silently
renumbering would change what `_3` means underneath the user.

`:time` collects phases as well as printing them, because `GLUON_TIMING` writes
to stderr and that interleaves with Bubble Tea's own rendering.

### Transient evaluation leaked the import cache

`:bench`, `:err` and `:esc` all evaluate transient entries needing imports the
session does not have. `EvalTransient` popped the entry but `Eval` →
`writeResolved` sets `e.imports` unconditionally, so the import stayed in the
cached block and the *next* ordinary line wrote it verbatim, failed to build
with "imported and not used", and recovered the expensive way.

**`Analyze` had the same bug, and had it all along.** It is the ~1ms path
behind `:t`, `:m`, `:slice`, `:layout` and every new inspector, and it renders
— which resolves imports. `:t strings.Builder` on a session that never imported
`strings` left it in the cache and cost the next line a doomed build. That was
invisible while it only wasted ~300ms; it became a visible error the moment
`:esc` reported the failed build instead of silently recovering.

Both now snapshot and restore `imports` and `resolved` (and `EvalTransient`
also `healthy`: whether the session exits cleanly is a property of the session,
not of a question asked about it).

---

## v3 — host-project integration (shipped)

The feature that turns gluon from a toy into a daily tool inside a real project.
Everything below is built. What follows is what was learned, because the
roadmap's own prediction about the cost was wrong and the hardest part was not
the part it warned about.

### 3.1 Import `internal/` packages from the temp module

**The problem:** per `go help importpath`, code under `internal/` is importable
only by code sharing the import path above it. A module named
`gluon.local/session` can never import `example.com/shop/internal/pricing`,
no matter how you `replace`.

**The solution, from `cmd/go/internal/load/pkg.go` (`disallowInternal`, module
branch), and now verified end to end:**

```go
// p is in a module, so make it available based on the importer's import path
// instead of the file path (https://golang.org/issue/23970).
parentOfInternal := p.ImportPath[:i]
if str.HasPathPrefix(importerPath, parentOfInternal) {
    return nil
}
```

It is a **pure string prefix test on the importer's import path** — no file-path
or module-identity check. So the temp module is given a path *underneath* the
host:

```
module example.com/shop/gluonsession
go 1.25
require example.com/shop v0.0.0
replace example.com/shop => /path/to/shop
```

**Verified:** a session at that path imports one of the host's `internal/`
packages, constructs a value of one of its types, calls a method on it, and
prints it. `:t`, `:m` and `:doc` work against host types too, because the
checker resolves them from the same build list.

Details that each earned their place:
- Nested module paths are normal (`golang.org/x/tools` / `…/gopls`).
- Filesystem `replace` needs no `go.sum` entry and never resolves the host's
  domain, so the closure stays stdlib — hermetic for free.
- `require … v0.0.0` is **required** alongside the replace; a bare `replace`
  does not put the module in the build list.
- The `go` directive is copied **verbatim from the host's go.mod** via
  `golang.org/x/mod/modfile`. `host.buildable` rejects a directive above the
  installed toolchain up front, because `GOTOOLCHAIN=local` turns that into a
  hard error naming a temp path the user never wrote.
- `go build -o … .` is the package-directory form, which takes the module
  branch of `disallowInternal`. The explicit-file-list form takes a different
  branch with a documented failure mode — do not switch to it.

**Correction: the ~0.7s-per-line cost this section predicted does not exist.**

| session | standalone | attached |
|---|---|---|
| 5 lines, stdlib only | 332–334 ms/line | 329–337 ms/line |
| 4 lines, importing a host package on each | — | 340–390 ms/line |

Attaching costs **nothing measurable per line**. The 0.67–0.70s figure in
*Measured facts* is `go list` run *inside* a 1,724-package host, where the
pattern forces every one of the module's packages to load. gluon builds in its
own one-file temp module, so the go command loads only what is in the import
graph — one host package, not the module.

**`--host` still stays opt-in, for a better reason:** attaching changes what a
bare qualifier *means*. `trace.New(...)` in an attached session is the host's
tracer; in a standalone one it is whatever goimports finds. A REPL that silently
reached into the surrounding repo would make those indistinguishable.

### goimports cannot be trusted to resolve a host name

This was the real difficulty, and it is not mentioned anywhere above because it
was not foreseen.

`imports.Process` ranks candidates from everything it can see, and that includes
the whole module cache. With a typical cache, a bare `trace.New(...)` in a
session attached to a host with its own `internal/trace` resolved to
**`k8s.io/utils/trace`** — a package the temp module does not require, so the
type checker then failed with "no export data", which is a confusing way to be
told the wrong package was chosen.

`internal/host.Index` fixes it by settling host names *before* goimports runs:
the rendered program already carries the host's own `…/internal/trace` import,
and goimports leaves an import that is already there alone. It then only fills
in the stdlib, which is what it is good at.

**The internal rule that made this hard is also what disambiguates it.** The
host it was measured against holds **ten** packages named `trace` — one under
the module root's `internal/` and nine elsewhere in the tree. Exactly one has a
parent that prefixes the session's `…/gluonsession` path, so nine are not
candidates at all. After filtering, the whole host offers **two** importable
packages. Where a name is still ambiguous, `Index.Resolve` lists the candidates
and refuses.

**Walk the tree; do not `go list ./...`.** **Verified:** `go list ./...` in that
1,724-package module costs **1.78s**, and **1,713** of the packages are `main` —
which nothing can import. Reading one package clause per directory with
`parser.PackageClauseOnly` answers the same question in **~0.3s**, once, at
attach time. The walk skips `vendor`, `testdata`, dot- and underscore-prefixed
directories, and any subtree with its own `go.mod`.

### 3.2 `gluon new` / `run` / `watch`

**One directory per scratch with its own `go.mod`**, not flat
`//go:build ignore` files.

The `ignore` trick does work — **verified**: `cmd/go/internal/load/pkg.go` sets
`ctxt.UseAllFiles = true` in `GoFilesPackage`, and
`$GOROOT/src/cmd/go/internal/test/genflags.go` is `//go:build ignore` +
`package main` beside a different package. **Also verified:** `go build -o <tmp>
<file>.go` takes the same file-list path, so the flat form builds as well as it
runs. But **gopls greys the file out entirely** — no completion, no hover, no
jump-to-definition — which is what a learner needs most. So `-flat` is a
fallback, and it works **only** with the file-list form, never `go run ./dir`.

**`go run` swallows the exit code.** It reports a non-zero exit as
`exit status N` on stderr and exits 1 itself, so a scratch could never say
anything through gluon's status. `gluon run` builds `-o` into a temp path and
execs the result, which makes gluon's exit code the program's — and keeps
invariant 1, since nothing is written into the scratch directory.

**Order scratches by mtime, not by name.** The names carry a date but no clock,
so three scratches made in one afternoon sort alphabetically, and `gluon run`
with no argument would reach for whichever sorts last rather than the one just
edited. A directory's own mtime does not change when a file inside it is
edited, so the newest entry *inside* the directory is what counts.

`:save` now scaffolds through `internal/scratch` too, so a saved session and a
fresh scratch are the same kind of thing in the same place. `gluon new -host .`
nests the scratch under a project the way a session attaches to one.

`$EDITOR` and `$VISUAL` are both unset on a fresh macOS account, so the
fallback chain is load-bearing: `$VISUAL` → `$EDITOR` → `nvim`, `vim`, `vi`,
with `--wait` appended for `code`, `cursor`, `subl`, `zed`.

### A detector that roams finds lookalikes

The first tree-finding heuristic was wrong on the first attempt, and the fix is
a rule: **require the shape, not the name, and never search above the enclosing
repository.**

A detector that looked for `<group>/<slug>/meta.json` searched upward, found no
repository to stop at, and matched a folder in the home directory that another
application writes in exactly that shape. The fix was to require the content
that makes a match a match, and to stop the upward walk at the directory
holding `.git` — never at a `.git` in the home directory itself, which is a
dotfiles checkout.

Every search is bounded, skips `node_modules`, and is overridable by a flag or
an environment variable. That rule is invariant 12, and the database detector,
project-file lookup and `:reload` are built on it (`internal/find`).

### Flags come before *and* after the target

Go's `flag` package stops at the first non-flag argument, so `gluon new
parser-bug -debug` silently treated `-debug` as a second positional and printed
usage. Flags are read wherever they appear — the standard idiom, and the only
shape these commands read well in; pflag does it natively since the move to
cobra.

### Still open in this area

- `Index` is built eagerly on attach (~0.3s). A session that never names a host
  package pays for it anyway.

## v4 — completion, config, and composing (shipped)

Everything planned for v4 is built. What follows is what was learned, because
the one item the roadmap called "mostly wiring" had a trap in it and the one it
called free was not free until something else changed.

### Tab completion

`bubbles/textinput` does carry the machinery — `SetSuggestions`,
`AcceptSuggestion`, `NextSuggestion` — and the wiring really is small. Three
things about it are not obvious:

**Suggestions are whole lines.** `updateSuggestions` matches a candidate against
the *entire* input, and accepting appends `suggestion[len(value):]`. So the
completion for `x := strings.To` is the full `x := strings.ToUpper`, not
`ToUpper`. `complete.Suggest` returns lines for that reason.

**Its matching is case-insensitive, and Go is not.** `strings.HasPrefix(
strings.ToLower(suggestion), strings.ToLower(value))` will match
`strings.ToUpper` against a typed `strings.tou` — and since accept keeps the
typed prefix verbatim and appends the rest, the result is `strings.toupper`.
The fix is to filter case-sensitively *here*, so every candidate handed over is
a true prefix extension and the two filters can never disagree.

**Up and down are already history.** The TUI intercepts them and returns before
the input sees them, so textinput's `NextSuggestion`/`PrevSuggestion` keep
ctrl-n and ctrl-p to themselves with no rebinding.

Candidates come from four places, and the precedence for a qualifier is: what
the session already imports, then config preloads, then the attached host, then
the unambiguous standard library. Once a session imports `math/rand`, `rand.`
means that one.

**Seven stdlib base names are ambiguous** — `asn1`, `hkdf`, `pprof`, `rand`,
`scanner`, `template`, `v2`. They are offered as identifiers, so typing `ran`
still completes to `rand`, but with no member list behind them: offering
`crypto/rand`'s `Read` to someone who meant `math/rand`'s `Intn` is the same
class of mistake as guessing between two candidates (invariant 13).

**Cost, measured:** 12–22µs a keystroke warm, 2.2ms on the first completion
after a submit, when the scope cache is rebuilt from a type check. `go list std`
costs ~0.5s and runs once in a background goroutine, so nothing waits on it.

**Nothing is offered while an evaluation runs.** `Core` is not safe for
concurrent use and completion type-checks the session, so the UI does not ask
while busy — and then *has* to refresh when the result lands, or a line typed
ahead stays uncompletable until the next keystroke.

### Config, and making preloaded imports actually pay

The roadmap said preloaded imports are free because the render pass strips
unused ones. Half right, and the half that was missing is the whole value.

Preloads are not written into every program. They are a name-to-path map
consulted exactly as `host.Index` is, so an entry nothing names is not an unused
import — it is not an import. That is what makes them free.

Making them *save* anything took one more change. The cached-import fast path
only fires once `resolved` is set, which used to mean "goimports has run" — so a
preloaded import would have been resolved by the very pass it existed to
replace. A session now starts `resolved`, and `importsSatisfied` decides: a line
naming only qualifiers gluon can already resolve skips goimports, anything else
pays the full pass.

**Measured** on `gluon -e 'strings.ToUpper("hi")'`: goimports 133ms → not run,
527ms → 402ms wall clock.

Preloads carry an explicit alias when the last path element is not the package
name, so `gopkg.in/yaml.v3` is `yaml` and `github.com/foo/bar/v2` is `bar`. The
alias means the name gluon predicted is the name the program uses, whatever the
package calls itself — a wrong guess still compiles.

An unknown key in the file is an **error**, not a shrug. A typo in a config file
is otherwise invisible, which is the failure the file most needs protection
from.

### `:get`

The one thing gluon does that leaves the machine, and a command rather than a
fallback for exactly the reason the roadmap gave: an automatic `go get` on an
unresolved qualifier mutates go.mod, hits the network and takes seconds, on a
line the user may simply have mistyped.

`GOPROXY` comes back from `go env` for this one command and nothing else, so a
session that never types `:get` still cannot reach out. Afterwards everything
derived from the build list is dropped — the checker's export data, the import
cache, and the result cache, whose key is program text alone and whose entries
may now compile.

### `-json`

The envelopes are defined in one place (`cmd/gluon/json.go`) rather than derived
from the readable output, so rewording a report cannot break a justfile.

Every envelope carries `ok`, meaning *the thing you asked for turned out the way
it should*. `doctor -json` prints its whole report with `ok` false when the
config does not load: the command ran, and the answer was not the one wanted.

An error is still a JSON object on stdout. A caller piping into `jq` should not
have to read stderr to find out what happened.

### Gatekeeper: measure it, do not ask

`spctl --status` reports whether assessment is enabled globally. It does not
report whether *this terminal* is exempt through Developer Tools, and the
exemption is the thing that matters — it is ~105ms of every line that runs.

So `gluon doctor` builds a do-nothing program and times its first execution. A
never-before-seen Mach-O that starts in a millisecond was not assessed.
**Measured: 95ms** on a terminal without the exemption, confirming the
estimate. doctor now says so, and says what to do about it.

### Testing a TUI through a pipe has a trap

`waitFor(out, "appleCount")` looks like it waits for ghost text and does not:
the session echoed `appleCount := 7` into scrollback moments earlier, so the
match is immediate and the next keystrokes race the ones under test. The fix is
`waitForAfter`, which only searches output written after a recorded offset.

The same test also has to sync on something that *prints*. A binding produces no
output and the prompt is rendered from the start, so neither is a sync point —
without one, the keystrokes land while Core is busy, which is exactly when it
declines to complete.

### Still open in this area

- Completion does not know about a package's members until the checker has
  export data for it, so the first `pkg.` after `:get` may come back empty and
  fill in on the next keystroke.
- `:get` cannot remove a module; `:reset` does not drop requirements.
- Ambiguous stdlib names could be disambiguated by what the line already
  suggests (`rand.Intn` vs `rand.Read`), which is more than a prefix match.

---

## v5 and beyond

- **Completion for the arguments of a call** whose signature the checker already
  knows. Struct literal fields are done; see below.
- **`:t` reporting the dynamic type of an interface**, carried over from v2's
  open list.
- **A `:inspect` view** over a large collection, using `bubbles/table` for the
  interactive case the static renderer is wrong for.
- **Evaluating a pasted batch in one go.** Ten independent statements cost ten
  evaluations (~3.45s); one construct costs one.

---

## Permanently rejected

- **An interpreter fast path (yaegi / gomacro).** The obvious "make it instant"
  idea. yaegi's stdlib symbol tables target Go 1.21/1.22; gomacro states
  generics are not imported from the stdlib at all, which breaks `slices.Sort`
  and `maps.Keys`. The moment the interpreter and the compiler disagree, the
  tool has taught a lie. The premise is *the real toolchain is the evaluator*.
- **Output diffing to hide replayed output.** Tried and measurably broken —
  Go randomizes map iteration order, so three evaluations after a
  `for k := range m` produced interleaved garbage. fd-level muting replaced it.
- **`-ldflags="-w -s"` / `-gcflags="-N -l"` to speed builds.** Measured at zero
  benefit; `-N -l` is *slower* because it invalidates the cached optimized
  stdlib. `go run -x` shows the toolchain already passes `-dwarf=false`.
- **`-buildmode=plugin`** to hot-load into a persistent process. Dead end on
  darwin/arm64 and abandons the real-semantics premise.
- **`go.work` for host integration.** It resolves the import but does **not**
  help with `internal` — workspace membership is not import-path prefix — so
  the nested module path is needed anyway and the workspace is strictly extra
  machinery. It also makes the host a main module, dragging every one of its
  `main` packages into type-checking.
- **Writing into the host's tree, as `_gluon/`.** Works (`./...` skips
  `_`-prefixed dirs) but writes into a product repo unasked, and would
  eventually be caught by a stray `git add -A`.
- **Letting goimports resolve host packages.** Its candidate set includes the
  whole module cache, so `trace` resolves to `k8s.io/utils/trace`. See §3.1.
- **Letting textinput's own case-insensitive matching filter completions.**
  It keeps the typed prefix verbatim and appends the rest, so accepting
  `strings.ToUpper` after typing `strings.tou` yields `strings.toupper`.
  Candidates are filtered case-sensitively before they are handed over.
- **Resolving an ambiguous stdlib name for completion.** `rand` is both
  `math/rand` and `crypto/rand`; offering one package's members under a name
  that might mean the other is the same mistake as guessing between candidates.
- **Auto-`go get` on an unresolved qualifier.** It mutates go.mod, hits the
  network and takes seconds, on a line that may simply be a typo. `:get` is the
  asking.
- **Reading `spctl --status` to decide whether Gatekeeper costs anything.** It
  reports the global setting, not whether this terminal is exempt through
  Developer Tools. `gluon doctor` times a fresh binary instead.
- **A nonce in the source to defeat the result cache for a query.** It works,
  and it costs a full recompile plus ~105ms of Gatekeeper per query where
  re-execing the same binary costs 0.00s — while filling a capped LRU with keys
  that can never hit again, which evicts the `:undo` history the cache exists
  for. `EvalLive` skips the cache in both directions instead.
- **Interpolating a connection string into the generated program.** It would
  land in `<tmp>/main.go`, in `:src`, in what `:save` writes, and in the build
  cache keyed to that text. The child reads an environment variable instead.
- **A `-dsn` flag, or `:db connect <url>`.** Either would put a password in
  `~/.local/state/gluon/history` in plaintext. A connection reaches gluon only
  through a config that references where the secret lives.
- **Reading `.env.example` values.** They parse — `postgres://user:password@localhost/dbname`
  is a valid DSN — and they are fictions. The file contributes the variable
  *name*, which is the useful half.
- **A hand-rolled YAML scanner, to avoid the dependency.** Real compose files
  use anchors and merge keys, and merge keys are resolved by `Decode` rather
  than present in the node tree — so a scanner reports no environment at all and
  nothing fails. A detector that is confidently wrong is worse than one that
  says nothing. `gopkg.in/yaml.v3` was already in the build list via fang with
  its `h1:` hash already in `go.sum`.
- **Re-encoding a config file to add a `[[database]]`.** BurntSushi's encoder
  emits a whole document: comments dropped, keys reordered. The array-of-tables
  schema exists so that adding an entry is an append, which makes "every byte
  above it is untouched" a property of the shape rather than of anyone's care.
- **Deciding whether `-h` asks for help by asking the checker whether `h` is
  bound.** It reads as helpful — `:t -h` would be help in a session with no `h`
  — and it hands the type checker a decision about what runs, which invariant 5
  forbids. It would also change meaning under `GLUON_NO_TYPECHECK=1`, and `h` is
  a common name (`h := sha256.New()`). A command taking a Go expression keeps
  `-h` as Go; `--help` and `?` ask everywhere, and a failed `-h` says so.

---

## Testing the UI

**A pty harness cannot drive the Bubble Tea UI.** Under `script(1)` bubbletea
emits a cursor-position query (`ESC[6n`) that nothing answers, and the input
reader swallows injected keystrokes waiting for the reply. A *minimal*
bubbletea program reproduces this exactly, so it is a harness limitation, not
a bug worth chasing.

Two layers cover it instead:

- **Model tests** (`tui_test.go`, no build tag) drive `Update` with synthetic
  `tea.KeyMsg`s — multi-line accumulation, history, Ctrl-R, type-ahead queuing.
- **Program tests** (`tui_integration_test.go`, `-tags=integration`) run a real
  `tea.Program` with `tea.WithInput(io.Pipe)` and `tea.WithOutput(buf)`,
  polling the rendered output. This is what caught the type-ahead bug; keep it.

**A real terminal exists for pictures, not for tests.** `tools/shots` drives
gluon in a Ghostty window under `script(1)` — the combination the first
paragraph rules out — and it works there because Ghostty, the outer
terminal, answers the cursor-position query. It is not a test tier: it
asserts nothing, it needs a screen and a person's permissions, and a shot
that shows a bug is a reason to write a model or a program test, which is
what each of the six things it found got.

**`tea.ExecProcess` is not testable, and does not need to be.** `:edit` and
`:buf` hand the terminal to an editor, which no harness can drive — the pty
limitation above is the whole reason. What is actually worth covering is either
side of it, and both are reachable without a program: the command produces a
file and names the line to submit afterwards, and `Core.Submit` of that line
does the rest. `buffer_integration_test.go` writes into the file exactly where
an editor would and then submits `:buf -run`, which exercises everything but
the two lines that hand the terminal over. Those two are a `switch` on
`Result.EditThen`, and the model test asserts on the field rather than on the
command it produces.

**Three facts about bubbletea's key decoding**, established while building vim
mode and recorded here because the next person driving keys will otherwise
rediscover them one failing test at a time:

1. **`tea.KeyRunes` can carry several runes in one message.** bubbletea
   coalesces every consecutive rune in one read, so `dw` typed quickly — or
   written by a test in one `Write` — arrives as one message with two runes. A
   dispatcher reading only the first rune passes every model test and fails
   intermittently under a real program, which is the worst failure shape
   available. `vimKey` loops over `msg.Runes` for this reason.
2. **A lone escape followed by another byte in the same read becomes
   `Alt: true` on that key**, not `KeyEsc` then the key. A human cannot type
   that fast; an `io.Pipe`, ssh and tmux can. Vim mode treats any `Alt` key as
   escape-then-key, and disables textinput's four `alt` bindings while it is on
   so they cannot claim one first. A program test that wants the *ordinary*
   escape path has to write the escape on its own, with a pause after it.
3. **`tea.KeySpace` carries `Runes: []rune{' '}`.** Reverse search used to
   append the runes *and* a literal space, so every space typed there was
   doubled — a bug that stood because nothing asserted on the query string.

## Measured facts (darwin/arm64)

Re-measure rather than trusting these after a toolchain upgrade.

| thing | cost |
|---|---|
| `go` command startup (`go env`) | 0.01s |
| `go build`, temp module, warm cache, real imports | **~200ms** |
| exec a **freshly-linked** binary | ~105ms |
| exec the **same** binary again | 0.00s |
| goimports (`imports.Process`) | ~135ms, only when the import set changes |
| gofmt in-process (`format.Source`) | ~0 |
| `go list` one package in a 3,400-file module | **0.67-0.70s** |
| `go list ./...` in a 1,724-package module | **1.78s** — why `host.Index` walks instead |
| `host.Index` walk of that module | **~0.3s**, once, at **first use** — `-host` no longer pays it at startup |
| `db.Detect`, 12 directories under the module | 2.0ms |
| `db.Detect`, 108 directories | 13.5ms |
| `db.Detect`, 1,200 directories | **66ms** — why the startup screen names configured databases and does not detect |
| `go list -deps -export` for 58 packages | ~76ms, only when the import set grows |
| **type check, first** | ~1.8ms |
| **type check, steady state** | **21-32µs** (~1ms as reported by the tracer) |
| **gluon per line that must run** | **~290ms** |
| **gluon per line answered by the checker** | **~1ms** |
| gluon `:t` / `:m` | ~1ms (no build, no exec) |
| gluon `:src` / `:save` / `:undo` | ~0 (render-only / cached) |
| **gluon per line, host attached** | **~330ms** — indistinguishable from standalone |
| **completion, warm keystroke** | **12-22µs** |
| completion, first after a submit (rebuilds scope) | ~2.2ms |
| `go list std` (381 packages) | ~0.5s, once, in the background |
| **first line naming a preloaded import** | **402ms** vs 527ms unconfigured |
| exec a fresh binary, measured by `gluon doctor` | **95ms** — assessment was on |

What v2 changed, measured end to end with `GLUON_NO_TYPECHECK=1` as the
control:

| session | v1 path | v2 |
|---|---|---|
| 6 lines, 5 of them type errors | 1.00-1.04s | **0.54-0.74s** |
| 14 lines, mixed (2 constants, 2 declarations) | 4.59-4.81s | **3.83-4.07s** |

The shape of the win is worth understanding before trying to improve it: a line
whose value only exists at run time is **unchanged**, because it still needs a
binary. What got cheaper is everything else — type errors (~75ms → ~1ms, since
a failed build skips the link), declarations (~190ms → ~1ms), constants
(~290ms → ~1ms). On a realistic session that is a large minority of lines, not
a majority.

Benchmark honestly: a trivial no-import session reports ~270ms and is
misleading. Use a session with real imports and a growing program — the
one in the perf commit is `s := "hello world"` through `slices.Sort`.

What v3 changed: **nothing measurable per line.** Attaching a host was expected
to cost ~0.7s a line and costs nothing, because the temp module's import graph
is what the go command loads — not the host's 1,724 packages. See §3.1.

What v4 changed: **the goimports pass is now optional.** A line naming only
qualifiers gluon can already resolve — from config, an attached host, or the
current import set — never runs it. That is ~133ms off the first line of a
configured session, and off any later line that would have re-resolved.

Two non-obvious conclusions:

1. **Module loading, not compilation, is what makes big modules slow.** This is
   why gluon always builds in its own one-file temp module — and, as v3 showed,
   why building in one is enough to make a large host free to attach to. The
   cost is paid by whoever names a *pattern* over the module, not by whoever
   imports one package from it.
2. **~105ms of every line is macOS validating a never-before-seen Mach-O**, not
   Go. `spctl --status` reports `assessments enabled`. Adding the terminal to
   *System Settings → Privacy & Security → Developer Tools* should remove it;
   worth measuring before and after. This is now ~30% of a line.

### Optimizations already applied

Do not redo these; do not regress them either.

- **Zero-result callees are remembered** (`seedNoValue` + learning in
  `retryNoValue`). `slices.Sort(x)` cannot be printed, and without a type
  checker that costs a failed build to discover — once per function now,
  not once per use.
- **Import changes are predicted** (`render.Qualifiers` + `importsSatisfied`)
  rather than discovered by a failed build.
- **Declarations are built but not exec'd** (`render.Executes`).
- **`:src` / `:save` render without building** (`Evaluator.Render`).
- **LRU on rendered program text** (`internal/eval/cache.go`). Cannot help
  typing forward — every new line is a new program — but `:undo` hits it.
- **The type checker decides most lines** (v2, §2 above). Errors, declarations,
  constants and zero-result calls no longer reach the toolchain.
- **goimports is not re-run for a bare undefined identifier.** `undefined:
  strings` and `undefined: undefinedThing` read identically, but goimports can
  only ever resolve the first, and guessing wrong cost ~135ms on the mistake a
  person makes most often while learning. `importFixable` draws the line.
- **A receiver is not a package name.** `render.UnboundQualifiers` subtracts
  the names an entry binds, so `func (p Point) Dist() int { return p.X*p.X }`
  no longer pays ~135ms for goimports to learn nothing. `render.Qualifiers`
  stays unfiltered for the recovery path — filtering there would turn a
  resolvable import into a reported error.

### The optimizations still on the table

The type-check work is done; what remains is the cost of lines that genuinely
have to run, which is now the whole of it: **~190ms build + ~105ms exec**.

- **~105ms of every running line is macOS validating a never-before-seen
  Mach-O**, not Go. Adding the terminal to *System Settings → Privacy &
  Security → Developer Tools* should remove it. Measure before and after; this
  is the single largest remaining item and it is not a code change.
- **Pasting N independent statements costs N evaluations** (~3.45s for ten). A
  multi-line construct is one. Evaluating a pasted batch in one go would fix
  it, at the cost of muting the intermediate lines' output.
- The build itself (~190ms) is close to the floor for a real toolchain; do not
  expect much here without giving up the real compiler, which is the whole
  premise.

## Environment facts

- gluon's own `go.mod` says `go 1.27.0`, and the floor is load-bearing: the
  in-process type checker is the `go/types` of the toolchain that *built*
  gluon, and one built with 1.26 does not resolve a bare generic type name —
  `:gen`, `:impl` and `:sat` answer that it "does not name anything". Which
  `go` is on `PATH` at run time does not matter to this; which built gluon
  does. Releases are built with the newest stable Go for the same reason.
- gluon sets `GOTOOLCHAIN=local` in every child environment it builds in, which
  is what makes a `go` directive above the installed toolchain a hard error
  rather than a silent download. `host.buildable` catches that case up front and
  names the host instead of a temp path.
- `go env GOMOD` returns a real path inside a module and the literal
  `/dev/null` outside one. That is the cheapest host detector.
- Keep `GOWORK=off` in the child env — the default is `auto`, which searches
  *containing* directories, and a `go.work` somewhere above a project would
  otherwise join the build.
- `GOFLAGS` must be **replaced, not inherited**, or a monorepo's `-mod=vendor`
  silently applies to the REPL.
- `goplay` is a Playground *uploader*, not a REPL.

## Constraints — A to K

The code cites these by letter ("constraint I") the way it cites invariants by
number, so the letters are frozen the same way: a constraint that stops holding
is reworded here, never relettered. Where an invariant states the same rule, the
invariant is the authority and the letter is its short name.

| # | Constraint | Stated or enforced in |
|---|---|---|
| **A** | Compile-and-run per line. The child process dies after each evaluation. | `internal/eval` |
| **B** | The generated child program is strictly standard library. gluon links none of the libraries its plugins describe. | CONTRIBUTING.md |
| **C** | A plugin command rewrites its argument to Go source run through `EvalTransient` — it can do nothing a line you could have typed could not. | `internal/repl/plugins.go` |
| **D** | Renderers see structure only (`pretty.Value`), are Rich-only, and may never reach `pretty.Plain` or the `-json` envelopes. | invariant 21 |
| **E** | Plugin activation runs only at `:get` and `:use`, never per line. | invariant 22 |
| **F** | The type checker must never be an authority. Every failure path returns unavailable. | invariant 5 |
| **G** | `:get` is the only command that fetches modules over the network; every other build runs with `GOPROXY=off`. | `internal/eval` (`TestRestoreLeavesTheProxyOff`) |
| **H** | gluon never writes a password; a resolved DSN never enters generated source. | invariants 24, 25 |
| **I** | `pretty.Plain` and the `-json` envelopes are frozen. New fields are add-only. | CONTRIBUTING.md |
| **J** | No tool that evaluates is exposed over MCP without `--eval`. | invariant 28 |
| **K** | No new dependencies without an issue first. | CONTRIBUTING.md |

---

## Invariants — do not break these

1. **Never `go build` without `-o` into a temp path, and never with a repo
   cwd.** A bare `go build` once put a tracked 3.1 MB Mach-O into a
   repository's git history; `.gitignore` did not cover it. gluon builds in its
   own temp module and writes the binary beside it, and that is a property of
   the argument builder, not of anyone's care. The integration tests assert the
   host tree is unchanged after a run — `TestHostTreeIsUnchangedAfterAQuery`,
   `TestProfileWritesOutsideTheHostProject`,
   `TestSaveDebugLeavesTheHostTreeUntouched` — and every new feature that
   builds against a host should add its own.
2. **Always emit `func main()`, even when empty.** A `main` package without it
   fails at **link**, not compile, with `runtime.main_main·f: function main is
   undeclared in the main package` — an error with no `file:line`, so any
   diagnostic rewriter must pass unparseable messages through untouched.
3. **Never emit `_ = T` for a local type or constant.** Only *variables* are
   subject to "declared and not used"; `_ = T` for a type is itself an error.
4. **Pin the synthesized `go` directive from the host or `go env GOVERSION`,
   never from `runtime.Version()`.**
5. **The type checker must never be an authority.** Every failure path in
   `internal/check` returns unavailable so the caller builds instead. It is the
   only component that could reject a program the compiler would accept, and
   `GLUON_NO_TYPECHECK=1` has to keep working as the escape hatch.
6. **The constant fast path must produce the child's exact bytes.** It calls
   `gluonrt.Payload`, the same encoder, for that reason. Never reimplement the
   formatting; the differential test in `eval_integration_test.go` is what
   holds this.
7. **`render.ColumnShift` and `render.DeclLineShift` are gofmt's behaviour, not
   arbitrary constants.** They are pinned by an integration test that builds
   for real. If it fails after a toolchain upgrade, re-derive them — do not
   delete the test.
8. **The identity state is per-`__gluonPayload` call.** A package-level counter
   would number the same value differently in gluon's process and in the child,
   breaking invariant 6 intermittently. Ids are sequence numbers, never
   addresses — an address would also poison the LRU in `internal/eval/cache.go`.
9. **Never render `:esc` through `__gluonPrint`.** Its `...any` parameter leaks
   its argument to the heap, so the analysis would report gluon's wrapper as
   the user's allocation. Escape mode swaps *every* print in the program for
   `_ = expr`, and the differential test in `eval_integration_test.go` is what
   holds it.
10. **The newest entry keeps its `__gluonPrint(expr)` shape.** Demand-driven
    `_N` emission gives this for free, and it is what preserves the constant
    fast path: `check.Printed` reads the argument's type, and an identifier
    bound to a constant is a variable whose `TV.Value` is nil.
11. *Retired.* It constrained a tool that is no longer part of gluon; the
    number stays so that every other invariant keeps its own.
12. **A tree detector requires shape, and never looks above the repository.**
    A name alone matches lookalikes: a first attempt that searched on name from
    the user's home directory matched another application's project folder of
    exactly the right shape. Shape means a manifest *plus* the content it
    describes, and for a module-rooted tree the module root as well: a directory
    of paired Go files is every Go repository, gluon's own `internal/` included.
    A `.git` in the home directory is a dotfiles checkout, not a ceiling
    (`internal/find`).
13. **A name that matches two things is refused, with the candidates listed.**
    The one kind of exception is a single documented tie-break that announces
    itself when it fires — `DATABASE_URL` beside `TEST_DATABASE_URL` in database
    detection.
14. **A transient evaluation must not mutate evaluator state.**
    `EvalTransient` snapshots `imports`, `resolved` and `healthy`; `:bench`,
    `:err` and `:esc` all pull in imports the session does not have.
15. **Completion must never be asked while an evaluation runs.** `Core` is not
    safe for concurrent use and completion type-checks the session; the UI
    checks `busy`, and refreshes when the result lands so a line typed ahead
    does not stay uncompletable.
16. **Every completion candidate must be a case-sensitive prefix extension of
    the typed line.** textinput keeps the typed prefix and appends the rest, so
    anything else silently corrupts the line. A value a parser accepts in any
    case (`Values.Fold`, the HTTP methods) is offered in the case being typed
    for this reason — `get` after `g`, `GET` after `G`.
17. **A preloaded or host import is written only when the session names it.**
    That is what makes config free; writing the set verbatim would fail the
    build with "imported and not used".
18. **`UseHost` must drop everything derived from the old module** — the
    checker's export data, the goimports cache, and the result cache. The last
    matters most: it is keyed on program text alone, and the same text means
    something different once `go.mod` changes. `Get` must do the same, for the
    same reason.
19. **A modal must print nothing while it is open, and exactly one line when
    it closes.** `tea.Println` is a no-op under the alt screen, so anything
    said up there is said into a void — and scrollback is what a REPL is for.
    `Result.Out` must always carry the same information in linear form, so the
    piped driver loses nothing. `internal/repl/modal_test.go` holds both halves.
    A view that can *change* something holds to the same rule. `:settings`
    ships a `ModalEntry` per row, and choosing a value hands the **command**
    back to the model — `:settings value.form tree`, submitted as though it had
    been typed — rather than writing the config from inside a widget. So one
    path still decides what changing a setting means, the answer reports in the
    footer instead of into the void, and the one line left behind names the
    settings that moved rather than claiming they were browsed.
    `ModalSpec.Refresh` is the command that rebuilds the view afterwards: a
    table still describing the config it opened with is the one lie a settings
    screen must not tell, and a callback into `Core` would be the UI goroutine
    reading a session that belongs to the evaluation one.
    `internal/repl/modal_entry_test.go` holds that half.
    A choice may instead fill the prompt: `ModalChoice.Fill` closes the view and
    puts its line on the prompt, unsubmitted, through Ctrl-R's accept path. It
    is how an example leaves the help view, and it submits nothing — the one
    closing line says the line was not run. `TestChoosingAnExampleFillsThePromptAndRunsNothing`
    holds it.
20. **`normalizeArgs` must keep every documented single-dash long flag
    working.** pflag reads `-stub` as four shorthands. The rewrite is pinned
    against the documentation by `cmd/gluon/compat_test.go`; a flag added to a
    subcommand is covered automatically, but a flag *renamed* is not.
21. **A plugin renderer may never reach `pretty.Plain` or the `-json`
    envelopes.** Those are read by pipes, `gluon -e`, justfiles and tests; a
    plugin that could change them could break a script by being installed.
    Hooks are passed to `pretty.RichWith` only, a hook that declines must fall
    through byte for byte, and `TestHooksFallThroughByteForByte` is what holds
    it.
22. **Plugin activation must never run per line.** It reads go.mod. It belongs
    at `:get`, `:use` and opening a scratchpad — the points invariant 18 already
    names, plus the one that restores module requirements and re-attaches a host
    — and nowhere else. Database detection reads files and inherits the same
    rule. The third point was added with `:scratch` rather than smuggled in: a
    snapshot cannot change the build list, and a scratchpad can, so `loadPad`
    calls `refreshPlugins` where `restore` deliberately does not.
23. **A value is a DSN by shape, never by the name of the key holding it.**
    `dsn.Parse` is not given the key's name. Names may order two values that
    already parse — one documented list, printed when it fires — and may decide
    nothing else. Invariant 12 applied to a value rather than a directory.
24. **A live query is never served from the result cache, and a resolved DSN
    never enters generated source.** The cache is keyed on program text alone,
    so the same SQL twice is two different answers; and the connection string
    travels in the child's environment, so the source names a variable and holds
    no secret to leak. `EvalLive` is both halves.
25. **gluon never writes a password, and never writes into a product repo
    unasked.** The loader refuses a `password` key and a `dsn` carrying one.
    `gluon init` writes nothing without a form, or without `-write` and a named
    destination; `gluon doctor` reports a `gluon.toml` that git already tracks,
    because a stray `git add -A` is the failure the rejected `_gluon/` entry
    named and the fix for a named failure is to report it.
26. **The fd gate's platform files must not drift, and the child gets exactly
    one of them.** `fd_dup2.go` and `fd_dup3.go` differ in one syscall and
    nothing else; `FDSource` picks by the host's GOOS, which is the only GOOS
    a child can ever have; and the checker parses the same one render emits,
    or `__gluonMute` would be undefined — or defined twice. Every supported
    GOOS/GOARCH pair is pinned by CI's cross-compile job.
27. **The ordinary one-line path renders byte-identical program text through
    the batch machinery.** `MainAs` is `MainFrom` at `firstNew == len-1`, and
    `Parse` is `ParseGroups` flattened; for a single entry both must produce
    the bytes they always did, because the result cache is keyed on program
    text and Plain is a frozen surface. The constant fast path never fires
    for a batch — it answers the newest entry alone, and a batch owes output
    for every entry.
28. **No tool that evaluates is ever exposed without `--eval`, and no plugin
    command is ever static.** The tier lives on the command definition, a
    plugin command runs through `EvalTransient` so `adapt` hard-codes its
    tier, and `TestMCPTierIsIntentional` makes joining the static tier a
    deliberate act in one reviewed place. Granting execution belongs to the
    human who edits the client config.
29. **Highlighting adds escape sequences and never a byte.** `internal/syntax`
    reports offsets; `Paint` is the only writer, and it emits slices of the
    source interleaved with fixed escapes. It must not link lipgloss —
    `Style.Render` converts tabs to spaces, rewrites CRLF and pads multi-line
    strings, any of which would reformat `:src`. `TestHighlightPreservesEveryByte`
    and its fuzz target hold this over every `.go` file in the repository.
30. **`Result.Out` is plain bytes, always.** `Result.Lang` is a tag, not a
    rendering: a pipe, `gluon -e`, the `-json` envelopes and every MCP tool
    read `Out` itself, and only the TUI paints it on the way to `tea.Println`.
    Core runs on a background goroutine and must not decide what a terminal it
    cannot see is capable of. `TestResultOutIsNeverPainted` is what holds it.
31. **Plain paints nothing, by construction.** `ui.Plain()`, `pretty.PlainStyles()`
    and every non-rich path set no foreground and no attribute. The terminal's
    colour profile must never be the only thing keeping escapes out of a pipe
    or a `-json` envelope — gluon honours a set-but-empty `NO_COLOR` and termenv
    does not, which is the case that proves the profile is not enough.
32. **Switching a theme repaints everything that kept a copy of the old one.**
    The styles in `internal/repl` are package-level and are set once at startup;
    four things hold copies rather than reading them — the input's prompt, its
    completion style, the spinner, and `Core.Render`/`Core.Styles`. `applyTheme`
    is the single place that re-sets all of them, and the picker previews by
    *installing* the palette rather than drawing a swatch beside the list, so a
    surface it forgot is visible on screen the moment somebody moves the
    selection. The choice is written into `config.toml` as one line —
    byte-compared against the original and re-parsed before the rename, in
    `config.SetThemeName`, now a wrapper over `config.SetOption` — because that
    file holds everything else the user configured and their comments are not
    gluon's to lose. The renderer holds a copy of the *form* as well as the
    palette, so `installRender` is the single constructor for that closure and
    `applyTheme` and `applyValues` both go through it: two constructors would
    be two places to forget one of the two things being carried.
33. **A form changes shape, never bytes on a frozen surface.** `pretty.Plain`,
    the `-json` envelopes, and `Result.Out` from a driver that installed
    nothing are exactly what they were. A form reaches `pretty.RichIn` and
    nowhere else; the zero `Options` is the bordered table gluon always drew,
    so every caller that never heard of forms is byte-identical; `Plain` builds
    no `Options`, so there is no path to it rather than a check that could be
    forgotten; and `Core` holds the form's *value* while only a driver ever
    sets `Core.Render`. This is invariant 21 generalised from plugin hooks to
    the shapes themselves, and for the same reason — a setting that could move
    those bytes could break somebody's script by being set.
    `TestTheDefaultFormIsTheOldRenderer`, `TestAFormNeverReachesPlain` and
    `TestTheFormIsInstalledOnlyByADriver` are what hold it, and
    `TestHooksFallThroughByteForByte` now runs over every form rather than
    only the table.
34. **A scratchpad is installed by a driver, and one that did not open is never
    written to.** `NewCore` opens none and `runTUI` is the only production
    caller that does, so `gluon -e`, `gluon mcp` and the piped loop are padless
    by construction rather than by a check — which is what makes
    `cmd/gluon/mcp.go`'s "session state is per-process" true now that gluon has
    a durable session at all. The second half is the worst bug the feature can
    have: open → replay fails → session empty → one line typed → forty lines
    replaced by one. Every failure to open sets `readOnly`, `persist` returns
    while it is set, and the reason is repeated once when the next line lands.
    `TestGluonEAndTheMCPServerInstallNoPad` and
    `TestAPadThatWillNotLoadIsNeverOverwritten` are what hold it. This is
    invariant 33's pattern applied to state rather than shape.
35. **Help is answered before a command runs, and only for a token that cannot
    be its argument.** `meta()` asks `cmdspec.AsksForHelp` on the whole
    argument and answers with the page before `Run` sees anything: `--help` and
    `?` for every command, `-h` and `-help` for every command whose argument is
    not a Go expression. Asking must never do what the command does.
    `TestHelpNeverRunsTheCommand` (every command, every token, on a Core that
    would panic if `Run` were reached) and `TestHelpHasNoSideEffects` hold it.
36. **A command's usage is declared once, and its parser is held to it.** The
    `cmdspec.Spec` beside a command is what `:help`, `--help`, completion, the
    usage hint, the MCP descriptions and the docs read. A parser's flags must
    be declared and a declared flag parsed (`TestParsersSpeakOnlyDeclaredFlags`),
    every example must parse (`TestEveryExampleParses`,
    `TestEveryPluginExampleParses`), and a flag marked Hidden — a confirmation
    a view submits on the reader's behalf — reaches no page, completion, hint or
    tool (`TestHiddenFlagsAreNeverShown`, `TestNoHiddenFlagReachesATool`).
37. **A fact the code holds is never typed a second time in the docs.** The
    reference pages, the README's command list, the counts, the version and the
    Go floor are rendered from the values that define them, and `TestDocs`
    fails while `docs/` or README.md differs from what those values render. A
    guide names a command, a flag, a setting or a plugin through a helper that
    fails the build on a name the registry does not have.
38. **A transcript in the docs is recorded, never typed.** Everything a guide,
    the landing page or the README shows gluon printing is rendered from a
    `site/sessions/*.out` that `just docs-sessions` wrote by running the script
    beside it; what a transcript cannot show — what tab offers, a view — is a
    screenshot (39). Every command is worked through on a
    guide page, and `TestDocs` names one that is not.
39. **An image of gluon is a screenshot, never a drawing.** Every picture of
    gluon's terminal in the docs, the README and the promo set is captured by
    `tools/shots` from a real Ghostty window running the release build, from a
    `tapes/*.shot` file whose `Alt` line is the image's text. A page naming a
    shot not taken in both palettes, or taken at two sizes, fails the build.
    The tool touches no window but the one it opened, never quits Ghostty, and
    closes its window by letting the program in it exit.
