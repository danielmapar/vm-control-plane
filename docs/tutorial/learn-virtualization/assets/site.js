/* Optional enhancements. All lessons and navigation also work without JavaScript. */
(() => {
  'use strict';
  const $ = selector => document.querySelector(selector);
  const $$ = selector => [...document.querySelectorAll(selector)];
  const storage = {
    get(key, fallback) {
      try { return JSON.parse(localStorage.getItem(key)) ?? fallback; }
      catch { return fallback; }
    },
    set(key, value) {
      try { localStorage.setItem(key, JSON.stringify(value)); return true; }
      catch { return false; }
    }
  };
  let toastTimer;
  function notify(message) {
    $('.toast').textContent = message;
    $('.toast').classList.add('visible');
    clearTimeout(toastTimer);
    toastTimer = setTimeout(() => $('.toast').classList.remove('visible'), 3000);
  }

  const themeButton = $('.theme-toggle');
  function setTheme(theme) {
    document.documentElement.dataset.theme = theme;
    themeButton.setAttribute('aria-label', `Switch to ${theme === 'dark' ? 'light' : 'dark'} theme`);
  }
  setTheme(storage.get('vmc-learn-theme', 'light') === 'dark' ? 'dark' : 'light');
  themeButton.addEventListener('click', () => {
    const theme = document.documentElement.dataset.theme === 'dark' ? 'light' : 'dark';
    setTheme(theme);
    storage.set('vmc-learn-theme', theme);
  });

  const menuButton = $('.menu-toggle');
  function closeMenu() {
    document.body.classList.remove('nav-open');
    $('#main').inert = false;
    menuButton.setAttribute('aria-expanded', 'false');
    menuButton.setAttribute('aria-label', 'Open chapter navigation');
  }
  menuButton.addEventListener('click', () => {
    const open = document.body.classList.toggle('nav-open');
    $('#main').inert = open;
    menuButton.setAttribute('aria-expanded', String(open));
    menuButton.setAttribute('aria-label', open ? 'Close chapter navigation' : 'Open chapter navigation');
  });
  matchMedia('(min-width: 801px)').addEventListener('change', event => {
    if (event.matches) closeMenu();
  });

  $$('.copy-code').forEach(button => {
    button.addEventListener('click', async () => {
      const code = button.closest('.code-block').querySelector('code');
      try {
        await navigator.clipboard.writeText(code.textContent);
        button.textContent = 'Copied ✓';
        notify('Code copied to clipboard');
        setTimeout(() => { button.textContent = 'Copy code'; }, 1800);
      } catch {
        const range = document.createRange();
        range.selectNodeContents(code);
        const selection = window.getSelection();
        selection.removeAllRanges();
        selection.addRange(range);
        notify('Code selected. Use your keyboard’s copy shortcut.');
      }
    });
  });

  const chapter = document.body.dataset.chapter;
  const progressKey = 'vmc-learn-completed-v1';
  const saved = storage.get(progressKey, []);
  const completed = new Set((Array.isArray(saved) ? saved : []).filter(id => /^chapter-(0\d|1\d|2[0-4])$/.test(id)));
  const completeButton = $('.complete-button');
  function updateProgress() {
    $$('[data-done]').forEach(element => { element.textContent = completed.has(element.dataset.done) ? '✓' : ''; });
    if (completeButton) {
      const done = completed.has(chapter);
      completeButton.setAttribute('aria-pressed', String(done));
      completeButton.textContent = done ? 'Chapter completed ✓' : 'Mark chapter complete ✓';
    }
    const strip = $('.reading-progress');
    if (strip && completed.size) {
      strip.hidden = false;
      $('[data-progress-count]').textContent = `${completed.size} / 25 chapters`;
      const next = Array.from({ length: 25 }, (_, i) => `chapter-${String(i).padStart(2, '0')}`).find(id => !completed.has(id));
      $('[data-resume]').href = next ? `${next}.html` : 'validation.html';
      $('[data-resume]').textContent = next ? 'Continue learning →' : 'Review your evidence →';
    }
  }
  if (completeButton) completeButton.addEventListener('click', () => {
    completed.has(chapter) ? completed.delete(chapter) : completed.add(chapter);
    const persisted = storage.set(progressKey, [...completed]);
    updateProgress();
    notify(persisted ? 'Progress saved on this browser' : 'Progress updated for this page; browser storage is unavailable');
  });
  updateProgress();

  const dialog = $('.search-dialog');
  const searchInput = $('#course-search');
  const results = $('.search-results');
  const status = $('.search-status');
  let index;
  let loading;
  let searchTimer;
  async function loadIndex() {
    if (index) return index;
    if (!loading) loading = fetch('search-index.json')
      .then(response => {
        if (!response.ok) throw new Error('Search index unavailable');
        return response.json();
      })
      .then(data => {
        index = data.map(item => ({ ...item, haystack: `${item.title} ${item.section} ${item.text}`.toLowerCase() }));
        return index;
      })
      .catch(error => { loading = null; throw error; });
    return loading;
  }
  async function search() {
    const query = searchInput.value.trim().toLowerCase();
    results.replaceChildren();
    if (!query) { status.textContent = 'Search across every chapter and reference.'; return; }
    status.textContent = 'Searching…';
    try {
      const data = await loadIndex();
      if (searchInput.value.trim().toLowerCase() !== query) return;
      const terms = query.split(/\s+/);
      const matches = data.filter(item => terms.every(term => item.haystack.includes(term)))
        .map(item => ({ ...item, score: (item.section.toLowerCase().includes(query) ? 10 : 0) + (item.title.toLowerCase().includes(query) ? 5 : 0) + (item.text.toLowerCase().includes(query) ? 2 : 0) }))
        .sort((a, b) => b.score - a.score);
      status.textContent = matches.length ? `${matches.length} matching sections${matches.length > 20 ? ' · showing the first 20' : ''}` : 'No matches. Try a shorter term, such as “KVM” or “backup”.';
      matches.slice(0, 20).forEach(item => {
        const link = document.createElement('a');
        link.className = 'search-result';
        link.href = item.url;
        const title = document.createElement('small');
        title.textContent = item.title;
        const heading = document.createElement('strong');
        heading.textContent = item.section;
        const snippet = document.createElement('p');
        const at = item.text.toLowerCase().indexOf(terms[0]);
        const start = Math.max(0, at - 65);
        snippet.textContent = (start ? '…' : '') + item.text.slice(start, start + 200) + (item.text.length > start + 200 ? '…' : '');
        link.append(title, heading, snippet);
        link.addEventListener('click', () => dialog.close());
        results.append(link);
      });
    } catch { status.textContent = 'Search could not load. Use the chapter navigation, or try again.'; }
  }
  function openSearch() {
    if (!dialog.open) dialog.showModal();
    searchInput.focus();
    searchInput.select();
    search();
  }
  $('[data-search-open]').addEventListener('click', openSearch);
  $('[data-search-close]').addEventListener('click', () => dialog.close());
  dialog.addEventListener('click', event => {
    if (event.target !== dialog) return;
    const box = dialog.getBoundingClientRect();
    if (event.clientX < box.left || event.clientX > box.right || event.clientY < box.top || event.clientY > box.bottom) dialog.close();
  });
  searchInput.addEventListener('input', () => {
    clearTimeout(searchTimer);
    searchTimer = setTimeout(search, 120);
  });
  document.addEventListener('keydown', event => {
    const editing = event.target.matches('input, textarea, [contenteditable="true"]');
    if ((event.key === '/' && !editing) || ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === 'k')) {
      event.preventDefault();
      openSearch();
    }
    if (event.key === 'Escape' && document.body.classList.contains('nav-open')) {
      closeMenu();
      menuButton.focus();
    }
  });

  if ('IntersectionObserver' in window) {
    const observer = new IntersectionObserver(entries => {
      const visible = entries.filter(entry => entry.isIntersecting);
      if (!visible.length) return;
      const id = visible[0].target.id;
      $$('.page-outline .toc a').forEach(link => link.classList.toggle('active', link.hash === `#${id}`));
    }, { rootMargin: '-90px 0px -65% 0px' });
    $$('.prose h2, .prose h3').forEach(heading => observer.observe(heading));
  }
})();
