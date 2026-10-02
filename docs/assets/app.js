/* Master KVM in 10 Days: progress, quizzes, simulated terminal, diagrams, search.
   Vanilla JS, no dependencies. Progress lives in localStorage when available. */
(function () {
  "use strict";

  window.addEventListener("error", function (e) {
    document.documentElement.setAttribute("data-js-error", String(e.message || e.error || "error").slice(0, 200));
  });
  var ROOT = document.body.getAttribute("data-root") || "";
  var PAGE = {};
  try {
    var pd = document.getElementById("page-data");
    if (pd) PAGE = JSON.parse(pd.textContent);
  } catch (e) { PAGE = {}; }

  /* ------------------------------------------------------------ helpers */
  function $(sel, el) { return (el || document).querySelector(sel); }
  function $$(sel, el) { return Array.prototype.slice.call((el || document).querySelectorAll(sel)); }
  function h(tag, attrs, html) {
    var el = document.createElement(tag);
    if (attrs) for (var k in attrs) {
      if (k === "class") el.className = attrs[k];
      else if (k === "text") el.textContent = attrs[k];
      else if (k.slice(0, 2) === "on") el.addEventListener(k.slice(2), attrs[k]);
      else if (attrs[k] !== null && attrs[k] !== undefined && attrs[k] !== false) el.setAttribute(k, attrs[k]);
    }
    if (html !== undefined && html !== null) el.innerHTML = html;
    return el;
  }
  function esc(s) {
    return String(s).replace(/[&<>"']/g, function (c) {
      return { "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" }[c];
    });
  }
  function textOf(html) { var d = document.createElement("div"); d.innerHTML = html; return d.textContent; }
  function shuffle(a) {
    a = a.slice();
    for (var i = a.length - 1; i > 0; i--) { var j = Math.floor(Math.random() * (i + 1)); var t = a[i]; a[i] = a[j]; a[j] = t; }
    return a;
  }
  var ICONS = {
    check: '<path d="M20 6.5 9.5 17 4 11.5"/>', x: '<path d="M6 6l12 12M18 6 6 18"/>',
    star: '<path d="m12 2.8 2.8 6 6.5.7-4.9 4.4 1.4 6.4L12 17l-5.8 3.3 1.4-6.4-4.9-4.4 6.5-.7z"/>',
    heart: '<path d="M12 20.5s-8.5-5-8.5-11A4.8 4.8 0 0 1 12 6.6a4.8 4.8 0 0 1 8.5 2.9c0 6-8.5 11-8.5 11z"/>',
    trophy: '<path d="M8 3.5h8v6a4 4 0 0 1-8 0z"/><path d="M8 5.5H4.5a3 3 0 0 0 3.5 4M16 5.5h3.5a3 3 0 0 1-3.5 4M12 13.5v4M8 21h8M9.5 17.5h5"/>',
    sword: '<path d="M14.5 3.5H20.5V9.5L9 21 3 15z"/><path d="m7 13 4 4M3 21l3-3"/>',
    arrow: '<path d="M4 12h16M14 6l6 6-6 6"/>', play: '<path d="M7 4.5v15l12-7.5z"/>', reset: '<path d="M3.5 12a8.5 8.5 0 1 0 2.6-6.1"/><path d="M3.5 3.5V9H9"/>',
    bulb: '<path d="M9 18h6M10 21.5h4"/><path d="M12 2.5a6.5 6.5 0 0 0-4 11.6c.8.7 1 1.4 1 2.4v1.5h6v-1.5c0-1 .3-1.7 1-2.4a6.5 6.5 0 0 0-4-11.6z"/>',
    flame: '<path d="M12 21.5c4 0 7-2.8 7-6.8 0-4.6-4.2-6.6-5-11.2-2.6 1.8-3.8 4.4-3.8 6.6-1-.8-1.9-2-2-3.6C6.4 8.4 5 11 5 14.7c0 4 3 6.8 7 6.8z"/>',
    circle: '<circle cx="12" cy="12" r="8.5"/>', target: '<circle cx="12" cy="12" r="9.5"/><circle cx="12" cy="12" r="5.5"/><circle cx="12" cy="12" r="1.5"/>'
  };
  function icon(name, cls) {
    return '<svg class="icon ' + (cls || "") + '" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">' + (ICONS[name] || "") + "</svg>";
  }
  function citeHTML(cites) {
    if (!cites || !cites.length) return "";
    return '<span class="cite">see ' + cites.map(function (c) {
      return '<a href="' + esc(ROOT + c.href) + '">' + esc(c.label) + "</a>";
    }).join(", ") + "</span>";
  }
  function today(offset) {
    var d = new Date(); d.setDate(d.getDate() + (offset || 0));
    return d.getFullYear() + "-" + ("0" + (d.getMonth() + 1)).slice(-2) + "-" + ("0" + d.getDate()).slice(-2);
  }

  /* ------------------------------------------------------------ store */
  var KEY = "kvm10-progress-v1";
  var storageOK = true;
  var S;
  function blank() {
    return { v: 1, xp: 0, awards: {}, ticks: {}, prereq: {}, quiz: {}, term: {}, cards: {}, seen: {}, boss: {}, done: {}, mission: {}, streak: { n: 0, last: "" } };
  }
  function load() {
    try {
      var raw = window.localStorage.getItem(KEY);
      S = raw ? JSON.parse(raw) : blank();
      var b = blank();
      for (var k in b) if (!(k in S)) S[k] = b[k];
    } catch (e) { storageOK = false; S = blank(); }
  }
  function save() {
    if (!storageOK) return;
    try { window.localStorage.setItem(KEY, JSON.stringify(S)); } catch (e) { storageOK = false; showStorageNote(); }
  }
  load();

  function bumpStreak() {
    var t = today(), y = today(-1);
    if (S.streak.last === t) return;
    S.streak.n = (S.streak.last === y) ? S.streak.n + 1 : 1;
    S.streak.last = t;
  }
  function streakNow() {
    return (S.streak.last === today() || S.streak.last === today(-1)) ? S.streak.n : 0;
  }
  /* The level title follows completed days (one title per badge); XP is the score. */
  var TITLES = ["Curious admin", "Host tamer", "Domain wrangler", "Bridge builder", "Chain smith", "Gatekeeper",
    "Recovery ranger", "Live mover", "API pilot", "VMM builder", "Hypervisor hero"];
  function level() {
    var l = 0;
    for (var i = 1; i <= 10; i++) if (S.done["d" + i]) l++;
    return l;
  }
  var pendingXP = 0, pendingLabel = "", pendingN = 0, pendingTimer = null;
  function flushXP() {
    if (pendingXP) toast("+" + pendingXP + " XP" + (pendingN === 1 && pendingLabel ? " · " + pendingLabel : ""));
    pendingXP = 0; pendingLabel = ""; pendingN = 0; pendingTimer = null;
  }
  function award(key, amount, label) {
    if (S.awards[key]) return false;
    S.awards[key] = amount;
    S.xp += amount;
    bumpStreak();
    save();
    renderHUD(true);
    if (label) {
      pendingXP += amount; pendingLabel = label; pendingN++;
      clearTimeout(pendingTimer); pendingTimer = setTimeout(flushXP, 800);
    }
    return true;
  }

  /* ------------------------------------------------------------ HUD, toasts */
  function renderHUD(bump) {
    $$("[data-xp]").forEach(function (el) { el.textContent = S.xp; });
    $$("[data-streak]").forEach(function (el) { el.textContent = streakNow(); });
    $$("[data-level]").forEach(function (el) { el.textContent = (level() + 1) + " · " + TITLES[level()]; });
    $$("[data-streak-unit]").forEach(function (el) { el.textContent = streakNow() === 1 ? "day" : "days"; });
    var lb = $("[data-level-bar]");
    if (lb) {
      var l = level();
      $("small", lb).textContent = l < 10 ? "Earn the Day " + (l + 1) + " badge to become " + TITLES[l + 1] : "Top title reached: Hypervisor hero";
      $(".bar span", lb).style.width = (10 * l) + "%";
    }
    if (bump) { var c = $("[data-xp-chip]"); if (c) { c.classList.remove("bump"); void c.offsetWidth; c.classList.add("bump"); } }
  }
  function toast(msg, big) {
    var zone = $("[data-toasts]");
    if (!zone) return;
    var t = h("div", { class: "toast" + (big ? " big" : ""), role: "status" }, icon(big ? "trophy" : "star") + "<span>" + esc(msg) + "</span>");
    while (zone.children.length >= 3) zone.firstChild.remove();
    zone.appendChild(t);
    setTimeout(function () { t.classList.add("out"); setTimeout(function () { t.remove(); }, 320); }, big ? 3600 : 2200);
  }
  function showStorageNote() {
    var n = $("[data-storage-note]");
    if (n && !storageOK) n.textContent = "Progress is not being saved: this browser blocks storage for the page.";
  }

  /* ------------------------------------------------------------ confetti */
  function confetti() {
    if (window.matchMedia && window.matchMedia("(prefers-reduced-motion: reduce)").matches) return;
    var c = h("canvas", { class: "confetti", "aria-hidden": "true" });
    document.body.appendChild(c);
    var ctx = c.getContext("2d"), W, H, dpr = window.devicePixelRatio || 1;
    function size() { W = c.width = innerWidth * dpr; H = c.height = innerHeight * dpr; }
    size();
    var P = [];
    for (var i = 0; i < 170; i++) {
      P.push({ x: W / 2 + (Math.random() - 0.5) * W * 0.3, y: H * 0.35, vx: (Math.random() - 0.5) * 16 * dpr, vy: (-Math.random() * 15 - 5) * dpr,
        w: (6 + Math.random() * 7) * dpr, h: (4 + Math.random() * 5) * dpr, r: Math.random() * 6, vr: (Math.random() - 0.5) * 0.35,
        c: "hsl(" + Math.floor(Math.random() * 360) + " 85% 58%)" });
    }
    var t0 = performance.now();
    function frame(t) {
      ctx.clearRect(0, 0, W, H);
      P.forEach(function (p) {
        p.vy += 0.42 * dpr; p.vx *= 0.99; p.x += p.vx; p.y += p.vy; p.r += p.vr;
        ctx.save(); ctx.translate(p.x, p.y); ctx.rotate(p.r); ctx.fillStyle = p.c; ctx.fillRect(-p.w / 2, -p.h / 2, p.w, p.h); ctx.restore();
      });
      if (t - t0 < 3200) requestAnimationFrame(frame); else c.remove();
    }
    requestAnimationFrame(frame);
  }

  /* ------------------------------------------------------------ progress model */
  function dayModel(n, labs, quizIds, termIds, hasBoss, nTerms, nConcepts) {
    var steps = 0, stepsDone = 0, checks = 0, checksDone = 0;
    labs.forEach(function (l) {
      if (l.optional) return;   /* optional labs (Appendix B.2) never count toward the badge */
      l.steps.forEach(function (s) { steps++; if (S.ticks[s]) stepsDone++; });
      if (l.check) { checks++; if (S.ticks[l.check]) checksDone++; }
    });
    var qOK = quizIds.filter(function (q) { return S.quiz[q] && S.quiz[q].ok; }).length;
    var tUsed = termIds.filter(function (t) { return S.term["d" + n] && S.term["d" + n][t]; }).length;
    var known = 0, cards = S.cards["d" + n] || {};
    for (var k in cards) if (cards[k] === "known") known++;
    var seen = Object.keys(S.seen["d" + n] || {}).length;
    var parts = [
      [0.45, steps + checks, stepsDone + checksDone], [0.25, quizIds.length, qOK], [0.15, hasBoss ? 1 : 0, S.boss["d" + n] ? 1 : 0],
      [0.05, termIds.length, tUsed], [0.05, nConcepts, Math.min(seen, nConcepts)], [0.05, nTerms, Math.min(known, nTerms)]
    ];
    var wsum = 0, acc = 0;
    parts.forEach(function (p) { if (p[1] > 0) { wsum += p[0]; acc += p[0] * (p[2] / p[1]); } });
    var pct = wsum ? Math.round(100 * acc / wsum) : 0;
    var complete = qOK === quizIds.length && (!hasBoss || !!S.boss["d" + n]) && checksDone === checks;
    if (S.done["d" + n]) pct = Math.max(pct, 100 * (complete ? 1 : 0)) || pct;
    return { pct: complete && pct < 100 ? Math.max(pct, 90) : pct, complete: complete, qOK: qOK, qN: quizIds.length,
      checks: checks, checksDone: checksDone, steps: steps, stepsDone: stepsDone, boss: !!S.boss["d" + n], hasBoss: hasBoss,
      tUsed: tUsed, tN: termIds.length };
  }
  function setRing(el, pct) {
    if (!el) return;
    var fg = $(".ring-fg", el);
    if (fg) fg.style.strokeDashoffset = String(326.73 * (1 - pct / 100));
  }
  function unlocked(n) { return n === 1 || !!S.done["d" + (n - 1)]; }

  /* ------------------------------------------------------------ theme */
  function initTheme() {
    var btn = $("[data-theme-toggle]");
    if (!btn) return;
    btn.addEventListener("click", function () {
      var cur = document.documentElement.getAttribute("data-theme");
      if (!cur) cur = (window.matchMedia && window.matchMedia("(prefers-color-scheme: dark)").matches) ? "dark" : "light";
      var next = cur === "dark" ? "light" : "dark";
      document.documentElement.setAttribute("data-theme", next);
      try { localStorage.setItem("kvm10-theme", next); } catch (e) { /* ignore */ }
    });
  }

  /* ------------------------------------------------------------ copy buttons */
  function copyText(text, btn) {
    function done() {
      if (!btn) return;
      var span = $("span", btn), old = span ? span.textContent : "";
      btn.classList.add("done"); if (span) span.textContent = "Copied";
      setTimeout(function () { btn.classList.remove("done"); if (span) span.textContent = old; }, 1400);
      award("first-copy", 5, "first copy");
    }
    function fallback() {
      var ta = h("textarea", { style: "position:fixed;left:-9999px;top:0", "aria-hidden": "true" });
      ta.value = text; document.body.appendChild(ta); ta.select();
      try { document.execCommand("copy"); done(); } catch (e) { /* ignore */ }
      ta.remove();
    }
    if (navigator.clipboard && window.isSecureContext) navigator.clipboard.writeText(text).then(done, fallback);
    else fallback();
  }
  function initCopy() {
    document.addEventListener("click", function (e) {
      var btn = e.target.closest ? e.target.closest("[data-copy]") : null;
      if (!btn) return;
      var box = btn.closest(".step") || btn.closest(".codebox");
      var code = box ? $("code", box) : null;
      if (code) copyText(code.textContent, btn);
    });
  }

  /* ------------------------------------------------------------ dialogs: focus trap, inert page */
  function trapFocus(layer, box, onClose) {
    var others = $$("body > *").filter(function (el) { return el !== layer && el.tagName !== "SCRIPT"; });
    others.forEach(function (el) { el.inert = true; });
    function key(e) {
      if (e.key === "Escape") { e.preventDefault(); onClose(); return; }
      if (e.key !== "Tab") return;
      var f = $$('a[href], button:not([disabled]), input:not([disabled]), [tabindex]:not([tabindex="-1"])', box)
        .filter(function (x) { return x.offsetParent !== null; });
      if (!f.length) return;
      var first = f[0], last = f[f.length - 1];
      if (e.shiftKey && document.activeElement === first) { e.preventDefault(); last.focus(); }
      else if (!e.shiftKey && document.activeElement === last) { e.preventDefault(); first.focus(); }
    }
    layer.addEventListener("keydown", key);
    return function release() {
      others.forEach(function (el) { el.inert = false; });
      layer.removeEventListener("keydown", key);
    };
  }
  var activateTarget = function () { return false; };   /* set by the day page */

  /* ------------------------------------------------------------ search */
  var searchLoaded = false, searchData = null, searchSel = -1;
  function loadSearch(cb) {
    if (searchLoaded) { cb(); return; }
    var s = h("script", { src: ROOT + "assets/search-index.js" });
    s.onload = function () { searchLoaded = true; searchData = window.KVM_SEARCH || []; cb(); };
    s.onerror = function () { searchLoaded = true; searchData = []; cb(); };
    document.head.appendChild(s);
  }
  function initSearch() {
    var layer = $("[data-search-layer]"), input = $("[data-search-input]"), out = $("[data-search-results]");
    if (!layer) return;
    var lastFocus = null, release = null;
    function open() {
      if (!layer.hidden) return;
      lastFocus = document.activeElement;
      layer.hidden = false; input.value = ""; out.innerHTML = '<p class="sr-empty">Type to search days, labs, glossary, commands and the manual.</p>';
      release = trapFocus(layer, $(".search-box", layer), close);
      setTimeout(function () { input.focus(); }, 10);
      loadSearch(function () { if (input.value) run(); });
    }
    function close(noRestore) {
      if (layer.hidden) return;
      layer.hidden = true;
      if (release) { release(); release = null; }
      if (noRestore !== true && lastFocus && lastFocus.focus) lastFocus.focus();
    }
    function go(a) {
      var url = new URL(a.href, location.href);
      close(true);
      if (url.pathname === location.pathname && url.hash) {
        if (location.hash !== url.hash) history.pushState(null, "", url.hash);
        if (!activateTarget(url.hash)) location.hash = url.hash;
        return true;
      }
      location.href = a.href;
      return true;
    }
    function hl(text, terms) {
      var t = esc(text);
      terms.forEach(function (w) {
        if (w.length < 2) return;
        var re = new RegExp("(" + w.replace(/[.*+?^${}()|[\]\\]/g, "\\$&") + ")", "ig");
        t = t.replace(re, "<mark>$1</mark>");
      });
      return t;
    }
    function snippet(x, terms) {
      var lx = x.toLowerCase(), pos = -1;
      for (var i = 0; i < terms.length && pos < 0; i++) pos = lx.indexOf(terms[i]);
      var start = Math.max(0, pos - 60);
      return (start ? "…" : "") + x.slice(start, start + 200);
    }
    function run() {
      var q = input.value.trim().toLowerCase();
      if (!q) { out.innerHTML = '<p class="sr-empty">Type to search days, labs, glossary and the manual.</p>'; return; }
      if (!searchData) { out.innerHTML = '<p class="sr-empty">Loading…</p>'; return; }
      var terms = q.split(/\s+/).filter(Boolean);
      var res = [];
      searchData.forEach(function (e) {
        var t = e.t.toLowerCase(), x = (e.x || "").toLowerCase(), k = e.k.toLowerCase(), c = (e.c || "").toLowerCase(), score = 0;
        for (var i = 0; i < terms.length; i++) {
          var w = terms[i], s = 0;
          if (t.indexOf(w) >= 0) s += t.indexOf(w) === 0 ? 8 : 5;
          if (x.indexOf(w) >= 0) s += 1 + Math.min(2, x.split(w).length - 1) * 0.5;
          if (c.indexOf(w) >= 0) s += 1 + Math.min(2, c.split(w).length - 1) * 0.5;
          if (k.indexOf(w) >= 0) s += 1;
          if (!s) return;
          score += s;
        }
        if (k === "glossary") score += 2;
        if (k.indexOf("day") === 0) score += 1;
        res.push([score, e]);
      });
      res.sort(function (a, b) { return b[0] - a[0]; });
      res = res.slice(0, 40);
      searchSel = res.length ? 0 : -1;
      if (!res.length) { out.innerHTML = '<p class="sr-empty">No results. Try another word, such as "overlay" or "virsh".</p>'; return; }
      out.innerHTML = res.map(function (r, i) {
        var e = r[1];
        return '<a class="sr-item' + (i === 0 ? " sel" : "") + '" role="option" href="' + esc(ROOT + e.u) + '"><span class="sr-kind">' + esc(e.k) +
          '</span><span class="sr-title">' + hl(e.t, terms) + '</span><span class="sr-snip">' +
          hl(snippet(((e.x || "").toLowerCase().indexOf(terms[0]) < 0 && e.c && e.c.toLowerCase().indexOf(terms[0]) >= 0) ? "$ " + e.c : (e.x || ""), terms), terms) + "</span></a>";
      }).join("");
    }
    function move(d) {
      var items = $$(".sr-item", out);
      if (!items.length) return;
      searchSel = (searchSel + d + items.length) % items.length;
      items.forEach(function (it, i) { it.classList.toggle("sel", i === searchSel); });
      items[searchSel].scrollIntoView({ block: "nearest" });
    }
    $$("[data-search-open]").forEach(function (b) { b.addEventListener("click", open); });
    $("[data-search-close]").addEventListener("click", close);
    layer.addEventListener("click", function (e) { if (e.target === layer) close(); });
    out.addEventListener("click", function (e) { var a = e.target.closest(".sr-item"); if (a) { e.preventDefault(); go(a); } });
    input.addEventListener("input", function () { loadSearch(run); });
    input.addEventListener("keydown", function (e) {
      if (e.key === "ArrowDown") { e.preventDefault(); move(1); }
      else if (e.key === "ArrowUp") { e.preventDefault(); move(-1); }
      else if (e.key === "Enter") { var it = $$(".sr-item", out)[searchSel]; if (it) go(it); }
    });
    document.addEventListener("keydown", function (e) {
      var tag = (e.target.tagName || "").toLowerCase();
      var typing = tag === "input" || tag === "textarea" || e.target.isContentEditable;
      if ((e.key === "/" && !typing) || ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "k")) { e.preventDefault(); open(); }
    });
  }

  /* ------------------------------------------------------------ export / import / reset */
  function initDataTools() {
    var ex = $("[data-export]"), im = $("[data-import]"), rs = $("[data-reset]");
    if (ex) ex.addEventListener("click", function () {
      var blob = new Blob([JSON.stringify(S, null, 1)], { type: "application/json" });
      var a = h("a", { href: URL.createObjectURL(blob), download: "kvm-in-10-days-progress.json" });
      document.body.appendChild(a); a.click(); setTimeout(function () { URL.revokeObjectURL(a.href); a.remove(); }, 500);
    });
    if (im) im.addEventListener("click", function () {
      var f = h("input", { type: "file", accept: "application/json,.json" });
      f.addEventListener("change", function () {
        var file = f.files && f.files[0];
        if (!file) return;
        var r = new FileReader();
        r.onload = function () {
          try {
            var data = JSON.parse(r.result);
            if (!data || data.v !== 1 || typeof data.xp !== "number") throw new Error("not a progress file");
            S = data; save(); location.reload();
          } catch (e) { alert("That file is not a progress export from this site."); }
        };
        r.readAsText(file);
      });
      f.click();
    });
    if (rs) rs.addEventListener("click", function () {
      if (!confirm("Reset all progress, XP, badges and streak in this browser?")) return;
      S = blank(); save(); location.reload();
    });
  }

  /* ============================================================ HOME */
  function initHome() {
    var days = PAGE.days || [];
    var badges = 0, totalPct = 0, current = null;
    days.forEach(function (d) {
      var m = dayModel(d.n, d.labs, d.quiz, d.terminal, d.boss, d.terms, d.concepts);
      if (m.complete && !S.done["d" + d.n]) { S.done["d" + d.n] = true; save(); }
      var done = !!S.done["d" + d.n];
      if (done) badges++;
      totalPct += done ? 100 : m.pct;
      var li = $('[data-quest="' + d.n + '"]');
      if (!li) return;
      var bar = $("[data-quest-bar]", li); if (bar) bar.style.width = (done ? 100 : m.pct) + "%";
      var st = $("[data-quest-status]", li);
      li.classList.remove("ahead", "done", "current");
      /* Every day is open: the first unfinished day is "Up next", later unfinished days are previews (codex-site 10). */
      if (done) { li.classList.add("done"); st.textContent = "Complete"; li.classList.add("earned"); }
      else if (current !== null) { li.classList.add("ahead"); st.textContent = m.pct ? m.pct + "% (ahead)" : "Preview"; }
      else { st.textContent = m.pct ? m.pct + "%" : "Up next"; }
      if (!done && current === null) current = d;
      var shelf = $('[data-shelf="' + d.n + '"]');
      if (shelf) { shelf.classList.toggle("earned", done); shelf.setAttribute("style", li.getAttribute("style") || ""); }
    });
    if (current) {
      var cur = $('[data-quest="' + current.n + '"]'); if (cur) cur.classList.add("current");
      var cont = $("[data-continue]"), lab = $("[data-continue-label]");
      if (cont && lab) {
        cont.href = "day-" + ("0" + current.n).slice(-2) + ".html";
        var any = S.xp > 0;
        lab.textContent = (any ? "Continue with Day " : "Start Day ") + current.n + ": " + current.title;
      }
    } else {
      var lab2 = $("[data-continue-label]"); if (lab2) lab2.textContent = "All 10 days complete. Review Day 10";
      var cont2 = $("[data-continue]"); if (cont2) cont2.href = "day-10.html";
    }
    $$("[data-badge-count]").forEach(function (el) { el.textContent = badges; });
    var pct = days.length ? Math.round(totalPct / days.length) : 0;
    $$("[data-total-pct]").forEach(function (el) { el.textContent = pct + "%"; });
    setTimeout(function () { setRing($("[data-total-ring]"), pct); }, 60);
  }

  /* ============================================================ DAY */
  var DAY = 0, dkey = "";
  function dayInfo() {
    return {
      labs: PAGE.labs || [],
      quiz: [].concat.apply([], (PAGE.quiz || []).map(function (g) { return g.questions.map(function (q) { return q.id; }); })),
      terminal: (PAGE.terminal || []).map(function (t) { return t.id; }),
      boss: !!PAGE.boss, terms: (PAGE.terms || []).length, concepts: (PAGE.concepts || []).length
    };
  }
  function refreshDay() {
    var di = dayInfo();
    var m = dayModel(DAY, di.labs, di.quiz, di.terminal, di.boss, di.terms, di.concepts);
    $$("[data-day-pct]").forEach(function (el) { el.textContent = m.pct + "%"; });
    setRing($("[data-day-ring]"), m.pct);
    var goal = $("[data-day-goal]");
    if (goal) {
      var items = [
        [m.qOK === m.qN, "Answer every quick check correctly (" + m.qOK + "/" + m.qN + ")"],
        [!m.hasBoss || m.boss, m.hasBoss ? "Defeat the boss challenge" : null],
        [m.checksDone === m.checks, "Tick every lab Check (" + m.checksDone + "/" + m.checks + ")"]
      ].filter(function (x) { return x[1]; });
      goal.innerHTML = "<h3>To earn the badge</h3><ul class=\"goal-list\">" + items.map(function (x) {
        return '<li class="' + (x[0] ? "met" : "") + '">' + icon(x[0] ? "check" : "circle") + "<span>" + esc(x[1]) + "</span></li>";
      }).join("") + "</ul>";
    }
    var bc = $("[data-badge-card]"), bs = $("[data-badge-state]");
    var done = !!S.done[dkey];
    if (bc) bc.classList.toggle("earned", done);
    if (bs) bs.textContent = done ? "Earned. Well done!" : "Complete the day to earn it.";
    $$(".hero-badge, .badge-card").forEach(function (el) { el.classList.toggle("earned", done); });
    // toc completion marks
    var tocLabs = $('[data-toc="labs"]'); if (tocLabs) tocLabs.classList.toggle("complete", m.checks > 0 && m.checksDone === m.checks);
    var tocQ = $('[data-toc="checks"]'); if (tocQ) tocQ.classList.toggle("complete", m.qN > 0 && m.qOK === m.qN);
    var tocB = $('[data-toc="boss"]'); if (tocB) tocB.classList.toggle("complete", m.boss);
    if (m.complete && !S.done[dkey]) {
      S.done[dkey] = true; save();
      award(dkey + "-complete", 150, "Day " + DAY + " complete");
      celebrate();
      refreshDay();
    }
    document.documentElement.setAttribute("data-js-ready", "1");
  }
  function celebrate() {
    confetti();
    var b = PAGE.badge || {};
    var medal = $(".hero-badge .badge-medal");
    var m = medal ? medal.outerHTML.replace("badge-medal", "badge-medal earned") : "";
    var next = PAGE.next ? '<a class="btn" href="day-' + ("0" + PAGE.next).slice(-2) + '.html">Start Day ' + PAGE.next + "</a>" : '<a class="btn" href="index.html">See your quest map</a>';
    var ov = h("div", { class: "celebrate", role: "dialog", "aria-modal": "true", "aria-label": "Day complete" },
      '<div class="celebrate-card">' + m + '<p class="eyebrow">Day ' + DAY + ' complete</p><h2>' + esc(b.name || "Badge earned") + "</h2><p>" + esc(b.desc || "") +
      '</p><p class="hero-actions" style="justify-content:center">' + next + '<button type="button" class="btn ghost" data-close>Stay here</button></p></div>');
    var opener = document.activeElement;
    document.body.appendChild(ov);
    var closeBtn = $("[data-close]", ov);
    var release = trapFocus(ov, $(".celebrate-card", ov), close);
    closeBtn.focus();
    function close() {
      if (!ov.parentNode) return;
      release(); ov.remove();
      if (opener && opener.focus) opener.focus();
    }
    closeBtn.addEventListener("click", close);
    ov.addEventListener("click", function (e) { if (e.target === ov) close(); });
  }

  /* ---------------- toc */
  function initToc() {
    var links = $$(".day-toc a");
    if (!links.length || !("IntersectionObserver" in window)) return;
    var map = {};
    links.forEach(function (a) { map[a.getAttribute("href").slice(1)] = a; });
    var obs = new IntersectionObserver(function (entries) {
      entries.forEach(function (en) {
        if (en.isIntersecting) {
          links.forEach(function (a) { a.classList.remove("active"); });
          var a = map[en.target.id]; if (a) { a.classList.add("active"); if (a.scrollIntoView && innerWidth < 1080) a.scrollIntoView({ block: "nearest", inline: "center" }); }
        }
      });
    }, { rootMargin: "-30% 0px -60% 0px" });
    Object.keys(map).forEach(function (id) { var s = document.getElementById(id); if (s) obs.observe(s); });
  }

  /* ---------------- briefing prereqs */
  function initPrereq() {
    $$("[data-prereq]").forEach(function (cb) {
      var id = cb.getAttribute("data-prereq");
      cb.checked = !!S.prereq[id];
      cb.addEventListener("change", function () { S.prereq[id] = cb.checked; save(); });
    });
  }

  /* ---------------- concept deck */
  function initDeck() {
    var deck = $("[data-deck]");
    if (!deck) return;
    var cards = $$(".card.concept", deck);
    if (!cards.length) return;
    deck.classList.add("js");
    var idx = 0;
    var seen = S.seen[dkey] || (S.seen[dkey] = {});
    var nav = h("div", { class: "deck-nav" });
    var prev = h("button", { type: "button", class: "btn ghost small", "aria-label": "Previous idea" }, "&larr; Back");
    var next = h("button", { type: "button", class: "btn small" }, "Next idea &rarr;");
    var dots = h("div", { class: "dots", role: "tablist", "aria-label": "Ideas" });
    cards.forEach(function (c, i) {
      var d = h("button", { type: "button", class: "dot", "aria-label": "Idea " + (i + 1), role: "tab" });
      d.addEventListener("click", function () { show(i); });
      dots.appendChild(d);
    });
    nav.appendChild(prev); nav.appendChild(dots); nav.appendChild(next);
    deck.appendChild(nav);
    function show(i) {
      idx = Math.max(0, Math.min(cards.length - 1, i));
      cards.forEach(function (c, j) { c.classList.toggle("show", j === idx); });
      $$(".dot", dots).forEach(function (d, j) {
        d.classList.toggle("on", j === idx); d.classList.toggle("seen", !!seen[j]); d.setAttribute("aria-selected", j === idx ? "true" : "false");
      });
      prev.disabled = idx === 0;
      next.innerHTML = idx === cards.length - 1 ? "All ideas seen &#10003;" : "Next idea &rarr;";
      var card = cards[idx], dg = $(".diagram", card);
      if (dg && !dg.dataset.ready) { renderDiagram(dg); dg.dataset.ready = "1"; }
      if (!seen[idx]) {
        seen[idx] = true; save();
        award(dkey + "-seen-" + idx, 3, null);
        refreshDay();
      }
    }
    prev.addEventListener("click", function () { show(idx - 1); });
    next.addEventListener("click", function () {
      if (idx === cards.length - 1) { var t = document.getElementById("terminal"); if (t) t.scrollIntoView({ behavior: "smooth" }); }
      else show(idx + 1);
    });
    deck.addEventListener("keydown", function (e) {
      var tag = (e.target.tagName || "").toLowerCase();
      if (tag === "input" || tag === "textarea") return;
      if (e.key === "ArrowRight" && !e.target.closest(".diagram")) { show(idx + 1); }
      if (e.key === "ArrowLeft" && !e.target.closest(".diagram")) { show(idx - 1); }
    });
    var start = 0;
    while (start < cards.length - 1 && seen[start]) start++;
    if (Object.keys(seen).length === cards.length) start = 0;
    if (location.hash && location.hash.indexOf("#c-") === 0) {
      cards.forEach(function (c, i) { if ("#" + c.id === location.hash) start = i; });
    }
    show(start);
    deckApi = { show: show, cards: cards };
  }
  var deckApi = null, quizGo = {};

  /* ============================================================ diagrams */
  function dgAward(el) {
    var key = dkey + "-dg-" + el.getAttribute("data-concept");
    award(key, 10, "explored a diagram");
  }
  function infoBox(el) {
    var box = h("div", { class: "dg-info", "aria-live": "polite" });
    el.appendChild(box);
    return function (part, extra) {
      var html = (part ? "<p><b>" + esc(part.name) + ".</b> " + esc(part.text) + "</p>" : "") + (extra || "") + (part ? citeHTML(part.cites) : "");
      var seen = {};
      box.innerHTML = html.replace(/<span class="cite">[\s\S]*?<\/span>/g, function (m) { if (seen[m]) return ""; seen[m] = 1; return m; });
    };
  }
  function renderDiagram(el) {
    var ci = +el.getAttribute("data-concept");
    var c = (PAGE.concepts || [])[ci];
    if (!c || !c.diagram) return;
    var spec = c.diagram, fn = DIAGRAMS[spec.type] || DIAGRAMS.flow;
    el.appendChild(h("p", { class: "dg-title" }, icon("target") + esc(spec.title || "Interactive diagram")));
    try { fn(el, spec); } catch (e) { el.appendChild(h("p", { class: "dg-help" }, "This diagram could not be drawn.")); if (window.console) console.error(e); }
  }
  var DIAGRAMS = {};

  DIAGRAMS.stack = function (el, spec) {
    var rows = (spec.data && spec.data.rows) || [];
    var parts = spec.parts || [];
    el.appendChild(h("p", { class: "dg-help" }, "Your request travels from top to bottom. Click each layer."));
    var wrap = h("div", { class: "stack" });
    var hues = [205, 262, 330, 28];
    var show = infoBox(el);
    rows.forEach(function (r, i) {
      if (i > 0) wrap.appendChild(h("div", { class: "stack-arrow", "aria-hidden": "true" }, "&#9660;"));
      var name = r[0].replace(/<[^>]+>/g, "");
      var b = h("button", { type: "button", class: "stack-layer", style: "--h:" + hues[i % 4] }, "<b>" + r[0] + "</b><span>" + r[1] + "</span>");
      b.addEventListener("click", function () {
        $$(".stack-layer", wrap).forEach(function (x) { x.classList.remove("on"); });
        b.classList.add("on");
        var p = parts.filter(function (x) { return x.name.toLowerCase() === name.toLowerCase(); })[0];
        show(null, "<p><b>" + esc(name) + "</b>: " + r[1] + "</p>" + (p ? "<p>On your host: <code>" + esc(p.text) + "</code></p>" + citeHTML(p.cites) : "") +
          citeHTML([spec.data.cite]));
        dgAward(el);
      });
      wrap.appendChild(b);
    });
    el.insertBefore(wrap, el.lastChild);
    show(null, "<p>Start with <b>virsh</b> at the top.</p>");
  };

  DIAGRAMS.flow = function (el, spec) {
    var parts = spec.parts || [];
    el.appendChild(h("p", { class: "dg-help" }, spec.play ? "Press play to follow the path, or click any step." : "Click any step."));
    var flow = h("div", { class: "flow" + (spec.layout === "column" ? " column" : "") });
    var nodes = [];
    var show = infoBox(el);
    parts.forEach(function (p, i) {
      if (i > 0) flow.appendChild(h("span", { class: "flow-arrow", "aria-hidden": "true" }, icon("arrow")));
      var b = h("button", { type: "button", class: "flow-node" }, esc(p.name));
      b.addEventListener("click", function () { select(i); dgAward(el); });
      nodes.push(b); flow.appendChild(b);
    });
    el.insertBefore(flow, el.lastChild);
    function select(i) {
      nodes.forEach(function (n, j) { n.classList.toggle("on", j === i); });
      show(parts[i]);
    }
    if (spec.play) {
      var tools = h("div", { class: "flow-tools" });
      var playing = false;
      var pb = h("button", { type: "button", class: "btn small" }, icon("play") + " Play");
      pb.addEventListener("click", function () {
        if (playing) return; playing = true; dgAward(el);
        var i = 0;
        (function step() {
          nodes.forEach(function (n) { n.classList.remove("hot"); });
          if (i >= nodes.length) { playing = false; return; }
          nodes[i].classList.add("hot"); select(i);
          i++; setTimeout(step, 1600);
        })();
      });
      tools.appendChild(pb);
      el.insertBefore(tools, el.lastChild);
    }
    if (spec.data && spec.data.rows) {
      var d = spec.data;
      var t = '<div class="table-wrap"><table><thead><tr>' + d.header.map(function (x) { return "<th>" + x + "</th>"; }).join("") +
        "</tr></thead><tbody>" + d.rows.map(function (r) { return "<tr>" + r.map(function (x) { return "<td>" + x + "</td>"; }).join("") + "</tr>"; }).join("") +
        "</tbody></table></div>";
      var det = h("details", { class: "dg-table" }, "<summary>" + esc(spec.table_title || "From the manual: the table behind this diagram") + "</summary>" + t + citeHTML([d.cite]));
      el.appendChild(det);
    }
    show(null, "<p>" + (spec.play ? "Press <b>Play</b>, or click a step." : "Click a step.") + "</p>");
  };

  DIAGRAMS.lifecycle = function (el, spec) {
    var P = {}; (spec.parts || []).forEach(function (p) { P[p.name] = p; });
    var rows = (spec.data && spec.data.rows) || [], plain = (spec.data && spec.data.plain) || [];
    function rowFor(op) {
      for (var i = 0; i < plain.length; i++) if (plain[i][0].toLowerCase().indexOf(op) === 0) return i;
      return -1;
    }
    var st = { defined: true, running: true, autostart: false }, count = 0;
    el.appendChild(h("p", { class: "dg-help" }, "This is the disposable profile-probe of 2.3, defined and running as 2.2 left it. Predict, then press an operation."));
    var wrap = h("div", { class: "lc" });
    var left = h("div", { class: "lc-state" }), right = h("div");
    var vm = h("div", { class: "lc-vm" });
    var facts = h("div", { class: "lc-facts" });
    left.appendChild(vm); left.appendChild(facts);
    var ops = h("div", { class: "lc-ops", role: "group", "aria-label": "virsh operations" });
    var names = [["define", "define probe.xml"], ["start", "start"], ["shutdown", "shutdown"], ["destroy", "destroy"], ["undefine", "undefine --keep-nvram"], ["autostart", "autostart"]];
    var btns = {};
    names.forEach(function (n) {
      var b = h("button", { type: "button", class: "btn ghost small" }, esc(n[1]));
      b.addEventListener("click", function () { if (predictOn) ask(n[0], n[1]); else act(n[0]); });
      btns[n[0]] = b; ops.appendChild(b);
    });
    right.appendChild(ops);
    /* Predict first: guess the QEMU process and the saved definition before the operation runs. */
    var predictOn = false;
    var ptoggle = h("label", { class: "tick tick-small lc-predict" }, '<input type="checkbox"><span class="tick-box">' + icon("check") + "</span><span>Predict first (2 XP per right prediction)</span>");
    right.appendChild(ptoggle);
    $("input", ptoggle).addEventListener("change", function (e) { predictOn = e.target.checked; pbox.hidden = true; });
    var pbox = h("div", { class: "lc-ask", hidden: "" });
    right.appendChild(pbox);
    function outcome(op) {
      var d = st.defined, r = st.running;
      if (op === "define") d = true;
      else if (op === "start") r = true;
      else if (op === "shutdown" || op === "destroy") r = false;
      else if (op === "undefine") d = false;
      return { running: r, defined: d };
    }
    function ask(op, label) {
      var exp = outcome(op);
      pbox.hidden = false;
      pbox.innerHTML = "<p><b>Before <code>" + esc(label) + "</code>:</b> what will be true afterwards?</p>" +
        '<p class="lc-q">QEMU process: <button type="button" class="btn ghost small" data-k="r" data-v="1">running</button> <button type="button" class="btn ghost small" data-k="r" data-v="0">none</button></p>' +
        '<p class="lc-q">Saved definition: <button type="button" class="btn ghost small" data-k="d" data-v="1">exists</button> <button type="button" class="btn ghost small" data-k="d" data-v="0">gone</button></p>' +
        '<p><button type="button" class="btn small" data-go disabled>Run it</button></p>';
      var guess = {};
      $$("button[data-k]", pbox).forEach(function (b) {
        b.addEventListener("click", function () {
          guess[b.getAttribute("data-k")] = b.getAttribute("data-v") === "1";
          $$('button[data-k="' + b.getAttribute("data-k") + '"]', pbox).forEach(function (x) { x.classList.toggle("on", x === b); x.setAttribute("aria-pressed", x === b ? "true" : "false"); });
          $("[data-go]", pbox).disabled = !("r" in guess && "d" in guess);
        });
      });
      $("[data-go]", pbox).addEventListener("click", function () {
        var right2 = (guess.r === exp.running ? 1 : 0) + (guess.d === exp.defined ? 1 : 0);
        act(op);
        pbox.innerHTML = "<p>" + (right2 === 2 ? icon("check") + " <b>Both predictions right.</b>" : "<b>" + right2 + " of 2 right.</b> Compare with the row of the 2.3 table below.") + "</p>";
        if (right2) award(dkey + "-lc-" + op + "-" + (exp.running ? "r" : "n") + (exp.defined ? "d" : "g"), 2 * right2, null);
      });
      $("button[data-k]", pbox).focus();
    }
    var rowBox = h("div", { class: "lc-row" });
    right.appendChild(rowBox);
    var log = h("div", { class: "lc-log", "aria-live": "polite" });
    right.appendChild(log);
    wrap.appendChild(left); wrap.appendChild(right);
    el.appendChild(wrap);
    var show = infoBox(el);
    function draw() {
      var gone = !st.defined && !st.running;
      var transient = st.running && !st.defined;
      vm.className = "lc-vm" + (st.running ? (transient ? " transient" : " running") : "") + (gone ? " gone" : "");
      vm.innerHTML = '<div class="lc-name">profile-probe</div><div class="lc-big">' +
        (gone ? "does not exist" : transient ? "running (transient)" : st.running ? "running" : "shut off") + "</div>" +
        '<div class="fine">' + (st.autostart ? "autostart: enable" : "autostart: disable") + "</div>";
      facts.innerHTML = "<div>QEMU process<b>" + (st.running ? "running" : "none") + "</b></div><div>Saved definition<b>" + (st.defined ? "yes" : "no") +
        "</b></div><div>Disk data<b>kept</b></div>";
      btns.start.disabled = !st.defined || st.running;
      btns.shutdown.disabled = !st.running; btns.destroy.disabled = !st.running;
      btns.undefine.disabled = !st.defined; btns.define.disabled = false;
      btns.autostart.disabled = !st.defined;
      btns.autostart.textContent = st.autostart ? "autostart --disable" : "autostart";
    }
    function showRow(op) {
      var i = rowFor(op);
      if (i < 0) { rowBox.innerHTML = ""; return; }
      rowBox.innerHTML = '<div class="table-wrap"><table><thead><tr>' + spec.data.header.map(function (x) { return "<th>" + x + "</th>"; }).join("") +
        "</tr></thead><tbody><tr>" + rows[i].map(function (x) { return "<td>" + x + "</td>"; }).join("") + "</tr></tbody></table></div>" + citeHTML([spec.data.cite]);
    }
    function act(op) {
      dgAward(el);
      var msg = "";
      if (op === "define") {
        var was = st.defined, run = st.running;
        st.defined = true; showRow("define");
        if (was) { show(P.define); msg = "define: saved definition updated; " + (run ? "the running VM is unchanged until its next start" : "the next start uses it"); }
        else if (run) { show(P.define); msg = "define: the running transient VM is persistent again"; }
        else { show(P.restore || P.define); msg = "define: the definition is saved again"; }
      }
      else if (op === "start") { st.running = true; showRow("start"); show(P.start); msg = "start: QEMU process starts"; }
      else if (op === "shutdown" || op === "destroy") {
        var wasTransient = !st.defined;
        st.running = false; showRow(op);
        show(wasTransient ? P.transient : P[op]);
        msg = op + (wasTransient ? ": the transient VM is gone" : ": shut off, definition kept");
      } else if (op === "undefine") {
        st.defined = false; st.autostart = false;
        if (st.running) { showRow("undefine while running"); show(P.undefine); msg = "undefine: still running, now transient"; }
        else { rowBox.innerHTML = ""; show(null, "<p><b>undefine.</b> Nothing was running, so the VM no longer exists. The disk and, with --keep-nvram, the NVRAM file stay; define probe.xml brings it back.</p>" + citeHTML((P.undefine || {}).cites)); msg = "undefine: no definition left"; }
      } else if (op === "autostart") {
        st.autostart = !st.autostart; showRow("autostart"); show(P.autostart); msg = st.autostart ? "autostart enabled" : "autostart disabled";
      }
      log.insertBefore(h("div", { text: (++count) + ". " + (op === "undefine" ? "undefine --keep-nvram" : op) + ": " + msg.replace(/^[a-z]+: /, "") }), log.firstChild);
      draw();
    }
    draw();
    show(null, "<p>The VM is defined and running, as 2.2 left it. Try <code>autostart</code>, <code>shutdown</code>, <code>start</code>, then <code>undefine --keep-nvram</code> while it runs, then <code>destroy</code>.</p>");
  };

  DIAGRAMS.chain = function (el, spec) {
    /* Follows chapter 4: 4.2 writes land in the top file; 4.3 stops the guest, converts the whole chain, then hides the base;
       4.5 snapshot, blockcommit --base, blockpull. qemu-img never touches a running guest's chain. */
    var P = {}; (spec.parts || []).forEach(function (p) { P[p.name] = p; });
    el.appendChild(h("p", { class: "dg-help" }, "clone-01 from chapter 4.2. The top file takes the writes; each arrow points from a file to its backing file. Try the operations in the order of 4.3 and 4.5."));
    var st;
    function reset() {
      st = { files: [{ n: "private-base.qcow2", data: 6, base: true }, { n: "clone-01.qcow2", data: 0 }], running: true, hidden: false, copy: null };
    }
    reset();
    var wrap = h("div", { class: "chain-wrap" });
    var col = h("div"), side = h("div");
    var chainEl = h("div", { class: "chain-col" });
    var guest = h("div", { class: "chain-side" });
    col.appendChild(chainEl); col.appendChild(guest);
    var ops = h("div", { class: "chain-ops", role: "group", "aria-label": "Chain operations" });
    var B = {};
    [["write", "Guest writes a file"], ["snapshot", "snapshot-create-as --disk-only"], ["commit", "blockcommit --base clone-01.qcow2 --pivot"],
      ["pull", "blockpull"], ["stop", "Stop clone-01"], ["start", "virsh start clone-01"], ["hide", "Hide the base (mv)"],
      ["convert", "qemu-img convert to restore-01.qcow2"], ["reset", "Reset"]].forEach(function (o) {
      var b = h("button", { type: "button", class: "btn ghost small" }, esc(o[1]));
      b.addEventListener("click", function () { op(o[0]); });
      B[o[0]] = b; ops.appendChild(b);
    });
    side.appendChild(ops);
    /* Predict first: before a write, pick the file that will take it (always the top file, 4.2). */
    var cpredict = false, pgen = 0;
    function dropPrediction() { pgen++; cask.hidden = true; cask.innerHTML = ""; }
    var ctoggle = h("label", { class: "tick tick-small lc-predict" }, '<input type="checkbox"><span class="tick-box">' + icon("check") + "</span><span>Predict first: which file takes the next write?</span>");
    side.appendChild(ctoggle);
    $("input", ctoggle).addEventListener("change", function (e) { cpredict = e.target.checked; dropPrediction(); });
    var cask = h("div", { class: "lc-ask", hidden: "" });
    side.appendChild(cask);
    var copyEl = h("div", { class: "chain-side" });
    side.appendChild(copyEl);
    wrap.appendChild(col); wrap.appendChild(side);
    el.appendChild(wrap);
    var show = infoBox(el);
    function askWrite() {
      var g = ++pgen, chainKey = st.files.map(function (f) { return f.n; }).join("|");
      cask.hidden = false;
      cask.innerHTML = "<p><b>The guest writes a file.</b> Which file stores the new data?</p><p>" + st.files.slice().reverse().map(function (f, i) {
        return '<button type="button" class="btn ghost small" data-f="' + (st.files.length - 1 - i) + '">' + esc(f.n) + "</button>";
      }).join(" ") + "</p>";
      $$("button[data-f]", cask).forEach(function (b) {
        b.addEventListener("click", function () {
          /* reject answers to a prediction that a later operation made stale */
          if (g !== pgen || !st.running || st.files.map(function (f) { return f.n; }).join("|") !== chainKey) { dropPrediction(); return; }
          var right = +b.getAttribute("data-f") === st.files.length - 1;
          cask.innerHTML = "<p>" + (right ? icon("check") + " <b>Right: the top file takes every write.</b>" : "<b>Not that one:</b> writes always land in the top file; the files below are only read.") + "</p>";
          if (right) award(dkey + "-chain-write-" + st.files.length, 2, null);
          doWrite();
        });
      });
      $("button[data-f]", cask).focus();
    }
    function doWrite() {
      if (!st.running) return;
      var top = st.files[st.files.length - 1]; top.mine = (top.mine || 0) + 1; draw(st.files.length - 1); show(P.write);
    }
    function dots(f) {
      var s2 = "";
      for (var i = 0; i < f.data; i++) s2 += "<i" + ((f.base || f.pulled) ? ' class="base-data"' : "") + "></i>";
      for (var j = 0; j < (f.mine || 0); j++) s2 += "<i></i>";
      return s2;
    }
    function hasTop() { return st.files[st.files.length - 1].n.indexOf("-top") >= 0; }
    function draw(flashIdx) {
      chainEl.innerHTML = "";
      st.files.forEach(function (f, i) {
        if (i > 0) chainEl.appendChild(h("div", { class: "chain-link", "aria-hidden": "true" }, "&#8595; backing file"));
        var top = i === st.files.length - 1;
        var d = h("div", { class: "chain-file" + (top ? " top" : "") + (f.base ? " base" : "") + (i === flashIdx ? " flash" : "") },
          "<span>" + esc(f.n) + (f.base && st.hidden ? " <em>(hidden)</em>" : "") + (top ? " <b>&#9998; top</b>" : "") + '</span><span class="chain-dots">' + dots(f) + "</span>");
        chainEl.appendChild(d);
      });
      var dependsOnBase = st.files[0].base;
      guest.innerHTML = '<span class="chain-guest' + (st.running ? "" : " off") + '">clone-01: ' + (st.running ? "running" : "shut off") + "</span>" +
        (dependsOnBase && st.hidden ? ' <span class="chain-guest broken">its base is hidden: it cannot start</span>' : "");
      copyEl.innerHTML = st.copy ? '<div class="chain-file"><span>' + esc(st.copy) + ' <em>standalone, no backing file</em></span><span class="chain-dots"><i class="base-data"></i><i></i><i></i></span></div>' : "";
      B.write.disabled = !st.running;
      B.snapshot.disabled = !st.running || hasTop();
      B.commit.disabled = !st.running || !hasTop();
      B.pull.disabled = !st.running || !dependsOnBase || hasTop();
      B.stop.disabled = !st.running;
      B.start.disabled = st.running;
      B.hide.disabled = !dependsOnBase || st.running;
      B.hide.textContent = st.hidden ? "Put the base back (mv)" : "Hide the base (mv)";
      B.convert.disabled = !!st.copy || st.running || (dependsOnBase && st.hidden);
    }
    function op(o) {
      dgAward(el);
      if (o !== "write") dropPrediction();
      var top = st.files[st.files.length - 1];
      var dependsOnBase = st.files[0].base;
      if (o === "reset") { reset(); draw(); show(null, "<p>Back to the start: clone-01.qcow2 on private-base.qcow2, with clone-01 running.</p>"); return; }
      if (o === "write") { if (!st.running) return; if (cpredict) askWrite(); else doWrite(); return; }
      if (o === "snapshot") { if (!st.running || hasTop()) return; st.files.push({ n: "clone-01-top.qcow2", data: 0 }); draw(st.files.length - 1); show(P.snapshot); return; }
      if (o === "commit") {
        if (!st.running || !hasTop()) return;
        var below = st.files[st.files.length - 2];
        below.mine = (below.mine || 0) + (top.mine || 0); st.files.pop(); draw(st.files.length - 1); show(P.commit); return;
      }
      if (o === "pull") {
        if (!st.running || !dependsOnBase || hasTop()) return;
        var base = st.files[0], mine = st.files[1];
        mine.data = base.data; mine.pulled = true;
        st.files.shift(); st.hidden = false;
        draw(0); show(P.pull); return;
      }
      if (o === "stop") { st.running = false; draw(); show(null, "<p>clone-01 is shut off, so qemu-img may now read its chain.</p>"); return; }
      if (o === "start") {
        if (dependsOnBase && st.hidden) { draw(); show(P.broken); return; }
        st.running = true; draw(); show(null, "<p>clone-01 runs again" + (dependsOnBase ? "." : ", now without any backing file (4.5).") + "</p>"); return;
      }
      if (o === "hide") {
        if (st.running || !dependsOnBase) return;
        st.hidden = !st.hidden; draw();
        if (st.hidden) show(P.broken, st.copy ? "<p>restore-01.qcow2 has no backing file, so it still boots on its own.</p>" : "");
        else show(P.start || null, P.start ? "" : "<p>The base is back; start clone-01 again.</p>");
        return;
      }
      if (o === "convert") {
        if (st.copy || st.running || (dependsOnBase && st.hidden)) return;
        st.copy = "restore-01.qcow2"; draw(); show(P.convert);
      }
    }
    draw();
    show(P.chain);
  };

  DIAGRAMS.identity = function (el, spec) {
    var P = {}; (spec.parts || []).forEach(function (p) { P[p.name] = p; });
    el.appendChild(h("p", { class: "dg-help" }, "Make two guests from one booted disk. Values are symbolic (A, B), not real output."));
    var modes = h("div", { class: "idg-modes" });
    var pipe = h("div", { class: "idg-pipe", "aria-hidden": "true" });
    var guests = h("div", { class: "idg-guests" });
    var b1 = h("button", { type: "button", class: "btn ghost small" }, "Copy the booted disk as it is");
    var b2 = h("button", { type: "button", class: "btn small" }, "Generalize first, then provision");
    modes.appendChild(b1); modes.appendChild(b2);
    el.appendChild(modes); el.appendChild(pipe); el.appendChild(guests);
    var show = infoBox(el);
    function card(name, vals, dupFlags) {
      var rowsH = [["hostname", vals[0]], ["machine ID", vals[1]], ["SSH host key", vals[2]], ["instance ID", vals[3]]].map(function (r, i) {
        return "<dt>" + r[0] + '</dt><dd class="' + (dupFlags[i] ? "dup" : "uniq") + '">' + esc(r[1]) + "</dd>";
      }).join("");
      return '<div class="idg-card"><h4>' + esc(name) + "</h4><dl>" + rowsH + "</dl></div>";
    }
    function copyMode() {
      dgAward(el);
      pipe.innerHTML = "<span>booted disk</span>&rarr;<span>copy</span>&rarr;<span>copy</span>";
      guests.innerHTML = card("guest 1", ["template-01", "ID A", "key A", "instance A"], [1, 1, 1, 1]) + card("guest 2", ["template-01", "ID A", "key A", "instance A"], [1, 1, 1, 1]);
      show(P.identity, "<p>Both copies carry the same identity.</p>" + (P.network ? "<p>" + esc(P.network.text) + "</p>" + citeHTML(P.network.cites) : ""));
    }
    function genMode() {
      dgAward(el);
      pipe.innerHTML = '<span class="on">cloud-init clean</span>&rarr;<span class="on">qemu-img convert</span>&rarr;<span class="on">virt-sysprep</span>&rarr;<span class="on">new seed + new UUID each</span>';
      guests.innerHTML = card("linux-02", ["linux-02", "UUID of linux-02", "key B", "instance B"], [0, 0, 0, 0]) + card("clone-01", ["clone-01", "UUID of clone-01", "key C", "instance C"], [0, 0, 0, 0]);
      show(P["virt-sysprep"], "<p>" + esc(P["cloud-init clean"] ? P["cloud-init clean"].text : "") + " " + esc(P["new seed"] ? P["new seed"].text : "") + " " +
        esc(P["machine ID"] ? P["machine ID"].text : "") + "</p>" + citeHTML((P["new seed"] || {}).cites));
    }
    b1.addEventListener("click", copyMode); b2.addEventListener("click", genMode);
    show(P.identity);
  };

  DIAGRAMS.migration = function (el, spec) {
    /* Pre-copy as 8.4 describes it: copy all memory while the guest runs, re-copy the changed pages round after round,
       and pause only when the pages left can be sent within the allowed pause (300 ms by default). A guest that changes
       memory faster than the network can carry may not get there; virsh domjobabort cancels and it stays on the source. */
    var P = {}; (spec.parts || []).forEach(function (p) { P[p.name] = p; });
    el.appendChild(h("p", { class: "dg-help" }, "A schematic of pre-copy. Here sending one page takes 75 ms, so the guest pauses once 4 pages or fewer are left (4 × 75 ms = 300 ms, the default allowed pause)."));
    var N = 48, MS = 75, LIMIT = 300;
    var wrap = h("div", { class: "mig" });
    var hosts = h("div", { class: "mig-hosts" });
    var src = h("div", { class: "mig-host" }, '<h4>Source <span class="mig-state run" data-s>running</span></h4><div class="mig-grid" data-g></div>');
    var pipeEl = h("div", { class: "mig-pipe" }, '<div>memory</div><div class="bar"><span></span></div><div data-r>idle</div>');
    var dst = h("div", { class: "mig-host" }, '<h4>Destination <span class="mig-state" data-s>waiting</span></h4><div class="mig-grid" data-g></div>');
    hosts.appendChild(src); hosts.appendChild(pipeEl); hosts.appendChild(dst);
    var disk = h("div", { class: "mig-disk" }, "Shared disk on nfs-01 (NFS): both hosts open the same file");
    hosts.appendChild(disk);
    wrap.appendChild(hosts);
    wrap.appendChild(h("div", { class: "mig-legend" }, '<span><i style="background:hsl(205 70% 55%)"></i>page</span><span><i style="background:hsl(28 90% 55%)"></i>changed since copied</span><span><i style="background:hsl(150 55% 45%)"></i>copied</span>'));
    var status = h("p", { class: "mig-round", "aria-live": "polite" }, "Press Migrate.");
    wrap.appendChild(status);
    var tools = h("div", { class: "flow-tools" });
    var play = h("button", { type: "button", class: "btn small" }, icon("play") + " Migrate");
    var storage = h("label", { class: "tick tick-small" }, '<input type="checkbox"><span class="tick-box">' + icon("check") + "</span><span>--copy-storage-all (no shared disk)</span>");
    var hot = h("label", { class: "tick tick-small" }, '<input type="checkbox"><span class="tick-box">' + icon("check") + "</span><span>Busy guest: changes memory faster than the network can carry</span>");
    tools.appendChild(play); tools.appendChild(storage); tools.appendChild(hot);
    wrap.appendChild(tools);
    el.appendChild(wrap);
    var show = infoBox(el);
    var sg = $("[data-g]", src), dg = $("[data-g]", dst), ss = $("[data-s]", src), ds = $("[data-s]", dst), rr = $("[data-r]", pipeEl);
    var cb = $("input", storage), hb = $("input", hot);
    function grid() {
      sg.innerHTML = ""; dg.innerHTML = "";
      for (var i = 0; i < N; i++) { sg.appendChild(h("i", { class: "p" })); dg.appendChild(h("i")); }
    }
    grid();
    cb.addEventListener("change", function () {
      if (busy) return;
      disk.textContent = cb.checked ? "Disks are not shared: --copy-storage-all copies the disk first" : "Shared disk on nfs-01 (NFS): both hosts open the same file";
      show(cb.checked ? P.storage : P.disk, !cb.checked && P.shared ? "<p>" + esc(P.shared.text) + "</p>" + citeHTML(P.shared.cites) : "");
    });
    function partHTML(p) { return p ? "<p>" + esc(p.text) + "</p>" + citeHTML(p.cites) : ""; }
    var busy = false;
    play.addEventListener("click", function () {
      if (busy) return; busy = true; dgAward(el); grid();
      var S1 = $$("i", sg), D1 = $$("i", dg);
      /* the options are fixed for the whole run; the controls stay disabled until it ends */
      var copied = {}, fast = hb.checked, copyStorage = cb.checked;
      play.disabled = true; cb.disabled = true; hb.disabled = true;
      disk.textContent = copyStorage ? "Disks are not shared: --copy-storage-all copies the disk first" : "Shared disk on nfs-01 (NFS): both hosts open the same file";
      function unlock() { busy = false; play.disabled = false; cb.disabled = false; hb.disabled = false; }
      ss.textContent = "running"; ss.className = "mig-state run"; ds.textContent = "receiving"; ds.className = "mig-state";
      pipeEl.classList.add("active");
      function round(r, pages, next) {
        rr.textContent = "round " + r;
        status.textContent = "Round " + r + ": sending " + pages.length + " pages while the guest keeps running.";
        show(r === 1 ? P.copy : P.recopy);
        var k = 0, newDirty = {};
        var tick = setInterval(function () {
          for (var c = 0; c < 4 && k < pages.length; c++, k++) {
            var p = pages[k]; copied[p] = true; delete newDirty[p]; S1[p].className = "p"; D1[p].className = "c";
          }
          /* a normal guest changes fewer pages than were sent; a busy one changes more */
          var tries = fast ? 6 : 2, cap = fast ? N : Math.floor(pages.length * 0.4);
          for (var j = 0; j < tries; j++) {
            if (Object.keys(newDirty).length >= cap) break;
            if (fast || Math.random() < 0.8) {
              var q = Math.floor(Math.random() * N);
              if (copied[q]) { newDirty[q] = true; S1[q].className = "d"; D1[q].className = ""; }
            }
          }
          if (k >= pages.length) { clearInterval(tick); next(Object.keys(newDirty).map(Number)); }
        }, 120);
      }
      function finish() {
        ss.textContent = "paused"; ss.className = "mig-state pause"; rr.textContent = "last pages + device state";
        show(P.pause, copyStorage ? "" : partHTML(P.lock));
        setTimeout(function () {
          for (var i = 0; i < N; i++) { D1[i].className = "c"; S1[i].className = ""; }
          ss.textContent = "gone (Stopped Migrated)"; ss.className = "mig-state"; ds.textContent = "running (migrated)"; ds.className = "mig-state run";
          if (copyStorage) disk.textContent = "The source still holds the old disk, now stale: delete it";
          rr.textContent = "done"; pipeEl.classList.remove("active");
          status.textContent = "Switched over: the guest runs on the destination.";
          show(P.events, partHTML(P.after) + (copyStorage ? partHTML(P.stale) : "") + partHTML(P.stream));
          unlock();
        }, 1300);
      }
      function abort() {
        rr.textContent = "domjobabort"; pipeEl.classList.remove("active");
        for (var i = 0; i < N; i++) D1[i].className = "";
        ss.textContent = "running"; ss.className = "mig-state run"; ds.textContent = "cancelled"; ds.className = "mig-state";
        status.textContent = "Not converging here: the pages left do not fit the 300 ms pause, and a busy guest may never finish (it can also still converge). virsh -c \"$B\" domjobabort linux-01, sent to the source from a second terminal (after . ~/kvm-course/ch08/env), cancels the move, and the guest keeps running there. (Not validated in the manual: idle linux-01 always converges.)";
        show(P.converge || P.recopy);
        unlock();
      }
      function check(r, left) {
        var ms = left.length * MS;
        status.textContent = "After round " + r + ": " + left.length + " pages left, about " + ms + " ms to send. Allowed pause: " + LIMIT + " ms.";
        setTimeout(function () {
          if (ms <= LIMIT) return finish();
          if (r >= (fast ? 4 : 8)) return abort();
          round(r + 1, left, function (d) { check(r + 1, d); });
        }, 900);
      }
      function begin() {
        var all = []; for (var i = 0; i < N; i++) all.push(i);
        round(1, all, function (d) { check(1, d); });
      }
      if (copyStorage) { rr.textContent = "copying disk"; status.textContent = "Copying the disk first (--copy-storage-all)."; show(P.storage); setTimeout(begin, 2200); }
      else begin();
    });
    show(P.copy, "<p>Press <b>Migrate</b>. Then tick <b>Busy guest</b> and press it again.</p>");
  };

  DIAGRAMS.kvmrun = function (el, spec) {
    var P = {}; (spec.parts || []).forEach(function (p) { P[p.name] = p; });
    var code = (spec.code_data && spec.code_data.items) || [];
    var out = (spec.data && spec.data.lines) || [];
    var exits = spec.exits || [];
    el.appendChild(h("p", { class: "dg-help" }, "The 16-byte guest of chapter 11, as printed in tiny-vmm.c. Press Run to call KVM_RUN once: the guest runs until the next exit that reaches your VMM (KVM handles others silently; Day 10 counts them)."));
    var wrap = h("div", { class: "kr" });
    var codeEl = h("div", { class: "kr-code", role: "list", "aria-label": "Guest program" });
    var addr = 0x1000;
    var lines = code.map(function (line) {
      var m = line.match(/^([^/]*)\/\*\s*(.*?)\s*\*\//);
      var bytes = m ? m[1].replace(/,\s*$/, "").trim() : line;
      var n = bytes.split(",").filter(function (x) { return x.trim(); }).length;
      var row = h("div", { class: "kr-line", role: "listitem" }, '<span class="addr">' + addr.toString(16) + "</span><span>" + esc(m ? m[2] : line) + "</span>");
      row.title = bytes;
      addr += n;
      codeEl.appendChild(row);
      return row;
    });
    var side = h("div", { class: "kr-side" });
    var loop = h("div", { class: "kr-loop", "aria-hidden": "true" }, '<span data-k="run">ioctl(KVM_RUN)</span>&rarr;<span data-k="guest">guest runs on the CPU</span>&rarr;<span data-k="exit">exit_reason</span>&rarr;<span data-k="vmm">VMM handles it</span>');
    var outEl = h("div", { class: "kr-out", "aria-live": "polite" });
    var tools = h("div", { class: "flow-tools" });
    var runB = h("button", { type: "button", class: "btn small" }, icon("play") + " Run (KVM_RUN)");
    var resetB = h("button", { type: "button", class: "btn ghost small" }, icon("reset") + " Reset");
    tools.appendChild(runB); tools.appendChild(resetB);
    side.appendChild(loop); side.appendChild(tools); side.appendChild(outEl);
    wrap.appendChild(codeEl); wrap.appendChild(side);
    el.appendChild(wrap);
    var show = infoBox(el);
    /* busy blocks overlapping runs; gen invalidates callbacks scheduled before a Reset. */
    var k = 0, pc = 0, busy = false, gen = 0;
    function hi(key) { $$("span", loop).forEach(function (s) { s.classList.toggle("on", s.getAttribute("data-k") === key); }); }
    function draw() {
      lines.forEach(function (l, i) {
        l.classList.toggle("done", i < pc);
        l.classList.toggle("cur", i === pc && k < exits.length);
        l.classList.toggle("exit", exits.slice(0, k).indexOf(i) >= 0);
      });
      runB.disabled = busy || k >= exits.length;
    }
    function reset() { gen++; busy = false; k = 0; pc = 0; outEl.textContent = "$ ./tiny-vmm\n"; draw(); hi("run"); show(P.KVM_RUN); }
    runB.addEventListener("click", function () {
      if (busy || k >= exits.length) return;
      dgAward(el);
      busy = true;
      var g = gen, idx = k;
      hi("guest");
      var target = exits[idx];
      draw();
      setTimeout(function () {
        if (g !== gen) return;
        pc = target; draw(); hi("exit");
        setTimeout(function () {
          if (g !== gen) return;
          var line = out[idx] || "";
          outEl.textContent += line + "\n";
          var part = /KVM_EXIT_IO\s+in/.test(line) ? P["KVM_EXIT_IO in"] : /KVM_EXIT_IO\s+out/.test(line) ? P["KVM_EXIT_IO out"] :
            /MMIO/.test(line) ? P.KVM_EXIT_MMIO : /HLT/.test(line) ? P.KVM_EXIT_HLT : P.KVM_RUN;
          show(part, k === exits.length - 1 && P["in-kernel"] ? "<p>" + esc(P["in-kernel"].text) + "</p>" + citeHTML(P["in-kernel"].cites) +
            citeHTML([spec.data.cite]) : citeHTML([spec.data.cite]));
          k = idx + 1;
          /* An IN finishes only on the next KVM_RUN (11.4), so the program counter stays on it until then; HLT ends the program. */
          pc = /KVM_EXIT_IO\s+in/.test(line) ? target : target + 1;
          busy = false;
          hi(k >= exits.length ? "exit" : "vmm");
          draw();
        }, 450);
      }, 450);
    });
    resetB.addEventListener("click", reset);
    reset();
    /* What if: change the IN answer and RBX, predict exit 2's character, compare with the real 11.4 line. */
    var real = { e1: out[0] || "", e2: out[1] || "", e3: out[2] || "" };
    var wi = h("div", { class: "kr-whatif" });
    wi.innerHTML = "<h4>What if? Change the VMM's numbers</h4>" +
      '<p class="fine">In tiny-vmm.c the VMM answers the IN with 2 (<code>*data = 2</code>, and its printf says "answers 2"), and sets RBX to 3. Change them, predict, then compare with the real line from 11.4.</p>' +
      '<div class="kr-inputs"><label>IN answer <input type="number" min="0" max="9" value="2" data-wi="in"></label>' +
      '<label>RBX <input type="number" min="0" max="9" value="3" data-wi="rbx"></label>' +
      '<label>Your prediction for exit 2\'s character <input type="text" maxlength="1" size="2" data-wi="guess" aria-describedby="kr-wi-help"></label>' +
      '<button type="button" class="btn small" data-wi="go">Check</button></div>' +
      '<p class="fine" id="kr-wi-help">One character: what does <code>out dx, al</code> write after <code>in</code>, <code>add al, bl</code> and <code>add al, \'0\'</code>?</p>' +
      '<div class="kr-out kr-wi-out" aria-live="polite"></div><div class="kr-out kr-wi-rec"></div>';
    side.appendChild(wi);
    function clamp(v) { v = parseInt(v, 10); return isNaN(v) ? 0 : Math.max(0, Math.min(9, v)); }
    $$("input", wi).forEach(function (inp) {
      inp.addEventListener("keydown", function (e) { if (e.key === "Enter") { e.preventDefault(); $('[data-wi="go"]', wi).click(); } });
    });
    $('[data-wi="go"]', wi).addEventListener("click", function () {
      dgAward(el);
      var a = clamp($('[data-wi="in"]', wi).value), b = clamp($('[data-wi="rbx"]', wi).value);
      $('[data-wi="in"]', wi).value = a; $('[data-wi="rbx"]', wi).value = b;
      var code = (0x30 + a + b) & 0xff, ch = String.fromCharCode(code);
      var guess = $('[data-wi="guess"]', wi).value;
      var ok = guess === ch;
      if (ok) award(dkey + "-kr-wi-" + a + "-" + b, 2, null);
      $(".kr-wi-out", wi).textContent =
        (guess ? (ok ? "Prediction right. " : "Prediction '" + guess + "' is not it. ") : "") +
        "Schematic: al = " + a + " + " + b + " = " + (a + b) + "; add al, '0' gives 0x" + code.toString(16) + " = '" + ch + "'" +
        (a + b > 9 ? " (above 9 it is no longer a decimal digit)" : "") +
        ". out dx, al writes that character, and the store to 0x5000 carries the same byte.";
      $(".kr-wi-rec", wi).textContent = "Recorded in 11.4 with IN 2 and RBX 3 (unchanged):\n" + out.join("\n");
      var add = "";
      ["whatif-in", "whatif-rbx", "whatif-digit"].forEach(function (k) { if (P[k]) add += "<p>" + esc(P[k].text) + "</p>" + citeHTML(P[k].cites); });
      show(null, add);
    });
  };

  /* Day 6: block grid for full + checkpoint, guest writes, incremental, restore (7.4, 7.5). Schematic: 12 blocks. */
  DIAGRAMS.incgrid = function (el, spec) {
    var P = {}; (spec.parts || []).forEach(function (p) { P[p.name] = p; });
    var N = 12, CHANGED = [2, 5, 9];   /* the blocks that "replace a.bin, delete b.bin, add d.bin" change (schematic) */
    el.appendChild(h("p", { class: "dg-help" }, "A schematic disk of 12 blocks. Work through chapter 7.4 and 7.5 in order. Letters show which version of a block each file holds."));
    var st;
    function reset() { st = { disk: [], bitmap: null, full: null, inc: null, restored: null, lost: false, otherFull: null, stopped: false, needFull: false }; for (var i = 0; i < N; i++) st.disk.push("A"); }
    reset();
    var rows = h("div", { class: "ig-rows" });
    var tools = h("div", { class: "chain-ops", role: "group", "aria-label": "Backup steps" });
    var B = {};
    [["full", "1. Full backup + checkpoint (7.4)"], ["writes", "2. Guest writes: a.bin, b.bin, d.bin"], ["inc", "3. Incremental backup (7.5)"],
      ["loss", "4. Lose the data (rm -r data)"], ["restore", "5. Stop, rebase -u onto its full, convert"], ["wrong", "5'. Rebase onto a different full instead"],
      ["next", "6. Next backup: incremental again?"], ["reset", "Reset"]].forEach(function (o) {
      var b = h("button", { type: "button", class: "btn ghost small" }, esc(o[1]));
      b.addEventListener("click", function () { act(o[0]); });
      B[o[0]] = b; tools.appendChild(b);
    });
    var predict = h("label", { class: "tick tick-small lc-predict" }, '<input type="checkbox"><span class="tick-box">' + icon("check") + "</span><span>Predict first</span>");
    var ask = h("div", { class: "lc-ask", hidden: "" });
    el.appendChild(rows); el.appendChild(tools); el.appendChild(predict); el.appendChild(ask);
    var show = infoBox(el);
    var pon = false, agen = 0;
    $("input", predict).addEventListener("change", function (e) { pon = e.target.checked; agen++; ask.hidden = true; });
    function row(label, blocks, note) {
      var cells = "";
      for (var i = 0; i < N; i++) {
        var v = blocks ? blocks[i] : null;
        cells += '<i class="ig-b ' + (v === null || v === undefined ? "ig-empty" : v === "dirty" ? "ig-dirty" : "ig-" + v) + '" aria-hidden="true">' + (v && v !== "dirty" ? v : "") + "</i>";
      }
      return '<div class="ig-row"><span class="ig-label">' + esc(label) + '</span><span class="ig-cells">' + cells + '</span><span class="sr">' + esc(summary(blocks)) + "</span>" + (note ? '<span class="ig-note">' + note + "</span>" : "") + "</div>";
    }
    function summary(blocks) {
      if (!blocks) return "no file yet.";
      var names = { A: "version A", B: "version B", C: "version C (from the other full)", x: "data lost", dirty: "changed since the checkpoint" };
      var groups = {}, empty = 0;
      blocks.forEach(function (v, i) { if (v === null || v === undefined) { empty++; return; } (groups[v] = groups[v] || []).push(i + 1); });
      var parts = Object.keys(groups).map(function (k) {
        var g = groups[k];
        return (g.length === N ? "all " + N + " blocks" : (g.length === 1 ? "block " : "blocks ") + g.join(", ")) + ": " + (names[k] || k);
      });
      if (empty === N) return "empty.";
      return parts.join("; ") + (empty && empty < N ? "; others empty." : ".");
    }
    function draw() {
      var html = row("linux-01 disk", st.disk, st.stopped ? "guest stopped" : "guest running");
      html += row("bitmap (checkpoint full)", st.bitmap, st.bitmap ? "" : (st.needFull ? "none: the restored disk has no bitmap" : "none yet"));
      html += row("full backup file", st.full, "");
      html += row("incremental file", st.inc, st.inc ? "only the changed blocks" : "");
      if (st.restored) html += row("restored disk", st.restored.blocks, st.restored.ok ? '<b class="ig-ok">matches the newest data</b>' : '<b class="ig-bad">wrong disk, and no error was printed</b>');
      rows.innerHTML = html;
      rows.setAttribute("aria-label", "Disk, bitmap and backup files");
      B.writes.disabled = !st.full || st.inc !== null || st.stopped;
      B.full.disabled = (!!st.full && !st.needFull) || st.stopped && !st.needFull;
      B.inc.disabled = !st.bitmap || st.inc !== null || !st.full || st.disk.indexOf("B") < 0;
      B.loss.disabled = !st.inc || st.lost;
      B.restore.disabled = !st.lost || !!st.restored;
      B.wrong.disabled = !st.lost || !!st.restored;
      B.next.disabled = !st.restored;
    }
    function predictThen(q, opts, right, go) {
      if (!pon) return go();
      var g = ++agen;
      ask.hidden = false;
      ask.innerHTML = "<p><b>" + esc(q) + "</b></p><p>" + opts.map(function (o, i) { return '<button type="button" class="btn ghost small" data-i="' + i + '">' + esc(o) + "</button>"; }).join(" ") + "</p>";
      $$("button[data-i]", ask).forEach(function (b) {
        b.addEventListener("click", function () {
          if (g !== agen) { ask.hidden = true; return; }
          var ok = +b.getAttribute("data-i") === right;
          ask.innerHTML = "<p>" + (ok ? icon("check") + " <b>Right.</b>" : "<b>Not quite:</b> see what happens.") + "</p>";
          if (ok) award(dkey + "-ig-" + q.length, 2, null);
          go();
        });
      });
      $("button[data-i]", ask).focus();
    }
    function act(o) {
      dgAward(el);
      if (o !== "inc" && o !== "wrong") { agen++; ask.hidden = true; }
      if (o === "reset") { reset(); draw(); show(null, "<p>Back to the start: one disk, no backups.</p>"); return; }
      if (o === "full") {
        if (st.needFull) { st.stopped = false; st.disk = st.restored.blocks.slice(); }
        st.full = st.disk.slice(); st.bitmap = []; for (var i = 0; i < N; i++) st.bitmap.push(null);
        st.inc = null; st.lost = false; st.restored = null; st.needFull = false;
        draw(); show(P.full); return;
      }
      if (o === "writes") {
        CHANGED.forEach(function (i) { st.disk[i] = "B"; st.bitmap[i] = "dirty"; });
        draw(); show(P.writes); return;
      }
      if (o === "inc") {
        predictThen("How many of the 12 blocks will the incremental copy?", ["All 12", "Only the 3 changed blocks", "None: it only records the checkpoint"], 1, function () {
          st.inc = []; for (var i = 0; i < N; i++) st.inc.push(st.bitmap[i] === "dirty" ? st.disk[i] : null);
          draw(); show(P.incremental);
        });
        return;
      }
      if (o === "loss") { st.lost = true; CHANGED.concat([0, 7]).forEach(function (i) { st.disk[i] = "x"; }); draw(); show(null, "<p>The data is gone from the disk. Restore it from the two backup files.</p>"); return; }
      if (o === "restore") {
        st.stopped = true;
        var out = []; for (var i = 0; i < N; i++) out.push(st.inc[i] || st.full[i]);
        st.restored = { blocks: out, ok: true }; st.bitmap = null; st.needFull = true; st.disk = out.slice();
        draw(); show(P.rebase, partHTML(P.stopped) + partHTML(P.restored)); return;
      }
      if (o === "wrong") {
        predictThen("You rebase the incremental onto a different full backup. Does qemu-img report an error?", ["Yes: it refuses the wrong full", "No: it gives a wrong disk without any error"], 1, function () {
          st.stopped = true;
          var other = []; for (var i = 0; i < N; i++) other.push(i % 4 === 1 ? "C" : "A");
          var out = []; for (var j = 0; j < N; j++) out.push(st.inc[j] || other[j]);
          st.restored = { blocks: out, ok: false }; st.bitmap = null; st.needFull = true; st.disk = out.slice();
          draw(); show(P.wrongfull, "<p>The blocks the incremental did not copy come from the other full (C), so the disk is wrong. Checking the data inside the guest (sha256sum -c, 7.5) is what proves a restore.</p>");
        });
        return;
      }
      if (o === "next") { draw(); show(P.restored, "<p>So an incremental from the checkpoint is no longer possible: take a full backup (step 1) to start again.</p>"); B.full.disabled = false; return; }
    }
    function partHTML(p) { return p ? "<p>" + esc(p.text) + "</p>" + citeHTML(p.cites) : ""; }
    draw();
    show(null, "<p>Start with <b>1. Full backup + checkpoint</b>. Tick <b>Predict first</b> to guess before the incremental and the wrong-full restore.</p>");
  };

  /* Day 3: fault injector over the 3.2–3.4 labs. Every output line is the manual's own. */
  DIAGRAMS.faults = function (el, spec) {
    var P = {}; (spec.parts || []).forEach(function (p) { P[p.name] = p; });
    var O = spec.out || {};
    el.appendChild(h("p", { class: "dg-help" }, "profile-probe has two cards: its first card on a libvirt network, and the data card kc-pp on the bridge kc-br (linux-01's kc-l01 is in VLAN 10). Change one thing, then read the symptoms and the one command per layer."));
    var st = { net: "default", vlan: 10, link: "up" }, mystery = null;
    var ctl = h("div", { class: "flt-ctl" });
    function group(label, key, vals) {
      var g = h("div", { class: "flt-group", role: "radiogroup", "aria-label": label }, '<span class="flt-label">' + esc(label) + "</span>");
      vals.forEach(function (v) {
        var b = h("button", { type: "button", class: "btn ghost small", role: "radio", "data-k": key, "data-v": String(v[0]) }, esc(v[1]));
        b.addEventListener("click", function () { if (mystery) return; set(key, v[0]); });
        g.appendChild(b);
      });
      /* ARIA radio pattern: one Tab stop per group, arrow keys move the selection */
      g.addEventListener("keydown", function (e) {
        if (mystery || ["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown"].indexOf(e.key) < 0) return;
        e.preventDefault();
        var i = 0; vals.forEach(function (v, j) { if (String(st[key]) === String(v[0])) i = j; });
        var d = (e.key === "ArrowLeft" || e.key === "ArrowUp") ? -1 : 1;
        var nv = vals[(i + d + vals.length) % vals.length][0];
        set(key, nv);
        var nb = $('button[data-v="' + String(nv) + '"]', g); if (nb) nb.focus();
      });
      ctl.appendChild(g);
    }
    group("First card's network", "net", [["default", "default"], ["isolated", "kc-isolated"]]);
    group("kc-pp VLAN", "vlan", [[10, "VLAN 10"], [20, "VLAN 20"]]);
    group("kc-pp link", "link", [["up", "link up"], ["down", "link down"]]);
    var game = h("div", { class: "flow-tools" });
    var mbtn = h("button", { type: "button", class: "btn small" }, "Mystery fault: diagnose it");
    var rbtn = h("button", { type: "button", class: "btn ghost small" }, icon("reset") + " Reset");
    game.appendChild(mbtn); game.appendChild(rbtn);
    var sym = h("div", { class: "flt-out", "aria-live": "polite" });
    var layers = h("div", { class: "flt-out" });
    var ask = h("div", { class: "lc-ask", hidden: "" });
    el.appendChild(ctl); el.appendChild(game); el.appendChild(sym); el.appendChild(ask); el.appendChild(layers);
    var show = infoBox(el);
    function line(key) { return O[key] ? esc(O[key].line) : ""; }
    function src(key) { return O[key] ? ' <span class="cite">(' + esc(O[key].cite.label) + ", recorded excerpt)</span>" : ""; }
    function block(title, cmd, body) { return '<div class="flt-block"><p class="flt-t">' + title + '</p><pre class="code code-text"><code>' + esc("$ " + cmd) + "\n" + body + "</code></pre></div>"; }
    function plain(title, body) { return '<div class="flt-block"><p class="flt-t">' + title + '</p><pre class="code code-text"><code>' + body + "</code></pre></div>"; }
    /* per-layer evidence, used after a diagnosis and by the Inspect actions */
    function cableEv() {
      return block("Virtual cable" + (st.link === "down" ? src("link_down") : ""), "sudo virsh -c qemu:///system domif-getlink profile-probe kc-pp",
          st.link === "down" ? line("link_down") : "(link up: the manual records this command's output only for a downed link, in 3.4)") +
        (st.link === "down" ? block("…and the guest's own view of its data card" + src("nocarrier"), "ip -br link   # in profile-probe", line("nocarrier")) : "");
    }
    function vlanEv() { return block("Bridge port" + src(st.vlan === 20 ? "vlan20" : "vlan10"), "bridge vlan show dev kc-pp", line(st.vlan === 20 ? "vlan20" : "vlan10")); }
    function routeEv() {
      return block("Route out" + (st.net === "isolated" ? src("route_iso") : ""), "ip route   # in profile-probe",
        st.net === "isolated" ? line("route_iso") + "\n# no default line" : "# on default the route list has a default line (3.1 shows linux-01's)");
    }
    function draw(hideLayers) {
      $$("button[data-k]", ctl).forEach(function (b) {
        var on = String(st[b.getAttribute("data-k")]) === b.getAttribute("data-v");
        b.classList.toggle("on", on); b.setAttribute("aria-checked", on ? "true" : "false"); b.disabled = !!mystery;
        b.tabIndex = on ? 0 : -1;
      });
      /* while a mystery is unanswered the configuration is hidden, visually and from the accessibility tree */
      ctl.hidden = !!mystery;
      var curlKey = st.net === "isolated" ? "curl_fail" : "curl_ok";
      var m = !!hideLayers, html = "<h4>Symptoms</h4>";
      if (st.vlan === 20) {
        html += block("linux-01 pings profile-probe's data address" + (m ? "" : src("ping_vlan_fail")), "ping -c 2 -W 2 172.30.77.2",
          m ? "# no reply (the recorded lines are shown after you answer)" : line("ping_vlan_fail"));
      } else if (st.link === "down") {
        /* 3.4 prints only its lab script's echo for this case, not a ping transcript */
        html += m ? block("linux-01 pings profile-probe's data address", "ping -c 2 -W 2 172.30.77.2", "# no reply (the recorded lines are shown after you answer)")
          : plain("3.4 lab script result after ping exits 1" + src("noreply_link"), esc("if ssh … 'ping -c 2 -W 2 172.30.77.2'; then …; elif [ $? -eq 1 ]; then echo 'expected: no reply'; …") + "\n" + line("noreply_link"));
      } else {
        html += block("linux-01 pings profile-probe's data address" + (m ? "" : src("ping_ok")), "ping -c 2 -W 2 172.30.77.2", line("ping_ok"));
      }
      html += block("profile-probe reaches the Internet by address" + (m ? "" : src(curlKey)), "curl -sS --max-time " + (m ? "…" : (st.net === "isolated" ? "5" : "10")) + " -I http://1.1.1.1", line(curlKey));
      if (st.net === "isolated" && !m) html += block("profile-probe pings its gateway on kc-isolated" + src("gw_ping_iso"), "ping -c 2 -W 2 192.168.250.1", line("gw_ping_iso"));
      sym.innerHTML = html;
      if (hideLayers) { layers.innerHTML = '<div data-inspected></div>'; return; }
      layers.innerHTML = "<h4>One command per layer (3.4)</h4>" + cableEv() + vlanEv() + routeEv();
    }
    function set(key, v) {
      dgAward(el);
      st[key] = v; draw();
      show(key === "net" ? (v === "isolated" ? P.isolated : P["default"]) : key === "vlan" ? (v === 20 ? P.vlan : P.table) : (v === "down" ? P.link : P.table));
    }
    mbtn.addEventListener("click", function () {
      dgAward(el);
      var faults = [["vlan", 20, "Bridge port"], ["link", "down", "Virtual cable"], ["net", "isolated", "Route out"]];
      var f = faults[Math.floor(Math.random() * faults.length)];
      st = { net: "default", vlan: 10, link: "up" }; st[f[0]] = f[1]; mystery = f;
      var inspected = {};
      draw(true);
      ask.hidden = false;
      ask.innerHTML = "<p><b>A fault was injected.</b> Inspect a layer, then name it.</p>" +
        '<p class="flt-inspect"><button type="button" class="btn ghost small" data-i="cable">Inspect cable (domif-getlink)</button> ' +
        '<button type="button" class="btn ghost small" data-i="vlan">Inspect VLAN (bridge vlan show)</button> ' +
        '<button type="button" class="btn ghost small" data-i="route">Inspect route (ip route)</button></p>' +
        "<p>Which layer is broken? " + ["Virtual cable", "Bridge port", "Route out"].map(function (n) { return '<button type="button" class="btn ghost small" data-l="' + n + '">' + n + "</button>"; }).join(" ") + "</p>" +
        '<p class="flt-msg" aria-live="polite"></p>';
      $$("button[data-i]", ask).forEach(function (b) {
        b.addEventListener("click", function () {
          if (!mystery) return;
          var box = $("[data-inspected]", layers), k = b.getAttribute("data-i");
          inspected[k] = true;
          if (box && !box.querySelector('[data-ev="' + k + '"]')) {
            var wrap2 = h("div", { "data-ev": k }, k === "cable" ? cableEv() : k === "vlan" ? vlanEv() : routeEv());
            box.appendChild(wrap2);
          }
          b.disabled = true;
        });
      });
      $$("button[data-l]", ask).forEach(function (b) {
        b.addEventListener("click", function () {
          if (!mystery) return;
          /* "no reply" alone fits a pulled cable and a wrong VLAN equally: never grade it without evidence */
          var ambiguous = mystery[0] !== "net";
          if (ambiguous && !inspected.cable && !inspected.vlan) {
            $(".flt-msg", ask).innerHTML = "<b>Not yet:</b> \"no reply\" fits both a pulled cable and a wrong VLAN. Inspect cable or VLAN to distinguish these causes, then decide.";
            return;
          }
          var ok = b.getAttribute("data-l") === mystery[2];
          ask.innerHTML = "<p>" + (ok ? icon("check") + " <b>Right: " + esc(mystery[2]) + ".</b>" : "<b>Not that one:</b> it was the " + esc(mystery[2].toLowerCase()) + ".") + " All per-layer commands are below.</p>";
          if (ok) award(dkey + "-flt-" + mystery[2], 5, "fault found");
          var m = mystery; mystery = null; draw();
          show(m[0] === "vlan" ? P.vlan : m[0] === "link" ? P.link : P.isolated, partHTML(P.table));
        });
      });
      $("button[data-i]", ask).focus();
      show(null, "<p>The symptoms are real lines from 3.2–3.4. Inspect the layer you would check first.</p>");
    });
    rbtn.addEventListener("click", function () { mystery = null; ask.hidden = true; ask.innerHTML = ""; st = { net: "default", vlan: 10, link: "up" }; draw(); show(P.table); });
    function partHTML(p) { return p ? "<p>" + esc(p.text) + "</p>" + citeHTML(p.cites) : ""; }
    draw();
    show(P.table, "<p>Change one toggle, or press <b>Mystery fault</b>.</p>");
  };

  DIAGRAMS.smoke = function (el, spec) {
    var P = {}; (spec.parts || []).forEach(function (p) { P[p.name] = p; });
    var labels = (spec.data && spec.data.items) || [];
    var pre = spec.ssh_prefixes || [];
    el.appendChild(h("p", { class: "dg-help" }, "smoke.sh runs one check per layer. Pull the virtual cable and see which layers notice."));
    var list = h("div", { class: "smk" });
    var rows = labels.map(function (l) {
      var r = h("div", { class: "smk-row pass" }, "<b>PASS</b><span>" + esc(l) + "</span>");
      list.appendChild(r); return r;
    });
    var tools = h("div", { class: "flow-tools" });
    var down = false;
    var b = h("button", { type: "button", class: "btn small" }, "domif-setlink … down");
    tools.appendChild(b);
    el.appendChild(list); el.appendChild(tools);
    var show = infoBox(el);
    b.addEventListener("click", function () {
      dgAward(el);
      down = !down;
      rows.forEach(function (r, i) {
        var ssh = pre.some(function (p) { return labels[i].indexOf(p) === 0; });
        var fail = down && ssh;
        r.className = "smk-row " + (fail ? "fail" : "pass");
        $("b", r).textContent = fail ? "FAIL" : "PASS";
      });
      b.textContent = down ? "domif-setlink … up" : "domif-setlink … down";
      show(down ? P["link down"] : P.repair, down && P["ssh checks"] ? "<p>" + esc(P["ssh checks"].text) + "</p>" + citeHTML(P["ssh checks"].cites) : "");
    });
    show(P.smoke, citeHTML([spec.data.cite]));
  };

  /* ============================================================ terminal */
  function norm(s) {
    s = (s || "").trim().replace(/\s+/g, " ");
    s = s.replace(/^sudo /, "").replace(/^timeout \d+ /, "").replace(/^sudo /, "");
    s = s.replace(/ -c qemu:\/\/\/system\b/g, "").replace(/ --connect qemu:\/\/\/system\b/g, "");
    s = s.replace(/\$HOME\//g, "~/").replace(/\/home\/[a-z0-9_-]+\//g, "~/");
    s = s.replace(/\/var\/lib\/libvirt\/images\/kvm-course\//g, "$P/").replace(/"\$P\/([^"]*)"/g, "$P/$1");
    s = s.replace(/;$/, "");
    return s;
  }
  /* A2: loose() compares the words bash would pass after quote removal, so quoting only matters where
     bash would treat it differently: '{"execute":"query-kvm"}' never equals {execute:query-kvm}.
     A quoted character outside the plain set is kept with a backslash; $ inside double quotes still expands. */
  function loose(s) {
    var t = norm(s), words = [], cur = null, q = null, PLAIN = /[A-Za-z0-9_@%+=:,.\/~-]/;
    function add(ch, quoted) { if (cur === null) cur = ""; cur += (quoted && !PLAIN.test(ch)) ? "\\" + ch : ch; }
    for (var i = 0; i < t.length; i++) {
      var ch = t.charAt(i);
      if (q === "'") { if (ch === "'") q = null; else add(ch, true); continue; }
      if (q === '"') {
        if (ch === '"') q = null;
        else if (ch === "\\" && /[$`"\\]/.test(t.charAt(i + 1))) { add(t.charAt(++i), true); }
        else add(ch, ch !== "$");
        continue;
      }
      if (ch === "'" || ch === '"') { q = ch; if (cur === null) cur = ""; continue; }
      if (ch === "\\" && i + 1 < t.length) { add(t.charAt(++i), true); continue; }
      if (/\s/.test(ch)) { if (cur !== null) { words.push(cur); cur = null; } continue; }
      add(ch, false);
    }
    if (cur !== null) words.push(cur);
    return words.join(" ");
  }
  /* For "closest command" suggestions only: words without quotes. */
  function fuzzy(s) { return norm(s).replace(/["']/g, "").replace(/\s+/g, " "); }
  function initTerminal() {
    var host = $("[data-terminal]");
    var items = PAGE.terminal || [];
    if (!host) return;
    if (!items.length) {
      host.innerHTML = '<div class="term-screen"><div class="ln dim"># Today\'s chapter has no recorded command output yet, so there is nothing to simulate. Go straight to the real lab below.</div></div>';
      return;
    }
    var used = S.term[dkey] || (S.term[dkey] = {});
    var bar = h("div", { class: "term-bar" }, "<i></i><i></i><i></i><span class=\"term-title\">fedora44-host: simulated shell</span><span class=\"term-badge\">simulation</span>");
    var screen = h("div", { class: "term-screen", role: "log", "aria-live": "polite", "aria-label": "Terminal output" });
    var line = h("div", { class: "term-input" }, '<span class="ps">[you@fedora ~]$</span>');
    var input = h("input", { type: "text", "aria-label": "Type a command", autocomplete: "off", autocapitalize: "off", spellcheck: "false" });
    line.appendChild(input);
    var tryBox = h("div", { class: "term-try" }, "<span>Try:</span>");
    host.appendChild(bar); host.appendChild(screen); host.appendChild(line); host.appendChild(tryBox);
    var prog = h("div", { class: "term-progress" }, '<span data-tp></span><div class="bar"><span></span></div>');
    host.parentNode.insertBefore(prog, host.nextSibling);
    var byNorm = {}, byLoose = {};
    items.forEach(function (it) {
      [it.cmd].concat(it.alias || []).forEach(function (c) { byNorm[norm(c)] = it; byLoose[loose(c)] = it; });
      var chip = h("button", { type: "button", class: "term-chip" + (used[it.id] ? " used" : ""), title: it.hint || it.cmd, "data-id": it.id }, esc(it.cmd));
      chip.addEventListener("click", function () { input.value = it.cmd; fromChip = it.cmd; input.focus(); });
      tryBox.appendChild(chip);
    });
    var fromChip = null;
    input.addEventListener("input", function () { fromChip = null; });
    /* Mission mode: hide the command chips and show each command's question instead. */
    var missionBox = h("div", { class: "term-missions", hidden: "" });
    var mbtn = h("button", { type: "button", class: "btn ghost small term-mode", "aria-pressed": "false" }, "Mission mode: hide the commands");
    host.parentNode.insertBefore(mbtn, prog);
    host.parentNode.insertBefore(missionBox, prog);
    function drawMissions() {
      missionBox.innerHTML = "<p class=\"fine\">Type the command that answers each question from memory (5 XP each; a clicked command earns 1 XP). Built-in <code>hint</code> still helps.</p><ol>" +
        items.map(function (it) { return '<li class="' + (used[it.id] ? "done" : "") + '">' + esc(it.hint || "Run a command from today's lab") + "</li>"; }).join("") + "</ol>";
    }
    function setMission(on) {
      mbtn.setAttribute("aria-pressed", on ? "true" : "false");
      mbtn.textContent = on ? "Show the commands again" : "Mission mode: hide the commands";
      tryBox.hidden = on; missionBox.hidden = !on;
      if (on) drawMissions();
    }
    mbtn.addEventListener("click", function () {
      var on = mbtn.getAttribute("aria-pressed") !== "true";
      setMission(on);
      S.mission = S.mission || {}; S.mission[dkey] = on; save();   /* the learner's explicit choice wins */
    });
    var hist = [], hpos = 0;
    function print(html, cls) { screen.appendChild(h("div", { class: "ln " + (cls || "") }, html)); screen.scrollTop = screen.scrollHeight; }
    function progress() {
      var n = items.filter(function (it) { return used[it.id]; }).length;
      $("[data-tp]", prog).textContent = n + " of " + items.length + " commands tried";
      $(".bar span", prog).style.width = (100 * n / items.length) + "%";
      if (n === items.length) {
        if (!S.awards[dkey + "-term-all"] && !(S.mission && dkey in S.mission))
          print("# Every command tried. Press <b>Mission mode</b> below the terminal to hide them and practise from memory.", "dim");
        award(dkey + "-term-all", 25, "every simulated command tried");
      }
    }
    print("# Simulated shell for Day " + DAY + ". It replays the output the manual recorded on its validation host.", "dim");
    print("# Type a command (or click one under Try:), then press Enter. Type <b>help</b> for the list, <b>hint</b> for a suggestion.", "dim");
    function suggest(cmd) {
      var words = fuzzy(cmd).split(" ").filter(function (w) { return w.length >= 3; }), best = null, bs = 0;
      items.forEach(function (it) {
        var w2 = fuzzy(it.cmd).split(" "), sc = 0;
        words.forEach(function (w) { if (w2.indexOf(w) >= 0) sc++; });
        if (sc > bs) { bs = sc; best = it; }
      });
      return (bs >= 2 || (words.length && bs / words.length >= 0.5 && bs >= 1 && words.length <= 2)) ? best : null;
    }
    /* The simulator requires the manual's sudo form; system/session guidance only where it applies (virsh). */
    function sudoHint(typed, canonical) {
      var msg = '<span class="hint">This simulator follows the manual\'s sudo form.</span> Try: <code>' + esc(canonical) + "</code>.";
      if (/^virsh\b/.test(typed)) {
        if (/(-c|--connect)\s+qemu:\/\/\/system\b/.test(typed))
          msg += " Your own user may connect to qemu:///system too; polkit decides what you may do there (6.1). The manual uses sudo.";
        else if (!/(-c|--connect)\s/.test(typed))
          msg += " Without -c, virsh as your own user may talk to your own qemu:///session instance, which has a separate VM list (2.5).";
      }
      return msg;
    }
    function otherDay(cmd) {
      var od = PAGE.otherdays || {}, n = loose(cmd);
      for (var d in od) for (var i = 0; i < od[d].length; i++) if (loose(od[d][i]) === n) return d;
      return null;
    }
    function run(raw) {
      var cmd = raw.trim();
      print('<span class="ps">[you@fedora ~]$</span> <span class="cmd">' + esc(cmd) + "</span>");
      if (!cmd) return;
      hist.push(cmd); hpos = hist.length;
      var low = cmd.toLowerCase();
      if (low === "clear") { screen.innerHTML = ""; return; }
      if (low === "help") {
        print("Today's simulator knows these commands. Where the manual writes sudo virsh -c qemu:///system, you may leave out -c qemu:///system (root's default), but not sudo:", "dim");
        items.forEach(function (it) { print("  " + esc(it.cmd) + (it.hint ? '  <span class="dim"># ' + esc(it.hint) + "</span>" : "")); });
        print("Built-ins: help, hint, history, clear", "dim");
        return;
      }
      if (low === "hint") {
        var next = items.filter(function (it) { return !used[it.id]; })[0] || items[0];
        print('<span class="hint">Hint:</span> ' + esc(next.hint || "Try this one") + ' &rarr; <code>' + esc(next.cmd) + "</code>");
        return;
      }
      if (low === "history") { hist.forEach(function (c, i) { print("  " + (i + 1) + "  " + esc(c)); }); return; }
      if (low === "exit" || low === "logout") { print("This shell is only a simulation; there is nothing to log out of. The real lab is further down the page.", "hint"); return; }
      var it = byNorm[norm(cmd)] || byLoose[loose(cmd)];
      var bare = cmd.replace(/^timeout \d+ /, "");
      if (it && /^sudo /.test(it.cmd.replace(/^timeout \d+ /, "")) && !/^sudo /.test(bare)) {
        print(sudoHint(bare, it.cmd));
        return;
      }
      if (it) {
        if (it.note) print("# " + esc(it.note), "dim");
        it.out.forEach(function (l) { print(esc(l)); });
        print("# " + (it.excerpt ? "excerpt " : "") + 'recorded on the validation host, <span class="src"><a href="' + esc(ROOT + it.cite.href) + '">manual ' + esc(it.cite.label) + "</a></span>" +
          ". Your values (addresses, IDs, sizes, dates) will differ.", "dim");
        if (it.result) {
          print("# Result: " + esc(it.result.label) + ' (<span class="src"><a href="' + esc(ROOT + it.result.cite.href) + '">manual ' + esc(it.result.cite.label) + "</a></span>):", "dim");
          it.result.lines.forEach(function (l) { print(esc(l)); });
        }
        var typed = fromChip !== cmd;
        if (!used[it.id]) {
          used[it.id] = true; save();
          var chip = $('.term-chip[data-id="' + it.id + '"]', tryBox); if (chip) chip.classList.add("used");
          progress(); refreshDay();
          if (!missionBox.hidden) drawMissions();
        }
        award(dkey + "-term-" + it.id, typed ? 5 : 1, null);
        if (typed && S.awards[dkey + "-term-" + it.id] === 1) award(dkey + "-term-typed-" + it.id, 4, null);
        fromChip = null;
        return;
      }
      var od = otherDay(cmd);
      if (od) {
        var odCmd = (PAGE.otherdays[od] || []).filter(function (c) { return loose(c) === loose(cmd); })[0] || "";
        if (/^sudo /.test(odCmd.replace(/^timeout \d+ /, "")) && !/^sudo /.test(bare)) {
          print(sudoHint(bare, odCmd) + ' Its recorded output is in the <a href="day-' + ("0" + od).slice(-2) + '.html#terminal">Day ' + esc(od) + "</a> simulator.");
          return;
        } print('<span class="hint">Good command, wrong day:</span> its recorded output is in the Day ' + esc(od) + ' simulator (<a href="day-' + ("0" + od).slice(-2) + '.html#terminal">open Day ' + esc(od) + "</a>)."); return; }
      var sg = suggest(cmd);
      if (sg) print('<span class="hint">Not in today\'s simulator.</span> Closest command it knows: <code>' + esc(sg.cmd) + "</code>. Anything else, run in the real lab.");
      else print('<span class="hint">Not in today\'s simulator.</span> It only knows commands whose output the manual recorded. Type <b>help</b> to list them, or run this one in the real lab.');
    }
    input.addEventListener("keydown", function (e) {
      if (e.key === "Enter") { e.preventDefault(); run(input.value); input.value = ""; }
      else if (e.key === "ArrowUp") { if (hpos > 0) { hpos--; input.value = hist[hpos]; } e.preventDefault(); }
      else if (e.key === "ArrowDown") { if (hpos < hist.length - 1) { hpos++; input.value = hist[hpos]; } else { hpos = hist.length; input.value = ""; } e.preventDefault(); }
      else if (e.key === "Tab" && !e.shiftKey) {
        var v = norm(input.value);
        if (!v) return;
        var m = items.filter(function (it) { return norm(it.cmd).indexOf(v) === 0; });
        if (m.length && input.value !== m[0].cmd) { e.preventDefault(); input.value = m[0].cmd; fromChip = m[0].cmd; }
      } else if ((e.ctrlKey && e.key.toLowerCase() === "l")) { e.preventDefault(); screen.innerHTML = ""; }
    });
    screen.addEventListener("click", function (e) {
      var c = e.target.closest && e.target.closest("code");
      if (c) { input.value = c.textContent; input.focus(); return; }
      if (!window.getSelection || !String(window.getSelection())) input.focus();
    });
    progress();
    /* Mission mode never hides a command on first exposure (codex-site 6): it starts on only when the learner
       chose it for this day, or by default once every command of this day has been tried. */
    var choice = S.mission && dkey in S.mission ? S.mission[dkey] : null;
    if (choice === true || (choice === null && items.every(function (it) { return used[it.id]; }))) setMission(true);
  }

  /* ============================================================ labs */
  function initLabs() {
    var labs = PAGE.labs || [];
    var byId = {};
    labs.forEach(function (l) { byId[l.id] = l; });
    function countLab(art) {
      var l = byId[art.getAttribute("data-lab")];
      if (!l) return;
      var total = l.steps.length + (l.check ? 1 : 0);
      var done = l.steps.filter(function (s) { return S.ticks[s]; }).length + (l.check && S.ticks[l.check] ? 1 : 0);
      var c = $("[data-lab-count]", art);
      if (c) c.textContent = total ? done + "/" + total : "read";
      art.classList.toggle("complete", total > 0 && done === total);
    }
    function totals() {
      var t = 0, d = 0;
      labs.forEach(function (l) { l.steps.concat(l.check ? [l.check] : []).forEach(function (s) { t++; if (S.ticks[s]) d++; }); });
      var el = $("[data-lab-total]"); if (el) el.textContent = d + " of " + t + " ticks";
    }
    $$("[data-tick]").forEach(function (cb) {
      var id = cb.getAttribute("data-tick");
      cb.checked = !!S.ticks[id];
      var step = cb.closest(".step"); if (step) step.classList.toggle("done", cb.checked);
      cb.addEventListener("change", function () {
        S.ticks[id] = cb.checked; save();
        if (step) step.classList.toggle("done", cb.checked);
        if (cb.checked) {
          var isCheck = /-check$/.test(id);
          award("tick-" + id, isCheck ? 25 : 10, isCheck ? "check passed" : null);
        }
        countLab(cb.closest(".lab")); totals(); refreshDay();
      });
    });
    var arts = $$(".lab");
    arts.forEach(function (art) {
      countLab(art);
      var btn = $(".lab-toggle", art), body = $(".lab-body", art);
      btn.addEventListener("click", function () {
        var open = btn.getAttribute("aria-expanded") === "true";
        btn.setAttribute("aria-expanded", open ? "false" : "true");
        body.hidden = open;
      });
    });
    function setAll(open) { arts.forEach(function (a) { $(".lab-toggle", a).setAttribute("aria-expanded", open ? "true" : "false"); $(".lab-body", a).hidden = !open; }); }
    var ex = $("[data-labs-expand]"), co = $("[data-labs-collapse]");
    if (ex) ex.addEventListener("click", function () { setAll(true); });
    if (co) co.addEventListener("click", function () { setAll(false); });
    // open the first unfinished lab (or the one in the URL hash)
    var target = null;
    if (location.hash && location.hash.indexOf("#lab-") === 0) target = document.getElementById(location.hash.slice(1));
    if (!target) target = arts.filter(function (a) { return !a.classList.contains("complete") && !a.classList.contains("lab-intro"); })[0];
    if (target) { $(".lab-toggle", target).setAttribute("aria-expanded", "true"); $(".lab-body", target).hidden = false; }
    if (location.hash && location.hash.indexOf("#lab-") === 0 && target) setTimeout(function () { target.scrollIntoView(); }, 50);
    totals();
  }
  /* Reveal and focus whatever a #hash points at: a concept card, a lab section or a question. */
  function dayActivate(hash) {
    if (!hash || hash.length < 2) return false;
    var id = decodeURIComponent(hash.slice(1)), el = document.getElementById(id);
    if (!el) return false;
    if (id.indexOf("c-") === 0 && deckApi) {
      deckApi.cards.forEach(function (c, i) { if (c.id === id) deckApi.show(i); });
    } else if (id.indexOf("lab-") === 0) {
      var t = $(".lab-toggle", el), b = $(".lab-body", el);
      if (t && b) { t.setAttribute("aria-expanded", "true"); b.hidden = false; }
    } else if (id.indexOf("q-") === 0 && quizGo[id]) {
      quizGo[id]();
    }
    el.scrollIntoView({ block: "start" });
    var f = el.matches("article, section, .q") ? (el.querySelector("h3, h4, .q-text") || el) : el;
    if (!f.hasAttribute("tabindex")) f.setAttribute("tabindex", "-1");
    f.focus({ preventScroll: true });
    return true;
  }

  /* ============================================================ quiz */
  var TYPE_LABEL = { mc: "Multiple choice", predict: "Predict the output", order: "Order the steps", build: "Build the command" };
  /* Practice mode keeps a temporary state per question: no XP, no change to saved progress. */
  var practice = {};
  function qState(id) {
    if (practice[id]) return practice[id];
    return S.quiz[id] || (S.quiz[id] = { ok: false, tries: 0 });
  }
  function solved(q, wrap, firstTry) {
    var st = qState(q.id);
    if (practice[q.id]) {
      st.ok = true;
    } else if (!st.ok) {
      st.ok = true; save();
      award("quiz-" + q.id, firstTry ? 20 : 10, firstTry ? "right first time" : "correct");
    }
    wrap.classList.add("correct");
    refreshDay(); groupScores();
    wrap.dispatchEvent(new CustomEvent("qsolved", { bubbles: true }));
  }
  function feedback(wrap, good, html) {
    var fb = $(".q-feedback", wrap);
    fb.className = "q-feedback show " + (good ? "good" : "bad");
    fb.innerHTML = html;
  }
  function explainHTML(q) { return '<b class="verdict">' + icon("check") + " Correct.</b> " + q.explain + " " + citeHTML(q.cites); }
  function groupScores() {
    $$(".qgroup").forEach(function (g) {
      var ids = (g.getAttribute("data-ids") || "").split(",");
      var ok = ids.filter(function (id) { return S.quiz[id] && S.quiz[id].ok; }).length;
      var el = $(".qgroup-score", g); if (el) el.textContent = ok + " of " + ids.length + " correct";
    });
  }
  /* whys: one sentence per option (same order as options), "" for the right one; shown when that option is picked. */
  function renderMC(q, wrap, options, answerIdx, mono, whys) {
    whys = whys || [];
    if (!mono) {
      var idx = shuffle(options.map(function (o, i) { return i; }));
      options = idx.map(function (i) { return options[i]; });
      whys = idx.map(function (i) { return whys[i] || ""; });
      answerIdx = idx.indexOf(answerIdx);
    }
    var box = h("div", { class: "q-opts", role: "group", "aria-label": "Answers" });
    var st = qState(q.id);
    var wrongs = 0;
    var btns = options.map(function (o, i) {
      var b = h("button", { type: "button", class: "q-opt" + (mono ? " mono" : "") }, '<span class="key">' + "ABCDEF"[i] + "</span><span>" + o + "</span>");
      b.addEventListener("click", function () {
        if (st.ok) return;
        st.tries++; save();
        if (i === answerIdx) {
          b.classList.add("right");
          btns.forEach(function (x) { x.disabled = true; });
          feedback(wrap, true, explainHTML(q));
          solved(q, wrap, st.tries === 1);
        } else {
          wrongs++;
          b.classList.add("wrong"); b.disabled = true;
          feedback(wrap, false, '<b class="verdict">Not quite.</b> ' + (whys[i] ? '<span class="q-why">' + whys[i] + "</span> " : "") +
            "Have another look" + (wrongs >= 2 ? ', or <button type="button" class="linkish" data-reveal>show the answer</button>.' : "."));
          var rv = $("[data-reveal]", wrap);
          if (rv) rv.addEventListener("click", function () { btns[answerIdx].classList.add("right"); feedback(wrap, false, "The answer is highlighted; click it to continue. " + q.explain + " " + citeHTML(q.cites)); });
        }
      });
      box.appendChild(b);
      return b;
    });
    wrap.appendChild(box);
    if (st.ok) { btns[answerIdx].classList.add("right"); btns.forEach(function (x) { x.disabled = true; }); wrap.classList.add("correct"); }
    wrap.appendChild(h("div", { class: "q-feedback" }));
    if (st.ok) feedback(wrap, true, explainHTML(q));
  }
  function renderPredict(q, wrap) {
    wrap.appendChild(h("div", { class: "q-cmd" }, '<pre class="code code-bash"><code>' + esc("$ " + q.cmd) + "</code></pre>"));
    var all = [q.correct].concat(q.distractors), whyAll = [""].concat(q.why || []);
    var idx = shuffle(all.map(function (o, i) { return i; }));
    renderMC(q, wrap, idx.map(function (i) { return esc(all[i]); }), idx.indexOf(0), true,
      idx.map(function (i) { return whyAll[i] || ""; }));
  }
  function renderOrder(q, wrap) {
    var st = qState(q.id);
    var n = q.items.length;
    var order = [];
    for (var i = 0; i < n; i++) order.push(i);
    if (!st.ok) { var guard = 0; do { order = shuffle(order); guard++; } while (order.every(function (v, i) { return v === i; }) && guard < 20); }
    wrap.appendChild(h("p", { class: "order-hint" }, "Drag the steps, or use the arrow buttons. Keyboard: focus a step, press Space to pick it up, arrows to move, Space to drop."));
    var list = h("ol", { class: "order-list" });
    wrap.appendChild(list);
    function draw() {
      list.innerHTML = "";
      order.forEach(function (idx, pos) {
        var li = h("li", { class: "order-item", tabindex: "0", "data-idx": idx, "aria-label": "Step " + (pos + 1) + ": " + textOf(q.items[idx]) },
          '<span class="grip" aria-hidden="true">⋮⋮</span><span class="txt">' + q.items[idx] + '</span><span class="mv"><button type="button" aria-label="Move up">&uarr;</button><button type="button" aria-label="Move down">&darr;</button></span>');
        var bs = $$("button", li);
        bs[0].disabled = pos === 0 || st.ok; bs[1].disabled = pos === n - 1 || st.ok;
        bs[0].addEventListener("click", function () { swap(pos, pos - 1, true); });
        bs[1].addEventListener("click", function () { swap(pos, pos + 1, true); });
        if (st.ok) li.classList.add("right");
        list.appendChild(li);
      });
    }
    var grabbed = -1;
    function swap(a, b, keepFocusBtn) {
      if (b < 0 || b >= n || st.ok) return;
      var t = order[a]; order[a] = order[b]; order[b] = t;
      draw();
      var li = list.children[b];
      if (li) { if (keepFocusBtn) { var bt = $$("button", li)[b < a ? 0 : 1]; (bt && !bt.disabled ? bt : li).focus(); } else li.focus(); }
    }
    list.addEventListener("keydown", function (e) {
      var li = e.target.closest(".order-item");
      if (!li || e.target.tagName === "BUTTON") return;
      var pos = Array.prototype.indexOf.call(list.children, li);
      if (e.key === " " || e.key === "Enter") {
        e.preventDefault();
        if (grabbed === pos) { grabbed = -1; li.classList.remove("grabbed"); }
        else { grabbed = pos; $$(".order-item", list).forEach(function (x) { x.classList.remove("grabbed"); }); li.classList.add("grabbed"); }
      } else if ((e.key === "ArrowUp" || e.key === "ArrowDown") && grabbed === pos) {
        e.preventDefault();
        var to = pos + (e.key === "ArrowUp" ? -1 : 1);
        if (to >= 0 && to < n) { swap(pos, to, false); grabbed = to; list.children[to].classList.add("grabbed"); }
      } else if (e.key === "ArrowUp" && pos > 0) { e.preventDefault(); list.children[pos - 1].focus(); }
      else if (e.key === "ArrowDown" && pos < n - 1) { e.preventDefault(); list.children[pos + 1].focus(); }
    });
    // pointer drag
    var drag = null;
    list.addEventListener("pointerdown", function (e) {
      var li = e.target.closest(".order-item");
      if (!li || e.target.closest("button") || st.ok) return;
      drag = { li: li, from: Array.prototype.indexOf.call(list.children, li) };
      li.classList.add("dragging");
      li.setPointerCapture && li.setPointerCapture(e.pointerId);
    });
    list.addEventListener("pointermove", function (e) {
      if (!drag) return;
      var items = $$(".order-item", list);
      var y = e.clientY;
      for (var i = 0; i < items.length; i++) {
        var r = items[i].getBoundingClientRect();
        if (items[i] !== drag.li && y > r.top && y < r.bottom) {
          var cur = items.indexOf(drag.li);
          var moved = order.splice(cur, 1)[0]; order.splice(i, 0, moved);
          if (i > cur) list.insertBefore(drag.li, items[i].nextSibling); else list.insertBefore(drag.li, items[i]);
          break;
        }
      }
    });
    function endDrag() { if (!drag) return; drag = null; draw(); }
    list.addEventListener("pointerup", endDrag);
    list.addEventListener("pointercancel", endDrag);
    draw();
    var actions = h("div", { class: "q-actions" });
    var chk = h("button", { type: "button", class: "btn small" }, "Check order");
    actions.appendChild(chk);
    wrap.appendChild(actions);
    wrap.appendChild(h("div", { class: "q-feedback" }));
    chk.addEventListener("click", function () {
      if (st.ok) return;
      st.tries++; save();
      var right = order.every(function (v, i) { return v === i; });
      $$(".order-item", list).forEach(function (li, i) { li.classList.toggle("right", order[i] === i); li.classList.toggle("wrong", order[i] !== i); });
      if (right) { feedback(wrap, true, explainHTML(q)); chk.disabled = true; solved(q, wrap, st.tries === 1); draw(); }
      else {
        var k = order.filter(function (v, i) { return v === i; }).length;
        feedback(wrap, false, '<b class="verdict">Not yet.</b> ' + k + " of " + n + " steps are in the right place (green). Move the others and check again.");
      }
    });
    if (st.ok) { chk.disabled = true; wrap.classList.add("correct"); feedback(wrap, true, explainHTML(q)); }
  }
  /* A6: build-the-command is graded as a set of options, each kept with its argument, not as fixed sequences.
     For programs whose parsers accept options anywhere after the subcommand, an answer is right when it has the
     same prefix (sudo, program, virsh's global -c before the subcommand, subcommand), the same positionals in the
     same order, and the same option units in any order. Other commands (ip, pipelines, ...) keep their order. */
  var BUILD_ARGOPTS = {
    "qemu-img": ["-f", "-F", "-b", "-O", "-o"],
    "virsh": ["--checkpointxml", "--timeout", "--condition", "--base"],
    "virt-xml": ["--connect", "--edit", "--network"],
    "ausearch": ["-m", "-ts", "-p", "-f", "-k"],
    "gcc": ["-o"]
  };
  var BUILD_GLOBAL = { "virsh": ["-c", "--connect"] };
  var BUILD_SUBCMD = { "qemu-img": true, "virsh": true };
  function buildKey(tokens) {
    var t = tokens.slice(), pre = [];
    if (t[0] === "sudo") pre.push(t.shift());
    var prog = t.shift();
    pre.push(prog);
    var args = BUILD_ARGOPTS[prog];
    if (!args || t.indexOf("|") >= 0) return null;
    var glob = BUILD_GLOBAL[prog] || [], g = [], units = [], pos = [];
    if (BUILD_SUBCMD[prog]) {
      while (t.length && /^-/.test(t[0])) {
        var o = t.shift();
        if (glob.indexOf(o) < 0 || !t.length) return "!";
        g.push(o + " " + t.shift());
      }
      if (!t.length) return "!";
      pre.push(g.sort().join(" "), t.shift());
    }
    for (var i = 0; i < t.length; i++) {
      var x = t[i];
      if (/^-./.test(x)) {
        if (glob.indexOf(x) >= 0) return "!";
        if (args.indexOf(x) >= 0) { if (i + 1 >= t.length) return "!"; units.push(x + " " + t[++i]); }
        else units.push(x);
      } else pos.push(x);
    }
    return JSON.stringify([pre, pos, units.sort()]);
  }
  function buildRight(q, got, answers) {
    if (answers.some(function (a) { return got.length === a.length && got.every(function (t, i) { return t === a[i]; }); })) return true;
    var ref = buildKey(q.tokens), mine = buildKey(got);
    return !!ref && ref !== "!" && mine === ref;
  }
  function renderBuild(q, wrap) {
    var st = qState(q.id);
    var lineEl = h("div", { class: "build-line", "aria-label": "Your command", role: "group" });
    var pool = h("div", { class: "build-pool", role: "group", "aria-label": "Pieces" });
    wrap.appendChild(lineEl); wrap.appendChild(pool);
    var chips = shuffle(q.tokens.map(function (t, i) { return { t: t, k: "t" + i }; }).concat((q.extra || []).map(function (t, i) { return { t: t, k: "x" + i }; })));
    var picked = [];
    var answers = [q.tokens].concat(q.accept || []);
    /* draw(focus): rebuild both rows, then put keyboard focus back where the learner was working. */
    function draw(focus) {
      lineEl.innerHTML = "";
      lineEl.classList.remove("right", "wrong");
      var lineBtns = picked.map(function (c, i) {
        var b = h("button", { type: "button", class: "bchip", "aria-label": "Remove " + c.t }, esc(c.t));
        b.addEventListener("click", function () { if (st.ok) return; picked.splice(i, 1); draw({ line: i }); });
        lineEl.appendChild(b);
        return b;
      });
      pool.innerHTML = "";
      var poolBtns = chips.map(function (c, j) {
        var used = picked.indexOf(c) >= 0;
        var b = h("button", { type: "button", class: "bchip", "aria-label": "Add " + c.t }, esc(c.t));
        b.disabled = used || st.ok;
        b.addEventListener("click", function () { picked.push(c); draw({ pool: j }); });
        pool.appendChild(b);
        return b;
      });
      if (focus && !st.ok) {
        var target = null;
        if (focus.pool !== undefined) {
          for (var k = 1; k <= poolBtns.length && !target; k++) {
            var cand = poolBtns[(focus.pool + k) % poolBtns.length];
            if (!cand.disabled) target = cand;
          }
          target = target || lineBtns[lineBtns.length - 1];
        } else if (focus.line !== undefined) {
          target = lineBtns[Math.min(focus.line, lineBtns.length - 1)] || poolBtns.filter(function (b) { return !b.disabled; })[0];
        }
        if (target) target.focus();
      }
    }
    var actions = h("div", { class: "q-actions" });
    var chk = h("button", { type: "button", class: "btn small" }, "Check command");
    var undo = h("button", { type: "button", class: "btn ghost small" }, "Undo");
    var clr = h("button", { type: "button", class: "btn ghost small" }, "Clear");
    actions.appendChild(chk); actions.appendChild(undo); actions.appendChild(clr);
    wrap.appendChild(actions);
    wrap.appendChild(h("div", { class: "q-feedback" }));
    undo.addEventListener("click", function () { if (!st.ok) { picked.pop(); draw(); } });
    clr.addEventListener("click", function () { if (!st.ok) { picked = []; draw(); } });
    chk.addEventListener("click", function () {
      if (st.ok) return;
      st.tries++; save();
      var got = picked.map(function (c) { return c.t; });
      var right = buildRight(q, got, answers);
      if (right) {
        var same = got.join(" ") === q.tokens.join(" ");
        lineEl.classList.add("right"); feedback(wrap, true, explainHTML(q) + (same ? "" : " Your order is valid too; the manual writes it like this:") +
          '<pre class="code code-bash"><code>' + esc(q.tokens.join(" ")) + "</code></pre>");
        [chk, undo, clr].forEach(function (b) { b.disabled = true; });
        solved(q, wrap, st.tries === 1); draw(); lineEl.classList.add("right");
      } else {
        lineEl.classList.add("wrong");
        var need = q.tokens.slice(), stray = 0;
        got.forEach(function (t) { var j = need.indexOf(t); if (j >= 0) need.splice(j, 1); else stray++; });
        var free = !!buildKey(q.tokens);
        var msg = stray ? "At least one piece does not belong in this command; not every piece is needed." :
          need.length ? "So far no wrong piece, but " + (need.length === 1 ? "one piece is" : need.length + " pieces are") + " still missing." :
          free ? "All the right pieces, but not in a working order: sudo and the program come first" +
            (BUILD_SUBCMD[q.tokens[q.tokens[0] === "sudo" ? 1 : 0]] ? ", then " + (q.tokens.indexOf("-c") >= 0 ? "-c and its URI, then " : "") + "the subcommand" : "") +
            "; each option keeps its value right after it, and the names keep their order. Options themselves may go in any order." :
          "All the right pieces, but this command needs them in one fixed order.";
        feedback(wrap, false, '<b class="verdict">Not yet.</b> ' + msg + (st.tries >= 3 ? ' <button type="button" class="linkish" data-reveal>Show the command</button>' : ""));
        var rv = $("[data-reveal]", wrap);
        if (rv) rv.addEventListener("click", function () {
          picked = q.tokens.map(function (t) { return chips.filter(function (c) { return c.t === t && picked.indexOf(c) < 0; })[0] || { t: t, k: t }; });
          picked = []; var avail = chips.slice();
          q.tokens.forEach(function (t) { for (var i = 0; i < avail.length; i++) if (avail[i].t === t) { picked.push(avail[i]); avail.splice(i, 1); break; } });
          draw(); feedback(wrap, false, "Here it is; press Check command to finish. " + q.explain + " " + citeHTML(q.cites));
        });
      }
    });
    if (st.ok) {
      var avail = chips.slice();
      q.tokens.forEach(function (t) { for (var i = 0; i < avail.length; i++) if (avail[i].t === t) { picked.push(avail[i]); avail.splice(i, 1); break; } });
      [chk, undo, clr].forEach(function (b) { b.disabled = true; });
      draw(); lineEl.classList.add("right"); wrap.classList.add("correct");
      feedback(wrap, true, explainHTML(q));
    } else draw();
  }
  function initQuiz() {
    var host = $("[data-quiz]");
    if (!host) return;
    (PAGE.quiz || []).forEach(function (g, gi) {
      var sec = h("div", { class: "qgroup", "data-ids": g.questions.map(function (q) { return q.id; }).join(",") });
      sec.appendChild(h("div", { class: "qgroup-head" }, "<h3>" + esc(g.group) + '</h3><span class="qgroup-score"></span>'));
      var wraps = [];
      function makeWrap(q, qi) {
        var wrap = h("div", { class: "q", id: "q-" + q.id, "data-interactive": "quiz" });
        wrap.appendChild(h("div", { class: "q-type" }, esc(TYPE_LABEL[q.type] || q.type) + " · question " + (qi + 1) + " of " + g.questions.length +
          (practice[q.id] ? " · practice" : "")));
        wrap.appendChild(h("p", { class: "q-text" }, q.q));
        if (q.type === "mc") renderMC(q, wrap, q.options, q.answer, false, q.why);
        else if (q.type === "predict") renderPredict(q, wrap);
        else if (q.type === "order") renderOrder(q, wrap);
        else if (q.type === "build") renderBuild(q, wrap);
        return wrap;
      }
      g.questions.forEach(function (q, qi) {
        var wrap = makeWrap(q, qi);
        wraps.push(wrap);
        sec.appendChild(wrap);
      });
      var api = stepper(sec, g, wraps);
      g.questions.forEach(function (q, qi) { quizGo["q-" + q.id] = function () { api.go(qi, false); }; });
      var again = h("button", { type: "button", class: "btn ghost small qagain" }, icon("reset") + " Practice this group again");
      again.addEventListener("click", function () {
        g.questions.forEach(function (q, qi) {
          practice[q.id] = { ok: false, tries: 0 };
          var w = makeWrap(q, qi);
          sec.replaceChild(w, wraps[qi]);
          wraps[qi] = w;
        });
        api.go(0, true);
        toast("Practice round: answers are hidden again; no extra XP, and your progress is kept.");
      });
      sec.appendChild(h("p", { class: "qagain-row" })).appendChild(again);
      host.appendChild(sec);
    });
    groupScores();
    host.addEventListener("keydown", function (e) {
      var q = e.target.closest ? e.target.closest(".q") : null;
      if (!q || e.ctrlKey || e.metaKey || e.altKey || /input|textarea/i.test(e.target.tagName)) return;
      var k = e.key.toLowerCase(), i = /^[1-6]$/.test(k) ? +k - 1 : "abcdef".indexOf(k);
      if (k.length === 1 && i >= 0) {
        var b = $$(".q-opt", q)[i];
        if (b && !b.disabled) { e.preventDefault(); b.click(); }
      }
    });
  }
  function stepper(sec, g, wraps) {
    var cur = 0;
    for (var i = 0; i < g.questions.length; i++) { if (!(S.quiz[g.questions[i].id] && S.quiz[g.questions[i].id].ok)) { cur = i; break; } }
    var nav = h("div", { class: "qnav" });
    var prev = h("button", { type: "button", class: "btn ghost small" }, "&larr; Previous");
    var pills = h("div", { class: "qpills", role: "tablist", "aria-label": g.group + " questions" });
    var next = h("button", { type: "button", class: "btn small" }, "Next &rarr;");
    var pillBtns = g.questions.map(function (q, i) {
      var b = h("button", { type: "button", class: "qpill", role: "tab", "aria-label": "Question " + (i + 1) }, String(i + 1));
      b.addEventListener("click", function () { go(i, true); });
      pills.appendChild(b);
      return b;
    });
    nav.appendChild(prev); nav.appendChild(pills); nav.appendChild(next);
    sec.appendChild(nav);
    function paint() {
      pillBtns.forEach(function (b, i) {
        var ok = qState(g.questions[i].id).ok;
        b.classList.toggle("ok", !!ok); b.classList.toggle("on", i === cur); b.setAttribute("aria-selected", i === cur ? "true" : "false");
      });
      prev.disabled = cur === 0;
      next.disabled = cur === wraps.length - 1;
      next.classList.toggle("pulse", !!qState(g.questions[cur].id).ok && cur < wraps.length - 1);
    }
    function go(i, focus) {
      cur = Math.max(0, Math.min(wraps.length - 1, i));
      wraps.forEach(function (w, j) { w.hidden = j !== cur; });
      paint();
      if (focus) { var t = $(".q-text", wraps[cur]); if (t) { t.setAttribute("tabindex", "-1"); t.focus({ preventScroll: true }); } }
    }
    prev.addEventListener("click", function () { go(cur - 1, true); });
    next.addEventListener("click", function () { go(cur + 1, true); });
    sec.addEventListener("qsolved", function () { paint(); });
    go(cur, false);
    return { go: go };
  }

  /* ============================================================ boss */
  function initBoss() {
    var host = $("[data-boss]"), B = PAGE.boss;
    if (!host || !B) return;
    var card = h("div", { class: "boss-card" });
    host.appendChild(card);
    var step = 0, hearts = 3;
    function start(focus) { step = 0; hearts = 3; draw(focus); }
    function meta() {
      var hs = ""; for (var i = 0; i < 3; i++) hs += icon("heart", i < hearts ? "" : "lost");
      var pr = B.steps.map(function (s, i) { return "<i class=\"" + (i < step ? "ok" : i === step ? "cur" : "") + "\"></i>"; }).join("");
      return '<div class="boss-meta"><div class="hearts" aria-label="' + hearts + ' hearts left">' + hs + '</div><div class="boss-progress" aria-label="Step ' + (step + 1) + " of " + B.steps.length + '">' + pr + "</div></div>";
    }
    function draw(focus) {
      if (S.boss[dkey] && step >= B.steps.length) return win(false, focus);
      var s = B.steps[step];
      card.innerHTML = "<h3>" + icon("sword") + esc(B.title) + "</h3>" + meta() + (step === 0 ? '<p class="boss-scn">' + B.scenario + "</p>" : "") +
        '<p class="boss-q">' + s.q + "</p>";
      var opts = h("div", { class: "q-opts" });
      var fb = h("div", { class: "q-feedback" });
      shuffle(s.options.map(function (o, i) { return i; })).forEach(function (i, pos) {
        var o = s.options[i];
        var b = h("button", { type: "button", class: "q-opt" }, '<span class="key">' + "ABCDEF"[pos] + "</span><span>" + o + "</span>");
        b.addEventListener("click", function () {
          if (i === s.answer) {
            b.classList.add("right");
            $$("button", opts).forEach(function (x) { x.disabled = true; });
            fb.className = "q-feedback show good";
            fb.innerHTML = "<b class=\"verdict\">Good call.</b> " + (s.feedback[i] || "").replace(/^Right\.\s*/, "") + " " + citeHTML(s.cites);
            var nb = h("button", { type: "button", class: "btn small", style: "margin-top:12px" }, step === B.steps.length - 1 ? "Finish" : "Next step &rarr;");
            nb.addEventListener("click", function () { step++; if (step >= B.steps.length) win(true, true); else draw(true); });
            fb.appendChild(h("div", null)).appendChild(nb);
            nb.focus();
          } else {
            b.classList.add("wrong"); b.disabled = true;
            hearts--;
            fb.className = "q-feedback show bad";
            fb.innerHTML = "<b class=\"verdict\">Ouch.</b> " + (s.feedback[i] || "That is not the right call.") + (hearts > 0 ? " Try again." : "");
            $(".hearts", card).outerHTML = meta().match(/<div class="hearts"[\s\S]*?<\/div>/)[0];
            if (hearts <= 0) {
              $$("button", opts).forEach(function (x) { x.disabled = true; });
              var rb = h("button", { type: "button", class: "btn small", style: "margin-top:12px" }, icon("reset") + " Out of hearts: start again");
              rb.addEventListener("click", function () { start(true); });
              fb.appendChild(h("div", null)).appendChild(rb);
            }
          }
        });
        opts.appendChild(b);
      });
      card.appendChild(opts); card.appendChild(fb);
      if (focus) {
        var qh = $(".boss-q", card);
        if (qh) { qh.setAttribute("tabindex", "-1"); qh.focus(); }
      }
    }
    card.addEventListener("keydown", function (e) {
      if (e.ctrlKey || e.metaKey || e.altKey) return;
      var k = e.key.toLowerCase(), i = /^[1-6]$/.test(k) ? +k - 1 : "abcdef".indexOf(k);
      if (k.length !== 1 || i < 0) return;
      var b = $$(".q-opts .q-opt", card)[i];
      if (b && !b.disabled) { e.preventDefault(); b.click(); }
    });
    function win(fresh, focus) {
      card.innerHTML = "<h3>" + icon("sword") + esc(B.title) + '</h3><div class="boss-win">' + icon("trophy") + "<h3 style=\"justify-content:center\">Boss defeated</h3><p>" + B.win + "</p></div>";
      var again = h("button", { type: "button", class: "btn small" }, icon("reset") + " Play again");
      again.addEventListener("click", function () { start(true); });
      card.appendChild(h("p", { style: "text-align:center" })).appendChild(again);
      if (focus) again.focus();
      if (fresh) {
        if (!S.boss[dkey]) { S.boss[dkey] = true; save(); confetti(); }
        award(dkey + "-boss", 100, "boss defeated");
        refreshDay();
      }
    }
    if (S.boss[dkey]) { step = B.steps.length; win(false); } else start();
  }

  /* ============================================================ flashcards */
  function initFlash() {
    var host = $("[data-flashcards]"), T = PAGE.terms || [];
    if (!host || !T.length) return;
    var known = S.cards[dkey] || (S.cards[dkey] = {});
    var order = T.map(function (t, i) { return i; });
    var pos = 0, flipped = false;
    var wrap = h("div", { class: "flash" });
    var card = h("div", { class: "flash-card", role: "group", "aria-roledescription": "flashcard" });
    var inner = h("div", { class: "flash-inner" });
    var front = h("div", { class: "flash-face flash-front" });
    var back = h("div", { class: "flash-face flash-back", "aria-live": "polite" });
    inner.appendChild(front); inner.appendChild(back); card.appendChild(inner);
    var cite = h("p", { class: "flash-cite cite-line" });
    var navEl = h("div", { class: "flash-nav" });
    var flip = h("button", { type: "button", class: "btn small", "aria-pressed": "false" }, "Flip");
    var prev = h("button", { type: "button", class: "btn ghost small", "aria-label": "Previous card" }, "&larr;");
    var next = h("button", { type: "button", class: "btn ghost small", "aria-label": "Next card" }, "&rarr;");
    var mix = h("button", { type: "button", class: "btn ghost small" }, "Shuffle");
    var again = h("button", { type: "button", class: "btn ghost small" }, "Again");
    var got = h("button", { type: "button", class: "btn small" }, "Got it");
    var count = h("span", { class: "flash-count", "aria-live": "polite" });
    var left = h("div", { class: "flash-know" }); [prev, next, mix].forEach(function (x) { left.appendChild(x); });
    var right = h("div", { class: "flash-know" }); [flip, again, got].forEach(function (x) { right.appendChild(x); });
    navEl.appendChild(left); navEl.appendChild(count); navEl.appendChild(right);
    wrap.appendChild(card); wrap.appendChild(cite); wrap.appendChild(navEl);
    host.appendChild(wrap);
    function setFlip(v) {
      flipped = v;
      card.classList.toggle("flipped", v);
      flip.setAttribute("aria-pressed", v ? "true" : "false");
      back.setAttribute("aria-hidden", v ? "false" : "true");
      front.setAttribute("aria-hidden", v ? "true" : "false");
      back.innerHTML = v ? "<p>" + T[order[pos]].def + "</p>" : "<p>" + T[order[pos]].def + "</p>";
    }
    function draw() {
      var t = T[order[pos]];
      card.setAttribute("aria-label", "Flashcard " + (pos + 1) + " of " + T.length + ": " + t.term);
      front.innerHTML = '<small>Term ' + (pos + 1) + " of " + T.length + '</small><span class="term-word">' + esc(t.term) + "</span><small>Press Flip (or click the card) to see the definition</small>";
      back.innerHTML = "<p>" + t.def + "</p>";
      cite.innerHTML = citeHTML(t.cites);
      cite.hidden = true;
      setFlip(false);
      var k = Object.keys(known).filter(function (x) { return known[x] === "known"; }).length;
      count.textContent = k + " of " + T.length + " known";
    }
    flip.addEventListener("click", function () { setFlip(!flipped); cite.hidden = !flipped; });
    card.addEventListener("click", function () { setFlip(!flipped); cite.hidden = !flipped; });
    prev.addEventListener("click", function () { pos = (pos - 1 + T.length) % T.length; draw(); });
    next.addEventListener("click", function () { pos = (pos + 1) % T.length; draw(); });
    again.addEventListener("click", function () { known[order[pos]] = "again"; save(); pos = (pos + 1) % T.length; draw(); refreshDay(); });
    got.addEventListener("click", function () {
      known[order[pos]] = "known"; save();
      award(dkey + "-card-" + order[pos], 2, null);
      pos = (pos + 1) % T.length; draw(); refreshDay();
      var kk = Object.keys(known).filter(function (x) { return known[x] === "known"; }).length;
      if (kk === T.length) award(dkey + "-cards-all", 20, "all flashcards known");
    });
    mix.addEventListener("click", function () { order = shuffle(order); pos = 0; draw(); });
    draw();
  }

  /* ============================================================ glossary */
  function initGlossary() {
    var f = $("[data-gl-filter]");
    if (!f) return;
    f.addEventListener("input", function () {
      var q = f.value.trim().toLowerCase();
      $$(".gl-group").forEach(function (g) {
        var any = false;
        $$(".gl-item", g).forEach(function (it) {
          var show = !q || it.textContent.toLowerCase().indexOf(q) >= 0;
          it.hidden = !show; if (show) any = true;
        });
        g.hidden = !any;
      });
    });
  }

  /* ============================================================ boot */
  function boot() {
    initTheme(); initCopy(); initSearch(); initDataTools(); renderHUD(false); showStorageNote();
    if (PAGE.kind === "home") initHome();
    if (PAGE.kind === "glossary") initGlossary();
    if (PAGE.kind === "day") {
      DAY = PAGE.day; dkey = "d" + DAY;
      var ln = $("[data-locked-note]"); if (ln && !unlocked(DAY) && !S.done[dkey]) ln.hidden = false;
      initToc(); initPrereq(); initDeck(); initTerminal(); initLabs(); initQuiz(); initBoss(); initFlash();
      refreshDay();
      activateTarget = dayActivate;
      window.addEventListener("hashchange", function () { dayActivate(location.hash); });
    }
    document.documentElement.setAttribute("data-js-ready", "1");
  }
  /* Read-only hooks for the site's own self-check (check.py); they change nothing. */
  window.kvm10Test = { loose: loose, buildKey: buildKey };
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", boot); else boot();
})();
