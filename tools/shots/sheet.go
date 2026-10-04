package main

import (
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
)

// contactSheet writes dist/promo/index.html: every promo image, by shot and
// palette, each with its caption and a button that copies it — the page to
// pick a post from.
func contactSheet(dir string, shots []*Shot) error {
	type img struct{ Size, Palette, Src string }
	type row struct {
		*Shot
		Images []img
	}
	var rows []row
	for _, s := range shots {
		r := row{Shot: s}
		for _, p := range palettes {
			for _, size := range s.Promo {
				wh := promoSizes[size]
				src := filepath.Join(p.Name, fmt.Sprintf("%dx%d", wh[0], wh[1]), s.ID+".png")
				if _, err := os.Stat(filepath.Join(dir, src)); err == nil {
					r.Images = append(r.Images, img{Size: fmt.Sprintf("%s · %dx%d", size, wh[0], wh[1]), Palette: p.Name, Src: filepath.ToSlash(src)})
				}
			}
		}
		if len(r.Images) > 0 {
			rows = append(rows, r)
		}
	}
	sort.SliceStable(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	f, err := os.Create(filepath.Join(dir, "index.html"))
	if err != nil {
		return err
	}
	if err := sheet.Execute(f, rows); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

var sheet = template.Must(template.New("sheet").Parse(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>gluon promo images</title>
<style>
:root{color-scheme:dark;--bg:#0A0A0A;--panel:#0F0F10;--line:#1F1F22;--ink:#EDEDED;--ink-2:#A6A6AD;--ink-3:#7C7C84;--accent:#00ADD8}
*{box-sizing:border-box}
body{margin:0;background:var(--bg);color:var(--ink);font:15px/1.55 -apple-system,BlinkMacSystemFont,"Helvetica Neue",sans-serif}
main{max-width:1200px;margin:0 auto;padding:32px 16px 80px}
h1{font-size:22px;margin:0 0 6px}
.lead{color:var(--ink-2);margin:0 0 28px}
section{border:1px solid var(--line);border-radius:10px;background:var(--panel);padding:18px;margin:0 0 22px}
h2{font:600 15px/1.3 ui-monospace,Menlo,monospace;margin:0 0 10px;color:var(--accent)}
.cap{display:flex;gap:10px;align-items:flex-start;margin:0 0 6px}
.cap p{margin:0;flex:1}
.alt{color:var(--ink-3);font-size:13px;margin:0 0 14px}
button{flex:none;cursor:pointer;font:500 12px/1 -apple-system,sans-serif;color:var(--ink-2);background:transparent;border:1px solid var(--line);border-radius:6px;padding:6px 10px}
button:hover{color:var(--ink);border-color:var(--ink-3)}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(260px,1fr));gap:12px}
figure{margin:0}
figure img{display:block;width:100%;height:auto;border-radius:6px;border:1px solid var(--line)}
figcaption{font:12px/1.4 ui-monospace,Menlo,monospace;color:var(--ink-3);margin-top:5px}
</style></head><body><main>
<h1>gluon promo images</h1>
<p class="lead">Every promo image, in both palettes, with a caption to post it with. Click an image for full size; <em>Copy</em> copies the caption.</p>
{{range .}}<section id="{{.ID}}">
  <h2>{{.ID}}{{if .Varies}} · its numbers vary between runs{{end}}</h2>
  <div class="cap"><p>{{.Caption}}</p><button type="button" data-copy="{{.Caption}}">Copy</button></div>
  <p class="alt">Alt text: {{.Alt}} <button type="button" data-copy="{{.Alt}}">Copy</button></p>
  <div class="grid">{{range .Images}}
    <figure><a href="{{.Src}}"><img src="{{.Src}}" alt="" loading="lazy"></a><figcaption>{{.Palette}} · {{.Size}}</figcaption></figure>{{end}}
  </div>
</section>
{{end}}</main>
<script>
document.addEventListener("click", function (e) {
  var b = e.target.closest("button[data-copy]");
  if (!b) return;
  navigator.clipboard.writeText(b.getAttribute("data-copy")).then(function () {
    var t = b.textContent; b.textContent = "Copied"; setTimeout(function () { b.textContent = t; }, 1200);
  });
});
</script>
</body></html>
`))
