// Shared by the landing page and the docs: theme toggle and copy buttons.
(function () {
  var root = document.documentElement;
  function current() {
    return root.getAttribute('data-theme') || (matchMedia('(prefers-color-scheme: light)').matches ? 'light' : 'dark');
  }
  document.addEventListener('click', function (e) {
    var t = e.target.closest && e.target.closest('[data-theme-toggle]');
    if (t) {
      var next = current() === 'light' ? 'dark' : 'light';
      root.setAttribute('data-theme', next);
      try { localStorage.setItem('ah-theme', next); } catch (err) {}
      return;
    }
    var c = e.target.closest && e.target.closest('.copy');
    if (!c) return;
    var text = c.getAttribute('data-copy');
    if (text === null) {
      var pre = c.parentNode.querySelector('pre');
      text = pre ? pre.innerText.replace(/^\$ /gm, '') : '';
    }
    text = text.replace(/\s+$/, '');
    function ok() { c.textContent = 'Copied'; c.classList.add('done'); setTimeout(function () { c.textContent = 'Copy'; c.classList.remove('done'); }, 1600); }
    if (navigator.clipboard && navigator.clipboard.writeText) navigator.clipboard.writeText(text).then(ok, fallback);
    else fallback();
    function fallback() {
      var ta = document.createElement('textarea'); ta.value = text; ta.style.position = 'fixed'; ta.style.opacity = '0';
      document.body.appendChild(ta); ta.select();
      try { document.execCommand('copy'); ok(); } catch (err) {}
      document.body.removeChild(ta);
    }
  });
})();
