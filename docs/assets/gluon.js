(function(){
'use strict';
var d=document;

/* iOS large-title collapse */
var nav=d.getElementById('nav'), big=d.getElementById('bigTitle');
if('IntersectionObserver' in window && big && nav){
  new IntersectionObserver(function(e){
    nav.classList.toggle('stuck', !e[0].isIntersecting);
  },{rootMargin:'-52px 0px 0px 0px',threshold:0}).observe(big);
  var nt=nav.querySelector('.nav-t');
  /* On the landing page the title is text, and clicking it goes back to the
     top. On every other page it is a link home, and it goes there. */
  if(nt && nt.tagName!=='A'){nt.addEventListener('click',function(){window.scrollTo({top:0,behavior:'smooth'});});}
}

/* The palette switch. A palette is a reading preference, so it survives a
   navigation: the choice is stored, and the
   inline script in <head> sets the attribute from it before the first paint,
   so a reader who chose Gruvppuccin never sees Go flash first. This draws the
   control and keeps it and the attribute in step. ?palette= is read by that
   script and never stored — it is how a link, or a headless capture, asks for
   one palette without changing yours. localStorage throws rather than returning
   null in a locked-down Safari, so both ends are wrapped. */
var TH='gluon:theme';
(function(){
  var host=d.querySelector('.nav-in'); if(!host)return;
  var root=d.documentElement, meta=d.querySelector('meta[name="theme-color"]');
  var box=d.createElement('div');
  box.className='th'; box.setAttribute('role','group'); box.setAttribute('aria-label','Palette');
  box.innerHTML='<button type="button" class="th-b" data-th="">Go</button>'+
    '<button type="button" class="th-b" data-th="gruv">Gruvppuccin</button>';
  host.appendChild(box);
  var apply=function(v){
    if(v)root.setAttribute('data-theme',v); else root.removeAttribute('data-theme');
    [].forEach.call(box.querySelectorAll('.th-b'),function(b){
      var on=b.getAttribute('data-th')===v;
      b.classList.toggle('on',on); b.setAttribute('aria-pressed',on?'true':'false');
    });
    if(meta)meta.setAttribute('content',v==='gruv'?'#101213':'#0A0A0A');
  };
  apply(root.getAttribute('data-theme')==='gruv'?'gruv':'');
  box.addEventListener('click',function(e){
    var b=e.target; while(b&&b!==box&&!(b.classList&&b.classList.contains('th-b')))b=b.parentNode;
    if(!b||b===box)return;
    var v=b.getAttribute('data-th');
    apply(v);
    try{ if(v)localStorage.setItem(TH,v); else localStorage.removeItem(TH); }catch(err){}
  });
  /* Another tab switched: follow it, so two open pages never disagree. */
  window.addEventListener('storage',function(e){ if(e.key===TH)apply(e.newValue==='gruv'?'gruv':''); });
})();

/* sidebar scrollspy with sliding pill */
var side=d.querySelector('.side-in');
if(side && 'IntersectionObserver' in window){
  var links=[].slice.call(side.querySelectorAll('a[href^="#"]')), map={}, current=null;
  links.forEach(function(a){map[a.getAttribute('href').slice(1)]=a;});
  var pill=d.createElement('span');
  pill.className='pill'; pill.setAttribute('aria-hidden','true');
  side.insertBefore(pill, side.firstChild);
  var setActive=function(id){
    var a=map[id]; if(!a||a===current)return;
    if(current){current.classList.remove('on');current.removeAttribute('aria-current');}
    current=a; a.classList.add('on'); a.setAttribute('aria-current','true');
    pill.style.height=a.offsetHeight+'px';
    pill.style.transform='translateY('+a.offsetTop+'px)';
    pill.classList.add('live');
  };
  var spy=new IntersectionObserver(function(entries){
    entries.forEach(function(en){ if(en.isIntersecting) setActive(en.target.id); });
  },{rootMargin:'-12% 0px -78% 0px',threshold:0});
  [].forEach.call(d.querySelectorAll('section.card[id]'),function(s){spy.observe(s);});
  var h=location.hash.slice(1), first=links[0]?links[0].getAttribute('href').slice(1):'';
  setActive(map[h]?h:first);
}

/* The reference filter: a search box over a page's entries. It is hidden in
   the markup and shown here, so with JS off the page is whole and there is no
   box that does nothing. / focuses it, esc clears it, and ?q= keeps it. */
[].forEach.call(d.querySelectorAll('input.flt'),function(inp){
  var wrap=inp.parentNode, sel=inp.getAttribute('data-filter')||'.ref';
  var items=[].slice.call(d.querySelectorAll(sel)), n=wrap.querySelector('.flt-n');
  wrap.hidden=false;
  var run=function(){
    var q=inp.value.trim().toLowerCase(), shown=0;
    items.forEach(function(el){
      var hit=!q||(el.getAttribute('data-f')||el.textContent).toLowerCase().indexOf(q)>=0;
      el.hidden=!hit; if(hit)shown++;
    });
    [].forEach.call(d.querySelectorAll('[data-group]'),function(g){
      g.hidden=!!q && !g.querySelector(sel+':not([hidden])');
    });
    if(n)n.textContent=q?shown+' of '+items.length:'';
    try{
      var u=new URL(location.href);
      if(q)u.searchParams.set('q',q); else u.searchParams.delete('q');
      history.replaceState(null,'',u);
    }catch(e){}
  };
  inp.addEventListener('input',run);
  inp.addEventListener('keydown',function(e){ if(e.key==='Escape'){inp.value='';run();inp.blur();} });
  d.addEventListener('keydown',function(e){
    var a=d.activeElement;
    if(e.key==='/' && a!==inp && !(a && /^(INPUT|TEXTAREA|SELECT)$/.test(a.tagName))){e.preventDefault();inp.focus();}
  });
  try{ var q0=new URLSearchParams(location.search).get('q'); if(q0){inp.value=q0;run();} }catch(e){}
});

/* copy-to-clipboard — .cmd blocks only. A transcript is not pasteable. */
var live=d.createElement('div');
live.className='sr'; live.setAttribute('aria-live','polite');
d.body.appendChild(live);
[].forEach.call(d.querySelectorAll('.cmd'),function(c){
  var bar=c.querySelector('.cmd-bar'), pre=c.querySelector('pre');
  if(!bar||!pre)return;
  var b=d.createElement('button');
  b.type='button'; b.className='copy'; b.textContent='Copy';
  b.setAttribute('aria-label','Copy code');
  b.addEventListener('click',function(){
    var done=function(msg,cls){
      b.textContent=msg; if(cls)b.classList.add(cls); live.textContent=msg;
      setTimeout(function(){b.textContent='Copy';b.classList.remove('ok');live.textContent='';},1400);
    };
    var fallback=function(){
      var r=d.createRange(); r.selectNodeContents(pre);
      var s=getSelection(); s.removeAllRanges(); s.addRange(r);
      var ok=false;
      try{ok=d.execCommand('copy');}catch(_){}
      s.removeAllRanges();
      done(ok?'Copied':'Press ⌘C', ok?'ok':null);
    };
    if(navigator.clipboard&&navigator.clipboard.writeText){
      navigator.clipboard.writeText(pre.textContent).then(function(){done('Copied','ok');},fallback);
    }else{fallback();}
  });
  bar.appendChild(b);
});

/* ── the hero replay ──────────────────────────────────────────────────────
   Every line is in the DOM from the start, so the transcript is complete
   with JS off. The animation only hides them and reveals them again. */
var term=d.getElementById('demo'), btn=d.getElementById('demoBtn');
var cap=d.getElementById('demoCap');
var panels=term? [].slice.call(term.querySelectorAll('.term-body')) : [];
var tabs=term? [].slice.call(term.querySelectorAll('.tab')) : [];
var body=panels[0];
var reduce=window.matchMedia && window.matchMedia('(prefers-reduced-motion: reduce)').matches;
if(term && body && btn && !reduce){
  var lines=[].slice.call(body.querySelectorAll('.tl'));
  var CPS=26;                       /* ms per character while "typing" */
  var timer=null, step=0, running=false;

  /* Reserve the height of the TALLEST panel, once, so switching tabs never
     moves the page under the reader. Panels are measured while briefly
     un-hidden — scrollHeight is 0 on a display:none element. */
  var reserve=function(){
    var tallest=0;
    panels.forEach(function(pn){
      var was=pn.hidden;
      if(was){ pn.hidden=false; }
      tallest=Math.max(tallest, pn.scrollHeight);
      if(was){ pn.hidden=true; }
    });
    term.style.setProperty('--term-h', tallest+'px');
  };
  /* Geist Mono changes the line box, so measure after it lands. */
  if(d.fonts && d.fonts.ready && d.fonts.ready.then){
    d.fonts.ready.then(function(){ if(!running) reserve(); });
  }

  var cursor=d.createElement('span');
  cursor.className='cur'; cursor.setAttribute('aria-hidden','true');

  var hideAll=function(){
    lines.forEach(function(l){
      l.hidden=true; l.classList.remove('typing');
      var t=l.querySelector('.txt');
      if(t){t.style.transition='none'; t.style.width='0ch';}
    });
    if(cursor.parentNode) cursor.parentNode.removeChild(cursor);
  };

  var showAll=function(){
    lines.forEach(function(l){
      l.hidden=false; l.classList.remove('typing');
      var t=l.querySelector('.txt');
      if(t){t.style.transition='none'; t.style.width='auto';}
    });
    if(cursor.parentNode) cursor.parentNode.removeChild(cursor);
  };

  var next=function(){
    if(step>=lines.length){
      running=false; term.classList.remove('play');
      body.removeAttribute('aria-busy');
      btn.textContent='Replay'; btn.setAttribute('aria-label','Replay the session');
      return;
    }
    var l=lines[step], t=l.querySelector('.txt');
    var wait=parseInt(l.getAttribute('data-wait'),10)||120;
    var typed=l.classList.contains('in');
    step++;

    timer=setTimeout(function(){
      l.hidden=false;
      if(!t){ next(); return; }
      if(!typed){
        t.style.transition='none'; t.style.width='auto';
        next();
        return;
      }
      var n=t.textContent.length, dur=Math.max(120, n*CPS);
      l.appendChild(cursor);
      /* width in ch is exact here: the block is monospace. */
      t.style.transition='width '+dur+'ms steps('+n+', end)';
      t.style.width=n+'ch';
      timer=setTimeout(function(){
        t.style.transition='none'; t.style.width='auto';
        next();
      }, dur);
    }, wait);
  };

  var start=function(){
    clearTimeout(timer); step=0; running=true;
    term.classList.add('play'); btn.textContent='Pause';
    btn.setAttribute('aria-label','Pause the replay');
    body.setAttribute('aria-busy','true');
    hideAll();
    /* let the width:0 reset paint before the first transition */
    requestAnimationFrame(function(){ requestAnimationFrame(next); });
  };

  var stop=function(){
    clearTimeout(timer); running=false;
    term.classList.remove('play');
    body.removeAttribute('aria-busy');
    btn.textContent='Replay'; btn.setAttribute('aria-label','Replay the session');
    showAll();
  };

  btn.addEventListener('click',function(){ running ? stop() : start(); });

  /* Switching tab is: stop what is playing, swap the panel, replay the new one.
     The caption travels with the panel, so a tab cannot end up describing a
     different transcript than the one on screen. */
  var select=function(name, replay){
    var idx=0;
    panels.forEach(function(pn,i){
      var on = pn.id === 'tp-'+name;
      pn.hidden=!on;
      if(on){ body=pn; idx=i; }
    });
    tabs.forEach(function(t){
      var on = t.getAttribute('data-panel')===name;
      t.classList.toggle('on',on);
      t.setAttribute('aria-selected', on?'true':'false');
    });
    lines=[].slice.call(body.querySelectorAll('.tl'));
    if(cap) cap.textContent = body.getAttribute('data-cap') || '';
    stop();
    if(replay) start();
    return idx;
  };

  tabs.forEach(function(t){
    t.addEventListener('click',function(){ select(t.getAttribute('data-panel'), true); });
    /* left/right arrows move between tabs, which is what a tablist owes. */
    t.addEventListener('keydown',function(e){
      if(e.key!=='ArrowRight' && e.key!=='ArrowLeft') return;
      e.preventDefault();
      var i=tabs.indexOf(t) + (e.key==='ArrowRight'?1:-1);
      var n=tabs[(i+tabs.length)%tabs.length];
      n.focus(); select(n.getAttribute('data-panel'), true);
    });
  });

  reserve();
  /* Only autoplay when the block is actually on screen. */
  if('IntersectionObserver' in window){
    var once=new IntersectionObserver(function(es){
      if(es[0].isIntersecting){ once.disconnect(); start(); }
    },{threshold:.35});
    once.observe(term);
  } else { start(); }
}else if(term && btn){
  /* Reduced motion: every transcript is already complete, so there is nothing
     to replay — but the tabs still have to switch, or four of the five panels
     would be unreachable. */
  btn.remove();
  tabs.forEach(function(t){
    t.addEventListener('click',function(){
      var name=t.getAttribute('data-panel');
      panels.forEach(function(pn){ pn.hidden = pn.id !== 'tp-'+name; });
      tabs.forEach(function(o){
        var on=o===t;
        o.classList.toggle('on',on);
        o.setAttribute('aria-selected', on?'true':'false');
      });
      var b=d.getElementById('tp-'+name);
      if(cap && b) cap.textContent=b.getAttribute('data-cap')||'';
    });
  });
}

/* ── the replay visualiser (section 14) ───────────────────────────────────
   Every step is precomputed as an immutable snapshot, so stepping backward is
   i-1 and reset is i=0 — there are no inverse operations to get wrong. The
   note is generated while the snapshot is built, so the caption can never
   drift out of sync with the picture. Pattern lifted from the graph
   explainers, which lifted their scrollspy from this page. */
(function(){
  var root=d.getElementById('vz-replay');
  if(!root) return;

  /* The session on show. `runs` is what actually costs something: a
     declaration is built but never executed, so it can be replayed for free. */
  var ENTRIES=[
    {src:'type Page struct { URL string }', runs:false},
    {src:'resp, _ := http.Get(u)',          runs:true, effect:true},
    {src:'resp.StatusCode',                 runs:true},
    {src:'strings.ToUpper("done")',         runs:true}
  ];

  var grid=root.querySelector('[data-rg]');
  var st=root.querySelector('[data-st]');
  var acts={};
  [].forEach.call(root.querySelectorAll('[data-act]'),function(b){
    acts[b.getAttribute('data-act')]=b;
  });

  var pinned=false, steps=[], i=0, timer=null;

  /* build returns one snapshot per line typed. Snapshot k describes the
     program gluon renders when entry k is the newest one. */
  function build(){
    var out=[], calls=0;
    for(var k=0;k<ENTRIES.length;k++){
      var cells=[], fired=0;
      for(var j=0;j<ENTRIES.length;j++){
        if(j>k){ cells.push('none'); continue; }
        var e=ENTRIES[j];
        /* Pinning is not retroactive: the entry ran once, when it was the
           newest line. That first request is real and the count must say so —
           the claim is "once, not once per line", not "never". */
        if(pinned && j===1 && j!==k){ cells.push('skip'); continue; }
        if(!e.runs){ cells.push('mute'); continue; }
        if(e.effect) fired++;
        cells.push(j===k ? 'print' : 'run');
      }
      calls+=fired;
      out.push({cells:cells, k:k, calls:calls, note:note(k,calls)});
    }
    return out;
  }

  function note(k,calls){
    var typed='line <b>'+(k+1)+'</b> of 4';
    if(pinned && k>=1){
      return typed+' · entry 2 pinned · <span class="cool">'+calls+
        ' request'+(calls===1?'':'s')+'</span> so far';
    }
    var cls=calls>1?'hot':'cool';
    return typed+' · <span class="'+cls+'">'+calls+' request'+(calls===1?'':'s')+
      '</span> so far';
  }

  function draw(){
    if(!steps.length) return;
    var s=steps[i];
    var cols='minmax(0,1fr) repeat('+ENTRIES.length+',34px)';
    var h='<div class="rg-h" style="--rg-c:'+cols+'"><span>the session</span>';
    for(var c=0;c<ENTRIES.length;c++) h+='<span>'+(c+1)+'</span>';
    h+='</div>';
    for(var r=0;r<ENTRIES.length;r++){
      var isPin=pinned&&r===1;
      h+='<div class="rg-r'+(isPin?' pin':'')+'" style="--rg-c:'+cols+'">'+
         '<span class="rg-l">'+esc(ENTRIES[r].src)+'</span>';
      for(var c2=0;c2<ENTRIES.length;c2++){
        var v=steps[c2].cells[r];
        var dim=c2>s.k?' style="opacity:.25"':'';
        h+='<span class="cl '+v+'"'+dim+'>'+glyph(v)+'</span>';
      }
      h+='</div>';
    }
    grid.innerHTML=h;
    if(st) st.innerHTML=s.note;
    if(acts.back) acts.back.disabled = i<=0;
    if(acts.fwd)  acts.fwd.disabled  = i>=steps.length-1;
  }

  function glyph(v){
    return v==='print'?'●' : v==='run'?'○' : v==='skip'?'—' : v==='mute'?'·' : '';
  }
  function esc(t){ return t.replace(/&/g,'&amp;').replace(/</g,'&lt;').replace(/>/g,'&gt;'); }

  function stop(){ if(timer){clearTimeout(timer);timer=null;} if(acts.play) acts.play.textContent='Play'; }
  function go(n){ i=Math.max(0,Math.min(steps.length-1,n)); draw(); }
  function tick(){
    if(i>=steps.length-1){ stop(); return; }
    go(i+1);
    timer=setTimeout(tick,900);
  }
  function rebuild(keep){ stop(); var at=keep?i:0; steps=build(); go(at); }

  if(acts.fwd)   acts.fwd.addEventListener('click',function(){stop();go(i+1);});
  if(acts.back)  acts.back.addEventListener('click',function(){stop();go(i-1);});
  if(acts.reset) acts.reset.addEventListener('click',function(){stop();go(0);});
  if(acts.play)  acts.play.addEventListener('click',function(){
    if(timer){stop();return;}
    if(i>=steps.length-1) i=0;
    acts.play.textContent='Pause'; tick();
  });
  if(acts.pin) acts.pin.addEventListener('click',function(){
    pinned=!pinned;
    acts.pin.classList.toggle('on',pinned);
    acts.pin.setAttribute('aria-pressed',String(pinned));
    acts.pin.textContent = pinned ? 'Unpin entry 2' : 'Pin entry 2';
    rebuild(true);
  });

  rebuild(false);
  /* Autoplay once, and only when it is actually on screen. */
  if(!reduce && 'IntersectionObserver' in window){
    var once=new IntersectionObserver(function(es){
      if(es[0].isIntersecting){ once.disconnect(); if(acts.play) acts.play.click(); }
    },{threshold:.4});
    once.observe(root);
  }
})();
})();
