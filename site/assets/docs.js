// Docs viewer: renders the Markdown files in this folder, so the same source
// serves the site, GitHub and llms-full.txt.
(function () {
  'use strict';
  var GROUPS = [
    ['Start', [['getting-started', 'Getting started'], ['agent-setup', 'Set up with an AI agent']]],
    ['Learn', [['concepts', 'Concepts'], ['dashboard', 'Dashboard'], ['agents', 'Agents and tools']]],
    ['Reference', [['cli', 'CLI reference'], ['configuration', 'Configuration'], ['api', 'HTTP API'], ['usage-costs', 'Usage and cost']]],
    ['More', [['security', 'Security'], ['troubleshooting', 'Troubleshooting'], ['contributing', 'Contributing']]]
  ];
  var PAGES = [];
  GROUPS.forEach(function (g) { g[1].forEach(function (p) { PAGES.push({ id: p[0], title: p[1] }); }); });
  var byId = {}; PAGES.forEach(function (p) { byId[p.id] = p; });

  var $ = function (s) { return document.querySelector(s); };
  var content = $('#content'), nav = $('#nav'), toc = $('#toc'), pager = $('#pager'), side = $('#side');
  var q = $('#q'), results = $('#results'), menu = $('#menu');
  var cache = {}, index = [], current = null, spy = null;

  /* ---------- navigation ---------- */
  GROUPS.forEach(function (g) {
    var h = document.createElement('h2'); h.textContent = g[0]; nav.appendChild(h);
    g[1].forEach(function (p) {
      var a = document.createElement('a'); a.href = '#' + p[0]; a.textContent = p[1]; a.setAttribute('data-id', p[0]); nav.appendChild(a);
    });
  });

  function parseHash() {
    var h = decodeURIComponent(location.hash.replace(/^#\/?/, ''));
    var parts = h.split('/');
    var id = byId[parts[0]] ? parts[0] : 'getting-started';
    return { id: id, anchor: parts[1] || '' };
  }

  function load(id) {
    if (cache[id]) return Promise.resolve(cache[id]);
    return fetch(id + '.md').then(function (r) {
      if (!r.ok) throw new Error(r.status);
      return r.text();
    }).then(function (t) { cache[id] = t; return t; });
  }

  function slug(s) { return s.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/^-|-$/g, ''); }

  function render(id, anchor) {
    return load(id).then(function (md) {
      current = id;
      content.innerHTML = marked.parse(md, { gfm: true });
      enhance(id);
      document.title = byId[id].title + ' | AgentHub docs';
      Array.prototype.forEach.call(nav.querySelectorAll('a'), function (a) {
        if (a.getAttribute('data-id') === id) a.setAttribute('aria-current', 'page'); else a.removeAttribute('aria-current');
      });
      buildToc(id); buildPager(id);
      if (anchor) { var t = document.getElementById(anchor); if (t) t.scrollIntoView(); else window.scrollTo(0, 0); }
      else window.scrollTo(0, 0);
    }).catch(function () {
      content.innerHTML = '<h1>Page not found</h1><p>Pick a page from the list, or <a href="#getting-started">start with Getting started</a>.</p>';
      toc.innerHTML = ''; pager.innerHTML = '';
    });
  }

  function enhance(id) {
    var seen = {};
    Array.prototype.forEach.call(content.querySelectorAll('h2, h3'), function (h) {
      var s = slug(h.textContent) || 'section', n = s, i = 2;
      while (seen[n]) n = s + '-' + (i++);
      seen[n] = 1; h.id = n;
      var a = document.createElement('a'); a.className = 'anchor'; a.href = '#' + id + '/' + n; a.setAttribute('aria-label', 'Link to this section'); a.textContent = '#';
      h.appendChild(a);
    });
    Array.prototype.forEach.call(content.querySelectorAll('table'), function (t) {
      var w = document.createElement('div'); w.className = 'tablewrap'; t.parentNode.insertBefore(w, t); w.appendChild(t);
    });
    Array.prototype.forEach.call(content.querySelectorAll('pre'), function (p) {
      var w = document.createElement('div'); w.className = 'codewrap'; p.parentNode.insertBefore(w, p); w.appendChild(p);
      var b = document.createElement('button'); b.type = 'button'; b.className = 'copy'; b.textContent = 'Copy'; w.appendChild(b);
    });
    // Links between pages: cli.md#section becomes #cli/section; external links open normally.
    Array.prototype.forEach.call(content.querySelectorAll('a[href]'), function (a) {
      var m = a.getAttribute('href').match(/^([a-z-]+)\.md(?:#(.*))?$/);
      if (m && byId[m[1]]) a.setAttribute('href', '#' + m[1] + (m[2] ? '/' + m[2] : ''));
      else if (/^https?:/.test(a.getAttribute('href'))) { a.rel = 'noopener'; }
    });
  }

  function buildToc(id) {
    var hs = content.querySelectorAll('h2, h3');
    toc.innerHTML = '';
    if (hs.length < 2) return;
    var b = document.createElement('b'); b.textContent = 'On this page'; toc.appendChild(b);
    var links = [];
    Array.prototype.forEach.call(hs, function (h) {
      var a = document.createElement('a'); a.href = '#' + id + '/' + h.id;
      a.textContent = h.firstChild.textContent;
      if (h.tagName === 'H3') a.className = 'sub';
      toc.appendChild(a); links.push([a, h]);
    });
    if (spy) spy.disconnect();
    if (!('IntersectionObserver' in window)) return;
    spy = new IntersectionObserver(function (entries) {
      entries.forEach(function (e) {
        if (!e.isIntersecting) return;
        links.forEach(function (l) { l[0].classList.toggle('on', l[1] === e.target); });
      });
    }, { rootMargin: '-70px 0px -70% 0px' });
    links.forEach(function (l) { spy.observe(l[1]); });
  }

  function buildPager(id) {
    var i = PAGES.findIndex(function (p) { return p.id === id; });
    pager.innerHTML = '';
    function link(p, cls, label) {
      var a = document.createElement('a'); a.href = '#' + p.id; a.className = cls;
      a.innerHTML = '<small>' + label + '</small>' + p.title;
      pager.appendChild(a);
    }
    if (i > 0) link(PAGES[i - 1], 'prev', 'Previous');
    if (i < PAGES.length - 1) link(PAGES[i + 1], 'next', 'Next');
  }

  /* ---------- search ---------- */
  function buildIndex() {
    return Promise.all(PAGES.map(function (p) { return load(p.id).catch(function () { return ''; }); })).then(function () {
      PAGES.forEach(function (p) {
        var heading = p.title, inCode = false;
        (cache[p.id] || '').split('\n').forEach(function (line) {
          if (/^```/.test(line)) { inCode = !inCode; return; }
          var m = line.match(/^(#{1,3})\s+(.*)/);
          if (!inCode && m) { heading = m[2].replace(/[`*]/g, ''); index.push({ page: p, heading: heading, text: heading, head: true }); return; }
          var t = line.replace(/[`*|]/g, ' ').replace(/\[([^\]]*)\]\([^)]*\)/g, '$1').trim();
          if (t.length > 2) index.push({ page: p, heading: heading, text: t });
        });
      });
    });
  }

  function search(term) {
    var words = term.toLowerCase().split(/\s+/).filter(Boolean);
    var best = {};
    index.forEach(function (row) {
      var hay = row.text.toLowerCase(), score = 0;
      for (var i = 0; i < words.length; i++) {
        if (hay.indexOf(words[i]) < 0) return;
        score += row.head ? 5 : 1;
      }
      var key = row.page.id + '/' + row.heading;
      if (!best[key] || best[key].score < score) best[key] = { row: row, score: score };
    });
    return Object.keys(best).map(function (k) { return best[k]; })
      .sort(function (a, b) { return b.score - a.score; }).slice(0, 8);
  }

  function mark(text, words) {
    var esc = text.replace(/[&<>]/g, function (c) { return { '&': '&amp;', '<': '&lt;', '>': '&gt;' }[c]; });
    words.forEach(function (w) {
      var re = new RegExp('(' + w.replace(/[.*+?^${}()|[\]\\]/g, '\\$&') + ')', 'ig');
      esc = esc.replace(re, '<mark>$1</mark>');
    });
    return esc;
  }

  q.addEventListener('input', function () {
    var term = q.value.trim();
    if (!term) { results.hidden = true; results.innerHTML = ''; return; }
    var hits = search(term), words = term.toLowerCase().split(/\s+/).filter(Boolean);
    results.hidden = false;
    if (!hits.length) { results.innerHTML = '<div class="none">Nothing found for “' + mark(term, []) + '”.</div>'; return; }
    results.innerHTML = hits.map(function (h) {
      var r = h.row, target = r.heading === r.page.title ? '' : '/' + slug(r.heading);
      var snip = r.head ? '' : '<span>' + mark(r.text.length > 110 ? r.text.slice(0, 110) + '…' : r.text, words) + '</span>';
      return '<a href="#' + r.page.id + target + '"><b>' + mark(r.heading, words) + '</b>' + r.page.title + (snip ? ' · ' : '') + snip + '</a>';
    }).join('');
  });
  results.addEventListener('click', function () { q.value = ''; results.hidden = true; results.innerHTML = ''; closeMenu(); });
  document.addEventListener('keydown', function (e) {
    if (e.key === '/' && document.activeElement !== q && !/input|textarea/i.test(document.activeElement.tagName)) { e.preventDefault(); q.focus(); }
    if (e.key === 'Escape') { if (document.activeElement === q) { q.value = ''; results.hidden = true; q.blur(); } closeMenu(); }
  });

  /* ---------- mobile menu ---------- */
  function closeMenu() { side.classList.remove('open'); menu.setAttribute('aria-expanded', 'false'); }
  menu.addEventListener('click', function () {
    var open = side.classList.toggle('open'); menu.setAttribute('aria-expanded', open ? 'true' : 'false');
  });
  nav.addEventListener('click', closeMenu);

  /* ---------- routing ---------- */
  function route() {
    var r = parseHash();
    if (r.id === current && r.anchor) { var t = document.getElementById(r.anchor); if (t) { t.scrollIntoView(); return; } }
    if (r.id === current && !r.anchor) { window.scrollTo(0, 0); return; }
    render(r.id, r.anchor);
  }
  window.addEventListener('hashchange', route);
  route();
  buildIndex();
})();
