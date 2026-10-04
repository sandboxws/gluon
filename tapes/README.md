# Screenshots

Every image of gluon in the docs, the README and the promo set is a
screenshot of a real terminal: a fresh Ghostty window running the release
build, driven by a `.shot` file here and captured by `tools/shots`. Nothing
is drawn, and nothing is edited after the capture except to crop it.

```sh
just shots-preflight     # permissions and tools, checked without a window
just shots-check         # every shot's lines through gluon, no window: a warm cache
just shots               # every shot, both palettes, in one new window
just shots hero db       # just these
just promo               # the docs' WebPs, the promo PNGs, og.png, the contact sheet
just docs                # the pages that show them
```

## The window

`just shots` opens **one** new Ghostty window, in the Ghostty already running,
and takes every shot in it. It is the only window the tool touches:

- It never quits, restarts or signals Ghostty. No AppleScript it runs can say
  `quit`, type keystrokes into an application, run a shell, or name a window
  by its position — `osa.go` lints every template before running it.
- A window is ours only if our own `new window` returned its id and that id
  was not open before. Only that window is sized, floated or closed.
- If the new surface lands as a tab of another window, it is aborted by making
  its own process exit. Nothing is closed.
- It closes the way a person closes one: gluon quits, the wrapper exits, and
  Ghostty closes a window whose process has exited without asking anything.
  A window is closed by its id only once no process is left in it, so no
  confirmation is ever waiting. Nothing is ever clicked.
- It floats while it works, because Ghostty stops drawing a window that is
  covered, and a capture of it would be of a frame from before.
- It opens only when the screen is unlocked, awake and free of the screen
  saver, and when nobody has typed for twenty seconds — a new window takes
  the keyboard. It waits the same way before each shot.
- A driver that dies takes the window with it: the wrapper's guard ends the
  run, and the wrapper's exit closes the window.

Each shot runs a gluon that has never run — its own config, history and
scratchpads, in a home laid out as a reader's is, so paths read `~/src/shop` —
and stands in a fresh copy of `testdata/shop`, whose servers are started when
a shot says `Serve`. The Go proxy is off: a module comes from the cache or
not at all.

The window uses your Ghostty config — font, line height, padding — and sets
only its font size and colours. To shoot with different line spacing, change
`adjust-cell-height` yourself before a run; the tool measures the cell it
gets.

## Permissions

The app the command runs in — Ghostty, if you run it there — needs
Automation for Ghostty and System Events, Accessibility (to size the window),
and Screen Recording (to read a window's title and capture it). macOS applies
Screen Recording after that app restarts, and the tool never restarts
Ghostty: pick the moment yourself, or run `just shots` from Terminal.app with
the permissions granted to it. `just shots-preflight` checks all of it.

## A .shot file

```
# A comment.
Shot layout                         the image's id, and the file names
Alt :layout on the project's …      what the image shows — required, it is the alt text
Also layout-2 the same, narrowed    a second image the shot takes, and its text
Caption :layout shows where …       a line to post it with
Headline Where your struct's bytes go   the promo images' headline
Promo 16:9 4:5 1:1 og               which promo images to compose
Varies                              its numbers change between runs

Host shop                           stand in a fresh copy of testdata/shop
Serve                               with its HTTP and gRPC servers running
Setup gluon init -no-prompt -write -local   a shell line, run first, where gluon starts
Env SHOP_CONFIG=$HOST/config.yaml   $HOST is the project's copy
History :bench strconv.Itoa(n), fmt.Sprint(n)   a line already in ctrl-r's history
Args -host .                        gluon's arguments
Banner full                         the startup screen; off unless it is the point
Config input.mode vim               a setting, written by gluon's own :settings
Grid 80x18                          the window, in cells

Line :layout pricing.LineItem       typed, entered, and waited for
Type "p := Point{"                  typed, not entered
Keys "/http"                        a key at a time, as a view reads it
Paste <<                            a pasted block
x := 1
>>
Enter  Tab  Esc  Backspace  Up  Down  Left  Right  PgUp  PgDn  Ctrl r
Wait Prompt                         until nothing has been drawn for a moment
Wait /regex/                        until the program prints it
Sleep 500ms
Screenshot                          capture the window now
Screenshot layout-2
Relaunch                            quit gluon and start it again, in the same home
```

A screenshot waits for gluon to be idle, then captures the window until the
prompt's blinking cursor has been seen both ways, and keeps the frame where it
is lit.

## What comes out

- `dist/shots/raw/<id>.<go|gruv>.png` — the captures, at the display's
  resolution, with a log of what the window printed beside each.
- `docs/img/shots/<id>.<go|gruv>.webp` — lossless: the terminal below its
  titlebar, cut to its content, the two palettes padded to one size so a page
  can swap them in place. `{% shot "id" %}` puts one on a page, and the
  README's `<!-- gluon:shot id -->` in the README.
- `dist/promo/<go|gruv>/<W>x<H>/<id>.png` — 1600×900, 1080×1350, 1080×1080
  and 1200×630, the window drawn in the site's palette on the site's page,
  with the headline. The titlebar is drawn rather than captured: the captured
  one is yours — your focus, your Ghostty's update notice.
- `docs/img/og.png` — the 1200×630 card in the default palette, which is
  what a link to the site unfurls into.
- `dist/promo/index.html` — every promo image with its caption and a button
  that copies it, for picking a post.

Promo headlines are set in Geist when it is installed
(`brew install --cask font-geist font-geist-mono`) and in Helvetica Neue when
it is not.

## Not automated

The MCP shot — a coding agent answering through gluon's tools — needs an agent
session with the server approved, so it is taken by hand. Open a project with
`{"mcpServers": {"gluon": {"command": "gluon", "args": ["mcp"]}}}` in
`.mcp.json`, ask for a struct's layout or a method set, and capture the answer.
Check it for account and model details before posting it.
