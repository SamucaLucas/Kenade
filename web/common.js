// ============================================================================
// KENADE — Utilitários Compartilhados, Tema Escuro Padrão, Header e Combobox @
// ============================================================================

function getActiveColorScheme() {
  const meta = document.querySelector('meta[name="color-scheme"]');
  return meta ? meta.content : 'dark';
}

function setColorScheme(scheme) {
  const meta = document.querySelector('meta[name="color-scheme"]');
  if (meta) {
    meta.content = scheme;
  }
  try {
    localStorage.setItem('color-scheme', scheme);
  } catch {}
  updateThemeButtonLabel();
}

function toggleColorScheme() {
  const current = getActiveColorScheme();
  const next = current === 'light' ? 'dark' : 'light';
  setColorScheme(next);
}

function updateThemeButtonLabel() {
  const isLight = getActiveColorScheme() === 'light';
  const label = isLight ? '🌙 Modo Escuro' : '☀️ Modo Claro';
  document.querySelectorAll('#theme-toggle-btn, .theme-toggle-action').forEach((btn) => {
    btn.textContent = label;
  });
}

// Inicializa um Navigation Drawer lateral (<dialog class="Drawer">) seguindo Modern Web Guidance: navigation-drawer
function setupNavigationDrawer(drawerEl, openBtnEl) {
  if (!drawerEl || !openBtnEl) return;
  const scroller = drawerEl.querySelector('.Drawer-scroller');
  const sheet = drawerEl.querySelector('.Drawer-sheet');
  const closeBtn = drawerEl.querySelector('.drawer-close-btn');
  if (!scroller || !sheet) return;

  if (!CSS.supports('animation-timeline: scroll()')) {
    scroller.addEventListener('scroll', () => {
      const ratio = 1 - scroller.scrollLeft / (sheet.offsetWidth || 1);
      drawerEl.style.setProperty('--drawer-backdrop', Math.max(0, Math.min(1, ratio)));
    });
  }

  async function openDrawer() {
    drawerEl.showModal();
    if (!CSS.supports('scroll-initial-target', 'nearest')) {
      scroller.scrollTo({ left: scroller.offsetWidth, behavior: 'instant' });
      await new Promise((r) => requestAnimationFrame(() => requestAnimationFrame(r)));
    }
    scroller.scrollTo({ left: 0, behavior: 'auto' });
  }

  function closeDrawer() {
    scroller.scrollTo({ left: scroller.offsetWidth, behavior: 'auto' });
  }

  function onDrawerOpened() {
    openBtnEl.setAttribute('aria-expanded', 'true');
  }

  function onDrawerClosed() {
    if (drawerEl.open) {
      drawerEl.close();
    }
    openBtnEl.setAttribute('aria-expanded', 'false');
  }

  const visibleThreshold = 1 / Math.max(320, window.innerWidth);
  const observer = new IntersectionObserver(
    (entries) => {
      const entry = entries.at(-1);
      if (!entry) return;
      if (entry.intersectionRatio < visibleThreshold) onDrawerClosed();
      if (entry.intersectionRatio === 1) onDrawerOpened();
    },
    { root: drawerEl, threshold: [visibleThreshold, 1] }
  );
  observer.observe(sheet);

  openBtnEl.addEventListener('click', openDrawer);
  if (closeBtn) closeBtn.addEventListener('click', closeDrawer);

  drawerEl.addEventListener('click', (event) => {
    if (!sheet.contains(event.target)) closeDrawer();
  });

  drawerEl.addEventListener('cancel', (event) => {
    if (event.cancelable) {
      event.preventDefault();
      closeDrawer();
    }
  });

  return { openDrawer, closeDrawer };
}

window.addEventListener('storage', (e) => {
  if (e.key === 'color-scheme') {
    const meta = document.querySelector('meta[name="color-scheme"]');
    if (meta) {
      meta.content = e.newValue || 'dark';
      updateThemeButtonLabel();
    }
  }
});

  // Fallback oficial Modern Web Guidance (`dialog-closedby`) para light-dismiss de <dialog closedby="any">
function initDialogLightDismissFallback() {
  for (const dialog of document.querySelectorAll('dialog:not(.Drawer)')) {
    dialog.addEventListener('close', () => {
      dialog.returnValue = '';
    });
  }
  if (!('closedBy' in HTMLDialogElement.prototype)) {
    const outside = (e, dialog) => {
      const r = dialog.getBoundingClientRect();
      return e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom;
    };
    for (const dialog of document.querySelectorAll('dialog[closedby="any"]:not(.Drawer)')) {
      let pressedOutside = false;
      dialog.addEventListener('pointerdown', (e) => {
        pressedOutside = outside(e, dialog);
      });
      dialog.addEventListener('pointerup', (e) => {
        if (pressedOutside && outside(e, dialog)) dialog.close();
      });
    }
  }
}

function showToast(message, type = 'info') {
  let container = document.getElementById('toast-container');
  if (!container) {
    container = document.createElement('div');
    container.id = 'toast-container';
    container.className = 'toast-container';
    container.setAttribute('aria-live', 'polite');
    document.body.appendChild(container);
  }
  const el = document.createElement('div');
  el.className = `toast ${type}`;
  el.textContent = message;
  container.appendChild(el);
  setTimeout(() => {
    el.remove();
  }, 3800);
}

async function apiFetch(path, options = {}) {
  const headers = { 'Content-Type': 'application/json', ...(options.headers || {}) };
  const token = localStorage.getItem('kenade_token');
  if (token) {
    headers['Authorization'] = `Bearer ${token}`;
  }
  const resp = await fetch(path, { ...options, headers });
  const data = await resp.json().catch(() => ({}));
  if (resp.status === 401 && window.location.pathname !== '/login') {
    window.location.href = '/login';
    throw new Error('Sessão expirada');
  }
  if (!resp.ok) {
    throw new Error(data.error || `Erro HTTP ${resp.status}`);
  }
  return data;
}

function escapeHTML(str) {
  return String(str || '')
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}

// Renderiza o cabeçalho compartilhado nas páginas internas e oculta tudo o que é de Admin para jogadores comuns
async function initAuthenticatedPage(activeNavId) {
  initDialogLightDismissFallback();
  updateThemeButtonLabel();

  const themeBtn = document.getElementById('theme-toggle-btn');
  if (themeBtn) {
    themeBtn.addEventListener('click', toggleColorScheme);
  }

  const logoutBtn = document.getElementById('logout-btn');
  if (logoutBtn) {
    logoutBtn.addEventListener('click', async () => {
      await apiFetch('/api/auth/logout', { method: 'POST' }).catch(() => {});
      localStorage.removeItem('kenade_token');
      window.location.href = '/login';
    });
  }

  if (activeNavId) {
    const activeLink = document.querySelector(`[data-nav="${activeNavId}"]`);
    if (activeLink) activeLink.classList.add('active');
  }

  // Renderiza o cabeçalho instantaneamente (0ms) usando cache da sessão enquanto valida em background
  try {
    const cached = sessionStorage.getItem('kenade_user_cache');
    if (cached) {
      const parsed = JSON.parse(cached);
      if (parsed && parsed.user) {
        applyUserToHeader(parsed.user, activeNavId);
      }
    }
  } catch {}

  const data = await apiFetch('/api/auth/me');
  try {
    sessionStorage.setItem('kenade_user_cache', JSON.stringify(data));
  } catch {}

  applyUserToHeader(data.user, activeNavId);
  return data;
}

function applyUserToHeader(user, activeNavId) {
  if (!user) return;
  const isAdmin = user.role === 'admin';

  // Injeta automaticamente a aba lateral (Drawer) para celular nas páginas com .app-header
  const headerInner = document.querySelector('.app-header .header-inner');
  if (headerInner && !document.getElementById('global-mobile-drawer')) {
    const menuBtn = document.createElement('button');
    menuBtn.type = 'button';
    menuBtn.id = 'global-drawer-open';
    menuBtn.className = 'drawer-trigger-btn mobile-menu-toggle-btn hidden';
    menuBtn.setAttribute('aria-label', 'Abrir menu lateral');
    menuBtn.setAttribute('aria-expanded', 'false');
    menuBtn.setAttribute('aria-controls', 'global-mobile-drawer');
    menuBtn.innerHTML = '☰ Menu';
    headerInner.appendChild(menuBtn);

    const drawer = document.createElement('dialog');
    drawer.className = 'Drawer';
    drawer.id = 'global-mobile-drawer';
    drawer.setAttribute('aria-label', 'Menu de navegação');
    drawer.innerHTML = `
      <div class="Drawer-scroller">
        <nav class="Drawer-sheet">
          <div class="drawer-header-bar">
            <div style="display: flex; align-items: center; gap: 0.65rem;">
              <img src="/favicon.svg" alt="Logo Kenade" width="34" height="34" style="border-radius: 8px;" />
              <div>
                <div style="font-weight: 900; font-size: 1.15rem;">KENADE</div>
                <div style="font-size: 0.78rem; opacity: 0.9;">${escapeHTML(user.name)} (@${escapeHTML(user.username)})</div>
              </div>
            </div>
            <button type="button" class="nav-btn drawer-close-btn" aria-label="Fechar menu" autofocus>✕</button>
          </div>
          <ul class="drawer-menu-list">
            <li><a class="drawer-menu-link ${activeNavId === 'dashboard' ? 'active' : ''}" href="/">🏠 Início &amp; Guia</a></li>
            <li><a class="drawer-menu-link ${activeNavId === 'arena' ? 'active' : ''}" href="/arena">🎮 Salas Arena (Equipes)</a></li>
            <li><a class="drawer-menu-link ${activeNavId === 'solo' ? 'active' : ''}" href="/solo">📱 Estudo Solo (Celular)</a></li>
            <li><a class="drawer-menu-link ${activeNavId === 'ranking' ? 'active' : ''}" href="/ranking">🏆 Rankings</a></li>
            ${isAdmin ? `<li><a class="drawer-menu-link ${activeNavId === 'questions' ? 'active' : ''}" href="/questions">⚙️ Banco de Questões</a></li>` : ''}
            <li><button type="button" class="drawer-menu-link theme-toggle-action">☀️ Modo Claro</button></li>
            <li><button type="button" class="drawer-menu-link" id="drawer-logout-btn" style="color: var(--color-crimson);">🚪 Sair da Conta</button></li>
          </ul>
        </nav>
      </div>
    `;
    document.body.appendChild(drawer);
    updateThemeButtonLabel();

    drawer.querySelector('.theme-toggle-action').addEventListener('click', toggleColorScheme);
    drawer.querySelector('#drawer-logout-btn').addEventListener('click', async () => {
      await apiFetch('/api/auth/logout', { method: 'POST' }).catch(() => {});
      localStorage.removeItem('kenade_token');
      sessionStorage.removeItem('kenade_user_cache');
      window.location.href = '/login';
    });

    setupNavigationDrawer(drawer, menuBtn);
  }

  // Regra estrita: O que é de Admin NÃO aparece para usuário comum / jogador!
  document.querySelectorAll('[data-nav="questions"], .admin-only').forEach((el) => {
    el.classList.toggle('hidden', !isAdmin);
  });

  const userNameEl = document.getElementById('header-user-name');
  const userRoleEl = document.getElementById('header-user-role');
  if (userNameEl) userNameEl.textContent = user.name;
  if (userRoleEl) {
    userRoleEl.textContent = isAdmin ? 'ADMIN' : 'JOGADOR';
    userRoleEl.className = `role-tag ${user.role}`;
  }
}

// Inicializa um Combobox de Pesquisa por @username
function createUserSearchCombobox({ inputEl, dropdownEl, onSelectUser, filterFn }) {
  let debounceTimer = null;

  const runSearch = async (query) => {
    try {
      const data = await apiFetch(`/api/users?q=${encodeURIComponent(query)}`);
      let users = data.users || [];
      if (filterFn) {
        users = users.filter(filterFn);
      }

      dropdownEl.innerHTML = '';
      if (users.length === 0) {
        dropdownEl.innerHTML = `<div class="combobox-item text-slate" style="cursor:default;">Nenhum @usuário encontrado</div>`;
        dropdownEl.classList.remove('hidden');
        return;
      }

      for (const u of users) {
        const item = document.createElement('div');
        item.className = 'combobox-item';
        item.innerHTML = `
          <div>
            <strong>@${escapeHTML(u.username)}</strong>
            <span class="text-slate" style="margin-left: 0.35rem; font-size: 0.86rem;">(${escapeHTML(u.name)})</span>
          </div>
          <span class="role-tag ${u.role}">${u.role === 'admin' ? 'ADMIN' : 'JOGADOR'}</span>
        `;
        item.addEventListener('click', () => {
          onSelectUser(u);
          inputEl.value = '';
          dropdownEl.classList.add('hidden');
        });
        dropdownEl.appendChild(item);
      }
      dropdownEl.classList.remove('hidden');
    } catch (err) {
      showToast(err.message, 'error');
    }
  };

  inputEl.addEventListener('focus', () => {
    runSearch(inputEl.value.trim());
  });

  inputEl.addEventListener('input', () => {
    clearTimeout(debounceTimer);
    debounceTimer = setTimeout(() => {
      runSearch(inputEl.value.trim());
    }, 150);
  });

  document.addEventListener('click', (e) => {
    if (!inputEl.contains(e.target) && !dropdownEl.contains(e.target)) {
      dropdownEl.classList.add('hidden');
    }
  });
}
