// ReturnIt website: small enhancements only. Without this file the page is complete and readable.
(function () {
  var root = document.documentElement;
  var reduce = window.matchMedia('(prefers-reduced-motion: reduce)').matches;
  root.classList.add('js');

  // Navigation: solid background once the page scrolls.
  var nav = document.querySelector('.nav');
  function onScroll() { if (nav) nav.classList.toggle('scrolled', window.scrollY > 8); }
  onScroll();
  window.addEventListener('scroll', onScroll, { passive: true });

  // Mobile menu.
  var btn = document.querySelector('.menu-btn');
  var menu = document.getElementById('menu');
  function setMenu(open) {
    if (!btn || !menu) return;
    btn.setAttribute('aria-expanded', open ? 'true' : 'false');
    menu.classList.toggle('open', open);
    if (open) { var first = menu.querySelector('a'); if (first) first.focus(); }
  }
  if (btn && menu) {
    btn.addEventListener('click', function () { setMenu(btn.getAttribute('aria-expanded') !== 'true'); });
    menu.addEventListener('click', function (e) { if (e.target.closest('a')) setMenu(false); });
    document.addEventListener('keydown', function (e) {
      if (e.key === 'Escape' && btn.getAttribute('aria-expanded') === 'true') { setMenu(false); btn.focus(); }
    });
    window.matchMedia('(min-width: 960px)').addEventListener('change', function (m) { if (m.matches) setMenu(false); });
  }

  // A link to a <details> (#sha-faq, #install-faq...) opens it.
  function openTarget() {
    var id = decodeURIComponent(location.hash.slice(1));
    var el = id && document.getElementById(id);
    if (el && el.tagName === 'DETAILS') el.open = true;
  }
  openTarget();
  window.addEventListener('hashchange', openTarget);

  if (reduce || !('IntersectionObserver' in window)) return;

  // Sections rise into view. Anything already on screen is shown at once, so nothing blinks.
  var items = document.querySelectorAll('[data-reveal]');
  var vh = window.innerHeight;
  items.forEach(function (el) { if (el.getBoundingClientRect().top < vh * 0.92) el.classList.add('in'); });
  root.classList.add('reveal-on');
  var io = new IntersectionObserver(function (entries) {
    entries.forEach(function (e) { if (e.isIntersecting) { e.target.classList.add('in'); io.unobserve(e.target); } });
  }, { rootMargin: '0px 0px -8% 0px' });
  items.forEach(function (el) { if (!el.classList.contains('in')) io.observe(el); });

  // Screenshots drift a little against the scroll (alternate phones in opposite directions).
  var shots = document.querySelector('.shots');
  if (shots && window.matchMedia('(min-width: 768px)').matches) {
    var list = shots.querySelectorAll('.shot');
    var ticking = false;
    function drift() {
      ticking = false;
      var r = shots.getBoundingClientRect();
      var p = (r.top + r.height / 2 - vh / 2) / vh; // -1..1 around the middle of the screen
      if (p < -1.2 || p > 1.2) return;
      list.forEach(function (s, i) { s.style.setProperty('--py', (p * (i % 2 ? -14 : 14)).toFixed(1) + 'px'); });
    }
    window.addEventListener('scroll', function () { if (!ticking) { ticking = true; requestAnimationFrame(drift); } }, { passive: true });
    drift();
  }
})();
