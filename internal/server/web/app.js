const collapseKey = "dlog.collapsed-groups";

function loadCollapsed() {
  try {
    const raw = JSON.parse(localStorage.getItem(collapseKey) || "[]");
    return new Set(Array.isArray(raw) ? raw.filter((id) => typeof id === "string") : []);
  } catch (e) {
    return new Set();
  }
}

const loginError = document.getElementById("login-error");
if (loginError) {
  const reason = new URLSearchParams(location.search).get("error");
  if (reason === "directory") {
    loginError.textContent = "The directory is unavailable.";
    loginError.hidden = false;
  } else if (reason) {
    loginError.hidden = false;
  }
}

const logoutBtn = document.getElementById("logout");
if (logoutBtn) {
  logoutBtn.addEventListener("click", async () => {
    try { await api("/api/v1/logout", { method: "POST", body: "{}" }); } catch (e) { /* redirect anyway */ }
    location.href = "/login";
  });
}

if (document.getElementById("hosts")) {
  initApp();
}
if (document.getElementById("admin-hosts")) {
  initAdmin();
}
if (document.getElementById("accounts")) {
  initAccounts();
}
if (document.getElementById("change-password")) {
  initAccount();
}

function initApp() {
  const hostsEl = document.getElementById("hosts");
  const hostCountEl = document.getElementById("host-count");
  const hostQEl = document.getElementById("host-q");
  const hostsToggle = document.getElementById("hosts-toggle");
  const chipsEl = document.getElementById("chips");
  const logEl = document.getElementById("log");
  // Rows live in #log. The parent scroller keeps the column header sticky
  // and the same width as the rows, scrollbar included.
  const scroller = document.getElementById("log-scroll");
  const emptyEl = document.getElementById("log-empty");
  const emptyTitle = emptyEl ? emptyEl.querySelector("[data-empty-title]") : null;
  const emptyHint = emptyEl ? emptyEl.querySelector("[data-empty-hint]") : null;
  const metaEl = document.getElementById("logmeta");
  const qInput = document.getElementById("q");
  const streamSel = document.getElementById("stream");
  const followBtn = document.getElementById("follow");
  const resetFiltersBtn = document.getElementById("reset-filters");
  const jumpLatestBtn = document.getElementById("jump-latest");
  const jumpLatestLabel = document.getElementById("jump-latest-label");
  let hosts = [];
  let groups = [];
  let collapsed = loadCollapsed();
  let containers = [];
  let lines = [];
  let hostId = "";
  let containerId = "";
  let q = "";
  let paintedQ = "";
  let stream = "";
  let follow = true;
  let gen = 0;
  let ready = false;
  let polling = false;
  let viewer = null;
  let unread = 0;

  followBtn.addEventListener("click", () => {
    follow = !follow;
    followBtn.classList.toggle("is-on", follow);
    followBtn.setAttribute("aria-pressed", follow ? "true" : "false");
  });
  document.getElementById("older").addEventListener("click", loadOlder);
  streamSel.addEventListener("change", () => { stream = streamSel.value; reload(); });
  const rangeSel = document.getElementById("range");
  const sinceInput = document.getElementById("since");
  let range = "";
  let sinceLocal = "";
  rangeSel.addEventListener("change", () => {
    range = rangeSel.value;
    sinceInput.hidden = range !== "from";
    reload();
  });
  sinceInput.addEventListener("change", () => {
    sinceLocal = sinceInput.value;
    if (range === "from") reload();
  });
  if (resetFiltersBtn) {
    resetFiltersBtn.addEventListener("click", () => {
      containerId = "";
      q = "";
      stream = "";
      range = "";
      sinceLocal = "";
      qInput.value = "";
      streamSel.value = "";
      rangeSel.value = "";
      sinceInput.value = "";
      sinceInput.hidden = true;
      paintChips();
      reload();
    });
  }
  if (jumpLatestBtn) {
    jumpLatestBtn.addEventListener("click", () => {
      pinBottom();
      unread = 0;
      syncJumpButton();
    });
  }
  view().addEventListener("scroll", () => {
    if (nearBottom()) unread = 0;
    syncJumpButton();
  }, { passive: true });
  document.addEventListener("selectionchange", () => {
    (scroller || logEl).style.overflowAnchor = selectingLog() ? "none" : "";
  });
  let qTimer = 0;
  qInput.addEventListener("input", () => {
    clearTimeout(qTimer);
    qTimer = setTimeout(() => {
      const next = qInput.value.trim();
      if (next === q) return;
      q = next;
      reload();
    }, 250);
  });
  qInput.addEventListener("keydown", (e) => {
    if (e.key === "Escape") {
      if (qInput.value) {
        clearTimeout(qTimer);
        qInput.value = "";
        if (q) {
          q = "";
          reload();
        }
      } else {
        qInput.blur();
      }
    }
  });
  hostQEl.addEventListener("input", () => paintHosts());
  if (hostsToggle) {
    const hostsPaneKey = "dlog.hosts-pane";
    const applyHostsPane = (open) => {
      document.documentElement.classList.toggle("hosts-collapsed", !open);
      hostsToggle.setAttribute("aria-expanded", open ? "true" : "false");
      hostsToggle.setAttribute("aria-label", open ? "Hide hosts" : "Show hosts");
      try { localStorage.setItem(hostsPaneKey, open ? "open" : "closed"); } catch (e) { /* keep the choice for this page */ }
    };
    let startClosed = document.documentElement.classList.contains("hosts-collapsed");
    try {
      if (localStorage.getItem(hostsPaneKey) === "closed") startClosed = true;
    } catch (e) {}
    applyHostsPane(!startClosed);
    hostsToggle.addEventListener("click", () => {
      applyHostsPane(document.documentElement.classList.contains("hosts-collapsed"));
    });
  }
  document.addEventListener("keydown", (e) => {
    if (e.key === "/" && !/^(INPUT|TEXTAREA|SELECT)$/.test(document.activeElement.tagName)) {
      e.preventDefault();
      qInput.focus();
    }
  });

  refreshHosts();
  api("/api/v1/meta").then((m) => {
    const version = document.getElementById("version");
    version.textContent = m.version ? "v" + m.version : "";
  }).catch(() => {});
  setInterval(() => { if (!document.hidden) refreshHosts(); }, 5000);
  setInterval(() => { if (!document.hidden) pollLogs(); }, 1000);
  api("/api/v1/me").then((me) => {
    viewer = me;
    if (me && me.role === "admin") showAdminLinks();
    paintHosts();
    paintChips();
  }).catch(() => {});

  async function refreshHosts() {
    try {
      const data = await api("/api/v1/hosts");
      hosts = data.hosts || [];
      groups = (data.groups || []).slice().sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: "base" }) || a.id.localeCompare(b.id));
      const prevHost = hostId;
      if (hostId && !hosts.some((h) => h.id === hostId)) {
        hostId = "";
        containerId = "";
      }
      if (!hostId && hosts.length) {
        hostId = hosts[0].id;
        containerId = "";
      }
      paintHosts();
      if (hostId !== prevHost || (hostId && !ready)) reload();
      if (hostId) refreshContainers();
    } catch (e) {
      if (e.message !== "unauthorized") toast(e.message);
    }
  }

  async function refreshContainers() {
    const myHost = hostId;
    try {
      const data = await api("/api/v1/containers?host_id=" + encodeURIComponent(myHost));
      if (myHost !== hostId) return;
      containers = data.containers || [];
      if (containerId && !containers.some((c) => c.id === containerId)) {
        containerId = "";
        reload();
      }
      paintChips();
    } catch (e) {
      if (e.message !== "unauthorized") toast(e.message);
    }
  }

  function paintHosts() {
    const query = hostQEl.value.trim().toLowerCase();
    const visible = hosts.filter((h) => matchesHost(h, query));
    if (query) hostCountEl.textContent = visible.length + " / " + hosts.length;
    else hostCountEl.textContent = hosts.length ? String(hosts.length) : "";

    hostsEl.replaceChildren();
    const buckets = new Map();
    for (const g of groups) buckets.set(g.id, []);
    const ungrouped = [];
    for (const h of visible) {
      const id = h.group_id || "";
      if (id && buckets.has(id)) buckets.get(id).push(h);
      else ungrouped.push(h);
    }
    let showedGroup = false;
    for (const g of groups) {
      const members = buckets.get(g.id) || [];
      const nameHit = !query || g.name.toLowerCase().includes(query);
      if (query && !nameHit && !members.length) continue;
      showedGroup = true;
      const open = groupOpen(g.id, query);
      hostsEl.append(groupLabel(g.name, members.length, false, g.id, open));
      if (open) for (const h of members) hostsEl.append(hostButton(h));
    }
    if (ungrouped.length && groups.length) {
      showedGroup = true;
      const open = groupOpen("ungrouped", query);
      hostsEl.append(groupLabel("Ungrouped", ungrouped.length, true, "ungrouped", open));
      if (open) for (const h of ungrouped) hostsEl.append(hostButton(h));
    } else {
      for (const h of ungrouped) hostsEl.append(hostButton(h));
    }
    if (!hosts.length && !showedGroup) {
      const scoped = viewer && viewer.role !== "admin" && !viewer.all_hosts;
      const msg = scoped
        ? "No hosts in your groups."
        : "No hosts yet. Start an agent with the shared key and it will register itself.";
      hostsEl.append(el("p", { class: "px-4 py-6 text-[13px] leading-relaxed text-slate-400" }, [msg]));
    } else if (!visible.length && !showedGroup) {
      hostsEl.append(el("p", { class: "px-4 py-6 text-[13px] leading-relaxed text-slate-400" }, ["No matching hosts."]));
    }
  }

  function matchesHost(h, query) {
    if (!query) return true;
    if (String(h.hostname || "").toLowerCase().includes(query)) return true;
    if (String(h.display_name || "").toLowerCase().includes(query)) return true;
    const g = groups.find((item) => item.id === (h.group_id || ""));
    if (g && g.name.toLowerCase().includes(query)) return true;
    if (!g && "ungrouped".includes(query)) return true;
    return false;
  }

  function groupOpen(key, query) {
    return !!query || !collapsed.has(key);
  }

  function toggleGroup(key) {
    if (hostQEl.value.trim()) return;
    if (collapsed.has(key)) collapsed.delete(key);
    else collapsed.add(key);
    try { localStorage.setItem(collapseKey, JSON.stringify([...collapsed])); } catch (e) { /* keep the choice for this page */ }
    paintHosts();
  }

  function groupLabel(name, count, loose, key, open) {
    return el("button", {
      type: "button",
      class: "host-group flex w-full cursor-pointer items-center justify-between gap-3 px-4 pb-1 pt-5 text-left text-slate-400 transition-colors duration-150 ease-in-out hover:text-slate-200" + (loose ? " is-loose" : "") + (open ? "" : " is-collapsed"),
      "aria-expanded": open ? "true" : "false",
      onclick: () => toggleGroup(key),
    }, [
      el("span", { class: "flex min-w-0 items-center gap-2" }, [
        iconChevron(),
        el("span", { class: "label truncate text-[11px] font-medium uppercase tracking-[0.14em]" }, [name]),
      ]),
      el("span", { class: "count font-mono text-[11px] tabular-nums text-slate-500" }, [String(count)]),
    ]);
  }

  function hostButton(h) {
    const dotClass = h.status !== "enabled" ? "mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-status-down" : (h.online ? "mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-status-on" : "mt-1.5 h-1.5 w-1.5 shrink-0 rounded-full bg-status-off");
    const rowClass = h.id === hostId
      ? "flex w-full cursor-pointer items-start gap-3 border-l-2 border-amber-500 bg-amber-500/[0.08] px-4 py-2.5 text-left transition-colors duration-150 ease-in-out"
      : "flex w-full cursor-pointer items-start gap-3 border-l-2 border-transparent px-4 py-2.5 text-left transition-colors duration-150 ease-in-out hover:bg-white/[0.03]";
    return el("button", {
      type: "button",
      class: rowClass,
      title: hostDetail(h),
      onclick: () => selectHost(h.id),
    }, [
      el("span", { class: dotClass, "aria-hidden": "true" }),
      el("span", { class: "min-w-0 flex-1" }, [
        el("span", { class: "block truncate text-[13px] font-medium leading-5 text-slate-200" }, [hostLabel(h)]),
        el("span", { class: "mt-0.5 block font-mono text-[11px] leading-4 text-slate-400" }, [hostDetail(h)]),
      ]),
    ]);
  }

  function selectHost(id) {
    if (id === hostId) return;
    hostId = id;
    containerId = "";
    paintHosts();
    refreshContainers();
    reload();
  }

  function paintChips() {
    chipsEl.replaceChildren();
    if (!hostId) return;
    const isAdmin = !!(viewer && viewer.role === "admin");
    const activeHost = hosts.find((h) => h.id === hostId);
    const hostOnline = !!(activeHost && activeHost.online);
    chipsEl.append(chip(null, "All containers", containerId === "", false, false));
    for (const c of containers) {
      const live = hostOnline && c.state === "running";
      const stateLabel = c.state === "running" ? "offline" : c.state;
      const label = c.name + (live ? "" : " · " + stateLabel);
      chipsEl.append(chip(c, label, c.id === containerId, !live, isAdmin && !live));
    }
  }

  function chip(c, label, selected, exited, deletable) {
    const id = c ? c.id : "";
    const tone = selected
      ? "relative z-10 border-amber-500 bg-amber-500 text-[#1c1408]"
      : "border-ink-edge bg-ink-field text-slate-300 hover:border-slate-500 hover:text-slate-100";
    const select = () => {
      if (containerId === id) return;
      containerId = id;
      paintChips();
      reload();
    };
    if (!deletable) {
      return el("button", {
        type: "button",
        class: "inline-flex h-8 max-w-[16rem] shrink-0 cursor-pointer items-center truncate border px-3 text-[12px] font-medium transition-colors duration-150 ease-in-out " + tone + (exited ? " opacity-50" : ""),
        title: label,
        onclick: select,
      }, [label]);
    }
    const delCls = selected
      ? "inline-flex w-7 shrink-0 cursor-pointer items-center justify-center border-l border-[#1c1408]/25 bg-transparent text-[#1c1408] transition-colors duration-150 ease-in-out hover:bg-[#1c1408]/15"
      : "inline-flex w-7 shrink-0 cursor-pointer items-center justify-center border-l border-ink-edge bg-transparent text-slate-400 transition-colors duration-150 ease-in-out hover:bg-status-down/15 hover:text-clay";
    return el("span", {
      class: "inline-flex h-8 shrink-0 items-stretch border transition-colors duration-150 ease-in-out " + tone + (selected ? "" : " opacity-60 hover:opacity-100 focus-within:opacity-100"),
    }, [
      el("button", {
        type: "button",
        class: "max-w-[14rem] cursor-pointer truncate bg-transparent px-3 text-[12px] font-medium text-inherit",
        title: label,
        onclick: select,
      }, [label]),
      el("button", {
        type: "button",
        class: delCls,
        title: "Delete " + c.name + " and its stored logs",
        "aria-label": "Delete container " + c.name,
        onclick: () => deleteContainer(c),
      }, [iconClose()]),
    ]);
  }

  async function deleteContainer(c) {
    if (!confirm("Delete container " + c.name + " and its stored logs?")) return;
    const myHost = hostId;
    try {
      await api("/api/v1/admin/hosts/" + encodeURIComponent(myHost) + "/containers/" + encodeURIComponent(c.id), { method: "DELETE" });
      if (myHost === hostId) {
        containers = containers.filter((item) => item.id !== c.id);
        if (containerId === c.id) containerId = "";
        paintChips();
        reload();
      }
      refreshHosts();
      toast("Container deleted");
    } catch (err) {
      toast(err.message);
      if (myHost === hostId) refreshContainers();
    }
  }

  async function reload() {
    const my = ++gen;
    ready = false;
    unread = 0;
    if (!hostId) {
      lines = [];
      paintLines();
      ready = true;
      return;
    }
    // Leave the rows in place until this response arrives. Clearing first
    // blanks the pane on every keystroke.
    try {
      const data = await api(logURL());
      if (my !== gen) return;
      lines = data.lines || [];
      paintLines({ stick: follow && nearBottom() && !selectingLog() });
      ready = true;
    } catch (e) {
      if (my === gen && e.message !== "unauthorized") toast(e.message);
      if (my === gen) ready = true;
    }
  }

  async function pollLogs() {
    if (polling || !follow || !hostId || !ready) return;
    polling = true;
    const my = gen;
    const after = lines.length ? lines[lines.length - 1].id : 0;
    try {
      const data = await api(logURL({ after_id: String(after) }));
      if (my !== gen || !data) return;
      if (!data.lines || !data.lines.length) return;
      // A selection inside the pane keeps its place while new lines arrive.
      const stick = nearBottom() && !selectingLog();
      for (const ln of data.lines) {
        lines.push(ln);
        logEl.append(lineNode(ln, q));
      }
      if (!stick) unread += data.lines.length;
      if (lines.length > 3000) {
        lines = lines.slice(lines.length - 3000);
        paintLines({ stick });
      } else {
        const label = lineLabel(lines.length);
        if (metaEl.textContent !== label) metaEl.textContent = label;
        paintEmpty();
        if (stick) pinBottom();
        syncJumpButton();
      }
    } catch (e) {
      if (e.message !== "unauthorized") toast(e.message);
    } finally {
      polling = false;
    }
  }

  async function loadOlder() {
    if (!hostId || !lines.length) { toast("No older lines"); return; }
    const my = gen;
    const prevHeight = view().scrollHeight;
    try {
      const data = await api(logURL({ before_id: String(lines[0].id) }));
      if (my !== gen) return;
      if (!data.lines || !data.lines.length) { toast("No older lines"); return; }
      lines = data.lines.concat(lines);
      paintLines({ anchorHeight: prevHeight });
    } catch (e) {
      if (e.message !== "unauthorized") toast(e.message);
    }
  }

  function view() {
    return scroller || logEl;
  }

  function pinBottom() {
    const node = view();
    const top = Math.max(0, node.scrollHeight - node.clientHeight);
    if (node.scrollTop !== top) node.scrollTop = top;
  }

  function syncJumpButton() {
    if (!jumpLatestBtn) return;
    const node = view();
    const scrollable = node.scrollHeight - node.clientHeight > 64;
    const show = lines.length > 0 && scrollable && !nearBottom();
    jumpLatestBtn.hidden = !show;
    if (show && jumpLatestLabel) {
      jumpLatestLabel.textContent = unread > 0 ? unread + " new" : "Jump to latest";
    }
  }

  function sameLineIds(want) {
    if (logEl.childElementCount !== want.length) return false;
    let i = 0;
    for (const node of logEl.children) {
      if (node.getAttribute("data-id") !== want[i]) return false;
      i++;
    }
    return true;
  }

  // Patch the pane by line id. Rows that are still on screen stay the same
  // nodes, so a filter refresh does not redraw them.
  function paintLines(opts) {
    opts = opts || {};
    const want = new Array(lines.length);
    const wantSet = new Set();
    for (let i = 0; i < lines.length; i++) {
      const id = String(lines[i].id);
      want[i] = id;
      wantSet.add(id);
    }
    const qChanged = q !== paintedQ;
    const changed = qChanged || !sameLineIds(want);
    const node = view();
    const manageScroll = changed && !!(opts.stick || opts.anchorHeight != null);
    if (manageScroll) node.style.overflowAnchor = "none";
    if (changed) {
      if (qChanged) {
        logEl.replaceChildren();
        for (let i = 0; i < lines.length; i++) logEl.append(lineNode(lines[i], q));
        paintedQ = q;
      } else {
        const stale = [];
        for (const node of logEl.children) {
          if (!wantSet.has(node.getAttribute("data-id"))) stale.push(node);
        }
        for (const node of stale) node.remove();
        let cursor = logEl.firstElementChild;
        for (let i = 0; i < want.length; i++) {
          const id = want[i];
          if (cursor && cursor.getAttribute("data-id") === id) {
            cursor = cursor.nextElementSibling;
            continue;
          }
          let found = null;
          for (let n = cursor; n; n = n.nextElementSibling) {
            if (n.getAttribute("data-id") === id) { found = n; break; }
          }
          logEl.insertBefore(found || lineNode(lines[i], q), cursor);
        }
        while (cursor) {
          const next = cursor.nextElementSibling;
          cursor.remove();
          cursor = next;
        }
      }
    }
    const label = hostId ? (lines.length ? lineLabel(lines.length) : "No lines") : "Select a host";
    if (metaEl.textContent !== label) metaEl.textContent = label;
    paintEmpty();
    if (opts.stick) {
      pinBottom();
      unread = 0;
    } else if (opts.anchorHeight != null) {
      const top = node.scrollHeight - opts.anchorHeight;
      if (node.scrollTop !== top) node.scrollTop = top;
    }
    if (manageScroll) node.style.overflowAnchor = selectingLog() ? "none" : "";
    syncJumpButton();
  }

  function hasActiveFilters() {
    return !!(containerId || q || stream || range);
  }

  function paintEmpty() {
    if (!emptyEl) return;
    const show = lines.length === 0;
    emptyEl.hidden = !show;
    if (!show || !emptyTitle || !emptyHint) return;
    if (hostId) {
      emptyTitle.textContent = "No lines in this view";
      emptyHint.textContent = "The container, stream, and time filters all apply. Older loads the previous page.";
      if (resetFiltersBtn) resetFiltersBtn.hidden = !hasActiveFilters();
    } else {
      emptyTitle.textContent = "Select a host";
      emptyHint.textContent = "Hosts appear after an agent registers with the shared key.";
      if (resetFiltersBtn) resetFiltersBtn.hidden = true;
    }
  }

  function selectingLog() {
    const sel = window.getSelection();
    if (!sel || sel.isCollapsed || !sel.rangeCount) return false;
    const node = sel.anchorNode;
    return !!(node && logEl.contains(node));
  }

  function sinceBound() {
    const now = Date.now();
    if (range === "15m") return new Date(now - 15 * 60 * 1000).toISOString();
    if (range === "1h") return new Date(now - 60 * 60 * 1000).toISOString();
    if (range === "from" && sinceLocal) {
      const start = new Date(sinceLocal);
      if (!Number.isNaN(start.getTime())) return start.toISOString();
    }
    return "";
  }

  function logURL(extra) {
    const p = new URLSearchParams();
    p.set("host_id", hostId);
    if (containerId) p.set("container_id", containerId);
    if (q) p.set("q", q);
    if (stream) p.set("stream", stream);
    const since = sinceBound();
    if (since) p.set("since", since);
    p.set("limit", "300");
    if (extra) for (const [k, v] of Object.entries(extra)) p.set(k, v);
    return "/api/v1/logs?" + p.toString();
  }

}

function hostLabel(h) {
  const name = String(h.display_name || "").trim();
  return name || h.hostname;
}

function hostDetail(h) {
  const status = h.status !== "enabled" ? "disabled" : (h.online ? "online" : "offline");
  const ctrs = h.container_count + (h.container_count === 1 ? " container" : " containers");
  const parts = [status, ago(h.last_seen_at), ctrs];
  if (String(h.display_name || "").trim()) parts.push(h.hostname);
  if (h.dropped) parts.push("dropped " + h.dropped);
  return parts.join(" · ");
}

const btnBase = "inline-flex h-8 shrink-0 cursor-pointer items-center rounded-md px-3 text-[12px] font-medium transition-colors duration-150 ease-in-out disabled:cursor-default disabled:opacity-40";
const btnPrimary = btnBase + " border border-amber-500 bg-amber-500 text-[#1c1408] hover:bg-amber-400";
const btnGhost = btnBase + " border border-ink-edge bg-ink-field text-slate-300 hover:border-slate-500 hover:text-slate-100";
const btnDanger = btnBase + " border border-status-down bg-transparent text-clay hover:bg-status-down/10";
const btnQuiet = btnBase + " border border-amber-500/50 bg-transparent text-amber-300 hover:bg-amber-500/10";
const btnOk = btnBase + " border border-status-on/60 bg-transparent text-status-on hover:bg-status-on/10";
const fieldBox = "h-8 rounded-md border border-ink-edge bg-ink-field px-2.5 text-[13px] text-slate-200 transition-colors duration-150 ease-in-out placeholder:text-slate-500 hover:border-slate-500 focus:border-amber-500/70 focus:outline-none";
const selectCls = "ui-select h-8 cursor-pointer rounded-md border border-ink-edge bg-ink-field pl-2.5 pr-8 text-[13px] text-slate-200 transition-colors duration-150 ease-in-out hover:border-slate-500 focus:border-amber-500/70 focus:outline-none";
const emptyRow = "px-1 py-6 text-[13px] text-slate-400";

function statusPill(dotClass, textClass, label) {
  return el("span", { class: "inline-flex items-center gap-1.5 font-mono text-[10px] font-medium uppercase tracking-[0.14em] " + textClass }, [
    el("span", { class: "h-1.5 w-1.5 shrink-0 rounded-full " + dotClass, "aria-hidden": "true" }),
    label,
  ]);
}

function statusBadge(h) {
  if (h.status !== "enabled") return statusPill("bg-status-down", "text-clay", "disabled");
  if (h.online) return statusPill("bg-status-on", "text-status-on", "online");
  return statusPill("bg-status-off", "text-slate-500", "offline");
}

function highlightNodes(text, query) {
  const str = String(text || "");
  if (!query) return [str];
  const lower = str.toLowerCase();
  const needle = query.toLowerCase();
  const out = [];
  let pos = 0;
  let hits = 0;
  while (pos < str.length && hits < 32) {
    const idx = lower.indexOf(needle, pos);
    if (idx === -1) break;
    if (idx > pos) out.push(str.slice(pos, idx));
    out.push(el("mark", { class: "log-hit" }, [str.slice(idx, idx + needle.length)]));
    pos = idx + needle.length;
    hits++;
  }
  if (pos < str.length) out.push(str.slice(pos));
  return out;
}

function copyLogLine(ln) {
  const text = (ln.timestamp || "") + " " + (ln.container_name || ln.container_id) + " " + ln.stream + " " + (ln.message || "");
  if (navigator.clipboard && navigator.clipboard.writeText) {
    navigator.clipboard.writeText(text).then(() => toast("Line copied")).catch(() => toast("Could not copy"));
    return;
  }
  toast("Clipboard unavailable");
}

function lineNode(ln, query) {
  const stderr = ln.stream === "stderr";
  const copyBtn = el("button", {
    type: "button",
    class: "copy-btn inline-flex h-6 w-6 cursor-pointer items-center justify-center rounded border border-ink-edge bg-ink-chrome text-slate-400 hover:border-slate-500 hover:text-slate-100",
    title: "Copy line",
    "aria-label": "Copy log line",
    onclick: () => copyLogLine(ln),
  }, [iconCopy()]);
  return el("div", {
    class: "log-line log-cols font-mono text-[12px] transition-colors duration-150 ease-in-out hover:bg-white/[0.03]" + (stderr ? " stderr" : ""),
    "data-id": String(ln.id),
  }, [
    el("span", { class: "ts tabular-nums text-slate-400", title: fullTime(ln.timestamp) }, [formatTime(ln.timestamp)]),
    el("span", { class: "name text-sand", title: ln.container_name || ln.container_id }, [ln.container_name || ln.container_id]),
    el("span", { class: "stream text-[11px] " + (stderr ? "text-clay" : "text-slate-400") }, [ln.stream]),
    el("span", { class: "msg " + (stderr ? "text-clay" : "text-slate-200") }, highlightNodes(ln.message, query)),
    copyBtn,
  ]);
}

function fullTime(iso) {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return String(iso);
  return d.toLocaleString() + " (" + iso + ")";
}

function lineLabel(n) {
  return n === 1 ? "1 line" : n + " lines";
}

function nearBottom() {
  const node = document.getElementById("log-scroll") || document.getElementById("log");
  return node.scrollHeight - node.scrollTop - node.clientHeight < 48;
}

function formatTime(iso) {
  if (!iso) return "never";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return String(iso);
  const p = (n, w = 2) => String(n).padStart(w, "0");
  let out = p(d.getHours()) + ":" + p(d.getMinutes()) + ":" + p(d.getSeconds()) + "." + p(d.getMilliseconds(), 3);
  if (d.toDateString() !== new Date().toDateString()) {
    out = p(d.getMonth() + 1) + "-" + p(d.getDate()) + " " + out;
  }
  return out;
}

function ago(iso) {
  if (!iso) return "never";
  const d = new Date(iso);
  const s = Math.max(0, Math.round((Date.now() - d.getTime()) / 1000));
  if (s < 60) return s + "s ago";
  if (s < 3600) return Math.round(s / 60) + "m ago";
  if (s < 86400) return Math.round(s / 3600) + "h ago";
  return Math.round(s / 86400) + "d ago";
}

function initAdmin() {
  const hostList = document.getElementById("admin-hosts");
  const groupList = document.getElementById("admin-groups");
  const hostQuery = document.getElementById("host-admin-q");
  const generalForm = document.getElementById("save-general");
  const registrationForm = document.getElementById("save-registration");
  const directoryForm = document.getElementById("save-directory");
  const sections = ["hosts", "general", "registration", "psk", "groups", "accounts", "directory"];
  let hosts = [];
  let groups = [];
  let savedSettings = null;
  let generalDirty = false;
  let registrationDirty = false;
  let directoryDirty = false;
  const expandedHosts = new Set();
  const hostContainers = new Map();

  for (const id of sections) {
    document.getElementById("nav-" + id).addEventListener("click", () => {
      if (location.hash === "#" + id) showSection(id);
      else location.hash = id;
    });
  }
  window.addEventListener("hashchange", () => showSection(sectionFromHash()));
  showSection(sectionFromHash());

  document.getElementById("create-group").addEventListener("submit", async (e) => {
    e.preventDefault();
    const input = document.getElementById("group-name");
    try {
      await api("/api/v1/admin/groups", { method: "POST", body: JSON.stringify({ name: input.value.trim() }) });
      input.value = "";
      await loadFleet(true);
    } catch (err) {
      toast(err.message);
    }
  });
  hostQuery.addEventListener("input", () => paintHosts());
  generalForm.addEventListener("input", () => { generalDirty = true; });
  generalForm.addEventListener("change", () => { generalDirty = true; });
  generalForm.addEventListener("submit", async (e) => {
    e.preventDefault();
    try {
      const data = await api("/api/v1/admin/settings", {
        method: "PATCH",
        body: JSON.stringify({
          brand_name: document.getElementById("brand-name").value.trim(),
          brand_tagline: document.getElementById("brand-tagline").value.trim(),
          retention: document.getElementById("retention").value,
          session: document.getElementById("session-length").value,
          offline: document.getElementById("offline-after").value,
        }),
      });
      generalDirty = false;
      fillSettings(data);
      applyBrand();
      toast("Settings saved");
    } catch (err) {
      toast(err.message);
    }
  });
  registrationForm.addEventListener("input", () => { registrationDirty = true; });
  registrationForm.addEventListener("change", () => { registrationDirty = true; });
  registrationForm.addEventListener("submit", async (e) => {
    e.preventDefault();
    try {
      const data = await api("/api/v1/admin/settings", {
        method: "PATCH",
        body: JSON.stringify({
          auto_register: document.getElementById("auto-register").checked,
          default_group_id: document.getElementById("default-group").value,
        }),
      });
      registrationDirty = false;
      fillSettings(data);
      toast("Settings saved");
    } catch (err) {
      toast(err.message);
    }
  });
  directoryForm.addEventListener("input", () => { directoryDirty = true; hideDirectoryTest(); });
  directoryForm.addEventListener("change", () => { directoryDirty = true; hideDirectoryTest(); });
  directoryForm.addEventListener("submit", async (e) => {
    e.preventDefault();
    try {
      const data = await api("/api/v1/admin/settings", { method: "PATCH", body: JSON.stringify(directoryBody()) });
      directoryDirty = false;
      fillSettings(data);
      paintDirectory(data);
      toast("Settings saved");
    } catch (err) {
      toast(err.message);
    }
  });
  document.getElementById("test-directory").addEventListener("click", async () => {
    const button = document.getElementById("test-directory");
    const result = document.getElementById("directory-test-result");
    button.disabled = true;
    const label = button.textContent;
    button.textContent = "Testing...";
    result.hidden = true;
    result.textContent = "";
    try {
      await api("/api/v1/admin/directory/test", { method: "POST", body: JSON.stringify(directoryBody()) });
      result.classList.remove("text-clay");
      result.classList.add("text-status-on");
      result.textContent = "Connected to the directory.";
      result.hidden = false;
    } catch (err) {
      result.classList.remove("text-status-on");
      result.classList.add("text-clay");
      result.textContent = err.message;
      result.hidden = false;
    } finally {
      button.disabled = false;
      button.textContent = label;
    }
  });
  document.getElementById("generate-psk").addEventListener("click", generatePSK);
  document.getElementById("replace-psk").addEventListener("submit", replacePSK);
  loadFleet(true);
  loadSettings();
  setInterval(() => { if (!document.hidden) loadFleet(false); }, 5000);
  setInterval(() => { if (!document.hidden) loadSettings(); }, 5000);

  function directoryBody() {
    const body = {
      ldap_enabled: document.getElementById("ldap-enabled").checked,
      ldap_url: document.getElementById("ldap-url").value.trim(),
      ldap_starttls: document.getElementById("ldap-starttls").checked,
      ldap_bind_dn: document.getElementById("ldap-bind-dn").value.trim(),
      ldap_base_dn: document.getElementById("ldap-base-dn").value.trim(),
      ldap_user_filter: document.getElementById("ldap-user-filter").value.trim(),
      ldap_username_attr: document.getElementById("ldap-username-attr").value.trim(),
      ldap_admin_group: document.getElementById("ldap-admin-group").value.trim(),
    };
    const secret = document.getElementById("ldap-bind-password").value;
    if (secret) body.ldap_bind_password = secret;
    if (document.getElementById("ldap-clear-bind").checked) body.ldap_clear_bind_password = true;
    return body;
  }

  function hideDirectoryTest() {
    const result = document.getElementById("directory-test-result");
    result.hidden = true;
    result.textContent = "";
  }

  function sectionFromHash() {
    const name = location.hash.replace(/^#/, "");
    if (name === "settings" || name === "general") return "general";
    if (sections.includes(name)) return name;
    return "hosts";
  }

  function showSection(name) {
    for (const id of sections) {
      document.getElementById("panel-" + id).hidden = id !== name;
      const btn = document.getElementById("nav-" + id);
      btn.classList.toggle("is-active", id === name);
      if (id === name) btn.setAttribute("aria-current", "page");
      else btn.removeAttribute("aria-current");
    }
    const main = document.querySelector(".admin-main");
    if (main) main.scrollTop = 0;
  }

  async function loadFleet(force) {
    try {
      const data = await api("/api/v1/hosts");
      hosts = data.hosts || [];
      groups = (data.groups || []).slice().sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: "base" }) || a.id.localeCompare(b.id));
      const hostIds = new Set(hosts.map((h) => h.id));
      for (const id of [...expandedHosts]) {
        if (!hostIds.has(id)) {
          expandedHosts.delete(id);
          hostContainers.delete(id);
        }
      }
      if (expandedHosts.size) {
        await Promise.all([...expandedHosts].map((id) => fetchHostContainers(id)));
      }
      if (force || !hostList.contains(document.activeElement)) paintHosts();
      if (force || !groupList.contains(document.activeElement)) paintGroups();
      const groupSelect = document.getElementById("default-group");
      if (savedSettings && !registrationDirty && document.activeElement !== groupSelect) {
        paintDefaultGroup(savedSettings.default_group_id || "");
      }
    } catch (err) {
      if (err.message !== "unauthorized") toast(err.message);
    }
  }

  async function fetchHostContainers(id) {
    try {
      const data = await api("/api/v1/containers?host_id=" + encodeURIComponent(id));
      hostContainers.set(id, data.containers || []);
    } catch (err) {
      if (err.message !== "unauthorized") toast(err.message);
    }
  }

  async function toggleHostContainers(id) {
    if (expandedHosts.has(id)) {
      expandedHosts.delete(id);
      paintHosts();
      return;
    }
    expandedHosts.add(id);
    paintHosts();
    await fetchHostContainers(id);
    if (expandedHosts.has(id)) paintHosts();
  }

  function matches(h, query) {
    if (!query) return true;
    if (String(h.hostname || "").toLowerCase().includes(query)) return true;
    if (String(h.display_name || "").toLowerCase().includes(query)) return true;
    const g = groups.find((item) => item.id === (h.group_id || ""));
    if (g && g.name.toLowerCase().includes(query)) return true;
    if (!g && "ungrouped".includes(query)) return true;
    return false;
  }

  function paintHosts() {
    const query = hostQuery.value.trim().toLowerCase();
    const visible = hosts.filter((h) => matches(h, query));
    hostList.replaceChildren();
    if (!visible.length) {
      const msg = hosts.length ? "No matching hosts." : "No hosts yet. Start an agent with the shared key and it will register itself.";
      hostList.append(el("div", { class: emptyRow }, [msg]));
      return;
    }
    for (const h of visible) hostList.append(hostRow(h));
  }

  function hostRow(h) {
    const row = el("div", { class: "border-b border-ink-line px-1 py-4" });
    const deleteBtn = el("button", { type: "button", class: btnDanger }, ["Delete"]);
    const statusBtn = h.status === "enabled"
      ? el("button", { type: "button", class: btnQuiet }, ["Disable"])
      : el("button", { type: "button", class: btnOk }, ["Enable"]);
    deleteBtn.addEventListener("click", () => removeHost(h));
    statusBtn.addEventListener("click", () => setStatus(h, h.status === "enabled" ? "disable" : "enable"));
    const sel = el("select", { class: selectCls + " w-full max-w-xs", "aria-label": "Group for " + h.hostname });
    const none = el("option", { value: "" }, ["Ungrouped"]);
    if (!h.group_id) none.selected = true;
    sel.append(none);
    for (const g of groups) {
      const opt = el("option", { value: g.id }, [g.name]);
      if (g.id === h.group_id) opt.selected = true;
      sel.append(opt);
    }
    sel.addEventListener("change", () => assignGroup(h, sel.value));
    const nameInput = el("input", {
      class: fieldBox + " w-64 max-w-full",
      type: "text",
      maxlength: "64",
      autocomplete: "off",
      spellcheck: "false",
      placeholder: "Display name",
      "aria-label": "Display name for " + h.hostname,
    });
    nameInput.value = h.display_name || "";
    const saveName = el("button", { type: "button", class: btnGhost }, ["Save name"]);
    saveName.addEventListener("click", () => saveDisplayName(h, nameInput.value));
    nameInput.addEventListener("keydown", (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        saveDisplayName(h, nameInput.value);
      }
    });
    const ctrCount = h.container_count + (h.container_count === 1 ? " container" : " containers") + (h.exited_count ? " (" + h.exited_count + " exited)" : "");
    const meta = h.hostname + " · seen " + ago(h.last_seen_at) + (h.dropped ? " · dropped " + h.dropped : "") + " · " + ctrCount;
    row.append(
      el("div", { class: "flex flex-wrap items-center gap-2" }, [
        el("span", { class: "min-w-0 flex-1 truncate text-[14px] font-medium text-slate-100" }, [hostLabel(h)]),
        statusBadge(h),
        el("div", { class: "flex shrink-0 gap-2" }, [statusBtn, deleteBtn]),
      ]),
      el("div", { class: "mt-1 font-mono text-[12px] text-slate-500" }, [meta]),
      el("div", { class: "mt-3 flex flex-wrap items-center gap-2" }, [nameInput, saveName]),
      el("div", { class: "mt-2" }, [sel]),
    );
    if (h.container_count > 0 || expandedHosts.has(h.id)) {
      const open = expandedHosts.has(h.id);
      const toggleLabel = h.exited_count
        ? "Containers (" + h.container_count + " · " + h.exited_count + " exited)"
        : "Containers (" + h.container_count + ")";
      const toggleBtn = el("button", {
        type: "button",
        class: "host-containers-toggle mt-3 inline-flex cursor-pointer items-center gap-1.5 text-[12px] font-medium text-slate-400 transition-colors duration-150 ease-in-out hover:text-slate-200" + (open ? " is-open" : ""),
        "aria-expanded": open ? "true" : "false",
        onclick: () => toggleHostContainers(h.id),
      }, [iconChevron(), el("span", {}, [toggleLabel])]);
      row.append(toggleBtn);
      if (open) row.append(hostContainersBox(h));
    }
    return row;
  }

  function hostContainersBox(h) {
    const box = el("div", { class: "mt-2 rounded-md border border-ink-line bg-ink-chrome px-3 py-2" });
    if (!hostContainers.has(h.id)) {
      box.append(el("div", { class: "py-2 text-[12px] text-slate-400" }, ["Loading containers..."]));
      return box;
    }
    const list = (hostContainers.get(h.id) || []).slice().sort((a, b) => {
      const aLive = h.online && a.state === "running" ? 1 : 0;
      const bLive = h.online && b.state === "running" ? 1 : 0;
      return aLive - bLive || a.name.localeCompare(b.name, undefined, { sensitivity: "base" });
    });
    if (!list.length) {
      box.append(el("div", { class: "py-2 text-[12px] text-slate-400" }, ["No containers."]));
      return box;
    }
    const removable = list.filter((c) => !(h.online && c.state === "running"));
    if (removable.length > 1) {
      const bulkLabel = h.online ? "Delete all exited (" + removable.length + ")" : "Delete all (" + removable.length + ")";
      const summaryLabel = h.online ? removable.length + " exited containers" : removable.length + " inactive containers";
      const pruneBtn = el("button", { type: "button", class: btnDanger }, [bulkLabel]);
      pruneBtn.addEventListener("click", () => removeExitedContainers(h, removable.length));
      box.append(el("div", { class: "flex items-center justify-between gap-2 border-b border-ink-line pb-2 mb-1" }, [
        el("span", { class: "text-[12px] text-slate-400" }, [summaryLabel]),
        pruneBtn,
      ]));
    }
    for (let i = 0; i < list.length; i++) {
      const c = list[i];
      const isLive = h.online && c.state === "running";
      const stateLabel = isLive ? "running" : (c.state === "running" ? "offline" : (c.state || "exited"));
      const badge = isLive
        ? statusPill("bg-status-on", "text-status-on", "running")
        : statusPill("bg-status-off", "text-slate-500", stateLabel);
      const infoParts = [];
      if (c.image) infoParts.push(c.image);
      if (c.last_seen_at) infoParts.push("seen " + ago(c.last_seen_at));
      const right = [badge];
      if (!isLive) {
        const del = el("button", { type: "button", class: btnDanger }, ["Delete"]);
        del.addEventListener("click", () => removeContainer(h, c));
        right.push(del);
      }
      box.append(el("div", {
        class: "flex flex-wrap items-center gap-3 py-2" + (i < list.length - 1 ? " border-b border-ink-line" : ""),
      }, [
        el("div", { class: "min-w-0 flex-1" }, [
          el("div", { class: "truncate font-mono text-[12px] text-slate-200" }, [c.name]),
          el("div", { class: "mt-0.5 truncate font-mono text-[11px] text-slate-500" }, [infoParts.join(" · ")]),
        ]),
        el("div", { class: "flex shrink-0 items-center gap-2" }, right),
      ]));
    }
    return box;
  }

  function paintGroups() {
    groupList.replaceChildren();
    if (!groups.length) {
      groupList.append(el("div", { class: emptyRow }, ["No groups yet."]));
      return;
    }
    for (const g of groups) groupList.append(groupRow(g));
  }

  function groupRow(g) {
    const row = el("div", { class: "flex items-center gap-3 border-b border-ink-line px-1 py-4" });
    const renameBtn = el("button", { type: "button", class: btnGhost }, ["Rename"]);
    const deleteBtn = el("button", { type: "button", class: btnDanger }, ["Delete"]);
    renameBtn.addEventListener("click", () => beginRenameGroup(g, row));
    deleteBtn.addEventListener("click", () => deleteGroup(g));
    row.append(
      el("div", { class: "min-w-0 flex-1" }, [
        el("div", { class: "truncate text-[14px] font-medium text-slate-100" }, [g.name]),
        el("div", { class: "mt-0.5 text-[12px] text-slate-500" }, [g.host_count === 1 ? "1 host" : g.host_count + " hosts"]),
      ]),
      el("div", { class: "flex shrink-0 gap-2" }, [renameBtn, deleteBtn]),
    );
    return row;
  }

  function beginRenameGroup(g, row) {
    const input = el("input", {
      class: fieldBox + " min-w-0 flex-1",
      type: "text",
      maxlength: "64",
      autocomplete: "off",
      spellcheck: "false",
      "aria-label": "Group name",
    });
    input.value = g.name;
    const save = el("button", { type: "button", class: btnPrimary }, ["Save"]);
    const cancel = el("button", { type: "button", class: btnGhost }, ["Cancel"]);
    save.addEventListener("click", () => renameGroup(g, input.value));
    cancel.addEventListener("click", () => paintGroups());
    input.addEventListener("keydown", (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        renameGroup(g, input.value);
      } else if (e.key === "Escape") {
        e.preventDefault();
        paintGroups();
      }
    });
    row.replaceChildren(el("div", { class: "flex w-full items-center gap-2" }, [input, save, cancel]));
    input.focus();
    input.select();
  }

  async function saveChange(path, body) {
    await api(path, body);
    await loadFleet(true);
  }

  async function saveDisplayName(h, name) {
    try {
      await saveChange("/api/v1/admin/hosts/" + encodeURIComponent(h.id), { method: "PATCH", body: JSON.stringify({ display_name: name.trim() }) });
    } catch (err) {
      toast(err.message);
      paintHosts();
    }
  }

  async function assignGroup(h, groupId) {
    try {
      await saveChange("/api/v1/admin/hosts/" + encodeURIComponent(h.id) + "/group", { method: "PATCH", body: JSON.stringify({ group_id: groupId }) });
    } catch (err) {
      toast(err.message);
      paintHosts();
    }
  }

  async function setStatus(h, action) {
    try {
      await saveChange("/api/v1/admin/hosts/" + encodeURIComponent(h.id) + "/" + action, { method: "POST", body: "{}" });
    } catch (err) {
      toast(err.message);
    }
  }

  async function removeHost(h) {
    if (!confirm("Delete " + hostLabel(h) + " and its stored logs?")) return;
    try {
      await saveChange("/api/v1/admin/hosts/" + encodeURIComponent(h.id), { method: "DELETE" });
    } catch (err) {
      toast(err.message);
    }
  }

  async function removeContainer(h, c) {
    if (!confirm("Delete container " + c.name + " and its stored logs?")) return;
    try {
      await api("/api/v1/admin/hosts/" + encodeURIComponent(h.id) + "/containers/" + encodeURIComponent(c.id), { method: "DELETE" });
      await loadFleet(true);
      toast("Container deleted");
    } catch (err) {
      toast(err.message);
      await loadFleet(true);
    }
  }

  async function removeExitedContainers(h, count) {
    if (!confirm("Delete " + count + " exited containers on " + hostLabel(h) + " and their stored logs?")) return;
    try {
      await api("/api/v1/admin/hosts/" + encodeURIComponent(h.id) + "/containers?state=exited", { method: "DELETE" });
      await loadFleet(true);
      toast("Exited containers deleted");
    } catch (err) {
      toast(err.message);
      await loadFleet(true);
    }
  }

  async function renameGroup(g, name) {
    try {
      await saveChange("/api/v1/admin/groups/" + encodeURIComponent(g.id), { method: "PATCH", body: JSON.stringify({ name: name.trim() }) });
    } catch (err) {
      toast(err.message);
    }
  }

  async function deleteGroup(g) {
    if (!confirm("Delete group " + g.name + "? Hosts in it become ungrouped.")) return;
    try {
      await saveChange("/api/v1/admin/groups/" + encodeURIComponent(g.id), { method: "DELETE" });
      await loadSettings();
    } catch (err) {
      toast(err.message);
    }
  }

  async function loadSettings() {
    try {
      const [data, psk] = await Promise.all([
        api("/api/v1/admin/settings"),
        api("/api/v1/admin/psk"),
      ]);
      fillSettings(data);
      paintPSK(psk);
    } catch (err) {
      if (err.message !== "unauthorized") toast(err.message);
    }
  }

  function fillSettings(data) {
    savedSettings = data;
    paintPending(data.pending || []);
    if (!generalDirty && !generalForm.contains(document.activeElement)) {
      document.getElementById("brand-name").value = data.brand_name || "";
      document.getElementById("brand-tagline").value = data.brand_tagline || "";
      chooseDuration(document.getElementById("retention"), data.retention_ms);
      chooseDuration(document.getElementById("session-length"), data.session_ms);
      chooseDuration(document.getElementById("offline-after"), data.offline_ms);
    }
    if (!registrationDirty && !registrationForm.contains(document.activeElement)) {
      document.getElementById("auto-register").checked = !!data.auto_register;
      paintDefaultGroup(data.default_group_id || "");
    }
    if (!directoryDirty && !directoryForm.contains(document.activeElement)) paintDirectory(data);
  }

  function paintDirectory(data) {
    document.getElementById("ldap-enabled").checked = !!data.ldap_enabled;
    document.getElementById("ldap-url").value = data.ldap_url || "";
    document.getElementById("ldap-starttls").checked = !!data.ldap_starttls;
    document.getElementById("ldap-bind-dn").value = data.ldap_bind_dn || "";
    document.getElementById("ldap-base-dn").value = data.ldap_base_dn || "";
    document.getElementById("ldap-user-filter").value = data.ldap_user_filter || "(uid=%s)";
    document.getElementById("ldap-username-attr").value = data.ldap_username_attr || "uid";
    document.getElementById("ldap-admin-group").value = data.ldap_admin_group || "";
    document.getElementById("ldap-bind-password").value = "";
    document.getElementById("ldap-clear-bind").checked = false;
    document.getElementById("ldap-bind-set").hidden = !data.ldap_bind_password_set;
  }

  function paintDefaultGroup(selected) {
    const sel = document.getElementById("default-group");
    if (!sel || document.activeElement === sel) return;
    sel.replaceChildren(el("option", { value: "" }, ["Ungrouped"]));
    for (const g of groups) sel.append(el("option", { value: g.id }, [g.name]));
    const has = [...sel.options].some((opt) => opt.value === selected);
    sel.value = has ? selected : "";
  }

  function paintPending(items) {
    const badge = document.getElementById("pending-badge");
    if (badge) {
      badge.textContent = items.length ? String(items.length) : "";
      badge.hidden = !items.length;
    }
    const list = document.getElementById("pending-hosts");
    if (!list) return;
    list.replaceChildren();
    if (!items.length) {
      list.append(el("div", { class: emptyRow }, ["No hosts are waiting."]));
      return;
    }
    for (const item of items) {
      const approve = el("button", { type: "button", class: btnPrimary }, ["Approve"]);
      const dismiss = el("button", { type: "button", class: btnGhost }, ["Dismiss"]);
      approve.addEventListener("click", () => decidePending(item.hostname, "approve"));
      dismiss.addEventListener("click", () => decidePending(item.hostname, "dismiss"));
      const row = el("div", { class: "flex flex-wrap items-center gap-3 border-b border-ink-line px-1 py-4" }, [
        el("div", { class: "min-w-0 flex-1" }, [
          el("div", { class: "truncate font-mono text-[13px] text-slate-100" }, [item.hostname]),
          el("div", { class: "mt-0.5 text-[12px] text-slate-500" }, [item.agent_version ? "agent " + item.agent_version : "agent"]),
        ]),
        el("div", { class: "flex shrink-0 gap-2" }, [approve, dismiss]),
      ]);
      list.append(row);
    }
  }

  async function decidePending(hostname, action) {
    try {
      if (action === "approve") {
        await api("/api/v1/admin/pending/" + encodeURIComponent(hostname) + "/approve", { method: "POST", body: "{}" });
      } else {
        await api("/api/v1/admin/pending/" + encodeURIComponent(hostname), { method: "DELETE" });
      }
      await loadSettings();
      await loadFleet(true);
    } catch (err) {
      toast(err.message);
    }
  }

  function paintPSK(info) {
    document.getElementById("psk-id").textContent = info && info.id ? info.id : "";
    document.getElementById("psk-fingerprint").textContent = info && info.fingerprint ? info.fingerprint : "";
    const paths = document.getElementById("psk-paths");
    paths.textContent = info && info.id_path ? info.id_path + "  ·  " + info.key_path : "";
  }

  async function generatePSK() {
    if (!confirm("Generate a new shared key? Agents cannot register until they have the new id and key. Hosts that already have a token keep sending logs.")) return;
    try {
      const info = await api("/api/v1/admin/psk", { method: "POST", body: JSON.stringify({ action: "generate" }) });
      paintPSK(info);
      showIssuedPSK(info);
      toast("New key is ready. Download it before you leave this page.");
    } catch (err) {
      toast(err.message);
    }
  }

  function showIssuedPSK(info) {
    const once = document.getElementById("psk-once");
    const idBtn = el("button", { type: "button", class: btnGhost }, ["Download psk.id"]);
    const keyBtn = el("button", { type: "button", class: btnPrimary }, ["Download psk.key"]);
    idBtn.addEventListener("click", () => downloadText("psk.id", (info.id || "") + "\n"));
    keyBtn.addEventListener("click", () => downloadText("psk.key", (info.psk_key || "") + "\n"));
    once.replaceChildren(
      el("p", { class: "text-[13px] leading-relaxed text-amber-300" }, ["Download the key now, or copy both values. A later visit does not show the key again. Set the same DLOG_PSK_ID and DLOG_PSK_KEY on every agent."]),
      el("div", { class: "mt-3 flex flex-wrap gap-2" }, [idBtn, keyBtn]),
    );
    once.hidden = false;
  }

  async function replacePSK(e) {
    e.preventDefault();
    const id = document.getElementById("psk-replace-id").value.trim();
    const key = document.getElementById("psk-replace-key").value.trim();
    if (!id || !key) {
      toast("Enter an id and a key");
      return;
    }
    if (!confirm("Replace the shared key? Agents cannot register until they have this id and key. Hosts that already have a token keep sending logs.")) return;
    try {
      const info = await api("/api/v1/admin/psk", {
        method: "POST",
        body: JSON.stringify({ action: "replace", psk_id: id, psk_key: key }),
      });
      e.target.reset();
      document.getElementById("psk-once").hidden = true;
      document.getElementById("psk-once").replaceChildren();
      paintPSK(info);
      toast("Key replaced. Set the same id and key on every agent.");
    } catch (err) {
      toast(err.message);
    }
  }
}

function initAccount() {
  api("/api/v1/me").then((me) => {
    const summary = document.getElementById("account-summary");
    if (summary && me) summary.textContent = me.username + " · " + (me.role === "admin" ? "Admin" : "User");
    if (me && me.role === "admin") showAdminLinks();
    if (me && me.source === "directory") {
      document.getElementById("change-password").hidden = true;
      const note = document.getElementById("account-directory");
      if (note) note.hidden = false;
    }
  }).catch(() => {});
  document.getElementById("change-password").addEventListener("submit", async (e) => {
    e.preventDefault();
    const current = document.getElementById("current-password").value;
    const next = document.getElementById("new-password").value;
    const again = document.getElementById("confirm-password").value;
    if (next !== again) {
      toast("Passwords do not match");
      return;
    }
    const others = document.getElementById("sign-out-others").checked;
    try {
      await api("/api/v1/me", { method: "PATCH", body: JSON.stringify({ current_password: current, password: next, sign_out_others: others }) });
      e.target.reset();
      toast("Password updated");
    } catch (err) {
      toast(err.message);
    }
  });
}

function initAccounts() {
  const listEl = document.getElementById("accounts");
  const form = document.getElementById("create-user");
  let me = null;
  let groups = [];

  form.addEventListener("submit", async (e) => {
    e.preventDefault();
    const username = document.getElementById("new-username").value;
    const password = document.getElementById("new-password").value;
    const role = document.getElementById("new-role").value;
    try {
      await api("/api/v1/admin/users", { method: "POST", body: JSON.stringify({ username, password, role }) });
      form.reset();
      document.getElementById("new-role").value = "user";
      await load();
    } catch (err) {
      toast(err.message);
    }
  });
  load();

  async function load() {
    try {
      me = await api("/api/v1/me");
      const data = await api("/api/v1/admin/users");
      const fleet = await api("/api/v1/hosts");
      groups = (fleet.groups || []).slice().sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: "base" }) || a.id.localeCompare(b.id));
      paint(data.users || []);
    } catch (err) {
      if (err.message !== "unauthorized") toast(err.message);
    }
  }

  function paint(users) {
    listEl.replaceChildren();
    if (!users.length) {
      listEl.append(el("div", { class: emptyRow }, ["No accounts."]));
      return;
    }
    for (const u of users) listEl.append(accountRow(u));
  }

  function accountRow(u) {
    const directory = u.source === "directory";
    const who = u.role === "admin" ? "Admin" : "User";
    const del = el("button", { type: "button", class: btnDanger }, ["Delete"]);
    if (me && u.id === me.id) del.disabled = true;
    del.addEventListener("click", () => removeAccount(u));
    const head = el("div", { class: "flex flex-wrap items-center gap-2" }, [
      el("div", { class: "mr-auto min-w-0" }, [
        el("div", { class: "truncate text-[14px] font-medium text-slate-100" }, [u.username]),
        el("div", { class: "mt-0.5 text-[12px] text-slate-500" }, [directory ? who + " · Directory" : who]),
      ]),
    ]);
    if (!directory) {
      const role = el("select", { class: selectCls + " w-auto", "aria-label": "Permission for " + u.username }, [
        accountOption("user", "User", u.role === "user"),
        accountOption("admin", "Admin", u.role === "admin"),
      ]);
      role.addEventListener("change", () => saveRole(u, role));
      const password = el("input", {
        class: fieldBox + " w-48 max-w-full",
        type: "password",
        autocomplete: "new-password",
        placeholder: "New password",
        "aria-label": "New password for " + u.username,
      });
      const setPassword = el("button", { type: "button", class: btnGhost }, ["Set password"]);
      setPassword.addEventListener("click", () => savePassword(u, password));
      head.append(role, password, setPassword);
    }
    head.append(del);
    const row = el("div", { class: "border-b border-ink-line px-1 py-4" }, [head]);
    const access = accessEditor(u);
    if (access) row.append(access);
    return row;
  }

  function accessEditor(u) {
    if (u.role !== "user") return null;
    const all = el("input", { class: "h-3.5 w-3.5 cursor-pointer", type: "checkbox" });
    all.checked = !!u.all_hosts;
    const box = el("div", { class: "account-groups" });
    const selected = new Set(u.group_ids || []);
    if (!groups.length) {
      box.append(el("span", { class: "text-[12px] text-slate-500" }, ["No groups yet."]));
    }
    for (const g of groups) {
      const input = el("input", { class: "h-3.5 w-3.5 cursor-pointer", type: "checkbox", value: g.id });
      input.checked = selected.has(g.id);
      box.append(el("label", { class: "inline-flex cursor-pointer items-center gap-2 text-[13px] text-slate-300" }, [
        input,
        el("span", {}, [g.name]),
      ]));
    }
    const sync = () => { box.hidden = all.checked; };
    all.addEventListener("change", sync);
    sync();
    const save = el("button", { type: "button", class: btnGhost }, ["Save access"]);
    save.addEventListener("click", () => saveAccess(u, all, box));
    return el("div", { class: "mt-3 flex w-full flex-wrap items-center gap-x-4 gap-y-2" }, [
      el("label", { class: "inline-flex cursor-pointer items-center gap-2 text-[13px] text-slate-200" }, [
        all,
        el("span", {}, ["All hosts"]),
      ]),
      box,
      save,
    ]);
  }

  async function saveAccess(u, all, box) {
    const groupIDs = [];
    if (!all.checked) {
      for (const input of box.querySelectorAll('input[type="checkbox"]')) {
        if (input.checked && input.value) groupIDs.push(input.value);
      }
    }
    try {
      await api("/api/v1/admin/users/" + encodeURIComponent(u.id), {
        method: "PATCH",
        body: JSON.stringify({ all_hosts: all.checked, group_ids: groupIDs }),
      });
      await load();
    } catch (err) {
      toast(err.message);
      await load();
    }
  }

  function accountOption(value, label, selected) {
    const node = el("option", { value }, [label]);
    node.selected = selected;
    return node;
  }

  async function saveRole(u, select) {
    try {
      await api("/api/v1/admin/users/" + encodeURIComponent(u.id), { method: "PATCH", body: JSON.stringify({ role: select.value }) });
      await load();
    } catch (err) {
      toast(err.message);
      await load();
    }
  }

  async function savePassword(u, input) {
    if (!input.value) {
      toast("Enter a password");
      return;
    }
    try {
      await api("/api/v1/admin/users/" + encodeURIComponent(u.id), { method: "PATCH", body: JSON.stringify({ password: input.value }) });
      if (me && u.id === me.id) {
        location.href = "/login";
        return;
      }
      input.value = "";
      toast("Password updated. That account is signed out.");
    } catch (err) {
      toast(err.message);
    }
  }

  async function removeAccount(u) {
    if (!confirm("Delete account " + u.username + "?")) return;
    try {
      await api("/api/v1/admin/users/" + encodeURIComponent(u.id), { method: "DELETE" });
      await load();
    } catch (err) {
      toast(err.message);
    }
  }
}

async function api(path, opts = {}) {
  const headers = { accept: "application/json", "content-type": "application/json", ...(opts.headers || {}) };
  const res = await fetch(path, { ...opts, headers, credentials: "same-origin" });
  if (res.status === 401) {
    location.href = "/login";
    throw new Error("unauthorized");
  }
  if (res.status === 204) return null;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error(data.error || res.statusText);
  return data;
}

function toast(message) {
  const node = document.getElementById("toast");
  const body = document.getElementById("toast-body");
  if (!node || !body) return;
  body.textContent = message;
  node.hidden = false;
  clearTimeout(toast.timer);
  toast.timer = setTimeout(() => { node.hidden = true; }, 2400);
}

function showAdminLinks() {
  const node = document.getElementById("admin-link");
  if (node) node.hidden = false;
}

function chooseDuration(select, ms) {
  if (!select) return;
  const wanted = durationValue(ms);
  let found = false;
  for (const opt of select.options) {
    if (opt.value === wanted) found = true;
  }
  if (!found) select.append(el("option", { value: wanted }, [wanted]));
  select.value = wanted;
}

function durationValue(ms) {
  const n = Number(ms) || 0;
  if (n === 0) return "0";
  const sec = Math.round(n / 1000);
  if (sec % 86400 === 0) return (sec / 86400) + "d";
  if (sec % 3600 === 0) return (sec / 3600) + "h";
  if (sec % 60 === 0) return (sec / 60) + "m";
  return sec + "s";
}

function downloadText(filename, text) {
  const blob = new Blob([text], { type: "application/x-pem-file" });
  const url = URL.createObjectURL(blob);
  const link = el("a", { href: url, download: filename });
  document.body.append(link);
  link.click();
  link.remove();
  setTimeout(() => URL.revokeObjectURL(url), 1000);
}

function applyBrand() {
  const marks = document.querySelectorAll("[data-brand]");
  const tagline = document.querySelector("[data-tagline]");
  if (!marks.length && !tagline) return;
  fetch("/api/v1/brand", { headers: { accept: "application/json" }, credentials: "same-origin" })
    .then((res) => (res.ok ? res.json() : null))
    .then((data) => {
      if (!data || typeof data.name !== "string" || !data.name) return;
      marks.forEach((node) => { node.textContent = data.name; });
      const page = document.body && document.body.getAttribute("data-title");
      document.title = page ? page + " · " + data.name : data.name;
      if (tagline && typeof data.tagline === "string" && data.tagline) tagline.textContent = data.tagline;
    })
    .catch(() => {});
}

applyBrand();

function iconChevron() {
  const ns = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(ns, "svg");
  svg.setAttribute("viewBox", "0 0 16 16");
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "1.5");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  svg.setAttribute("aria-hidden", "true");
  svg.setAttribute("class", "chevron");
  const path = document.createElementNS(ns, "path");
  path.setAttribute("d", "M6 3.5 10.5 8 6 12.5");
  svg.append(path);
  return svg;
}

function iconClose() {
  const ns = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(ns, "svg");
  svg.setAttribute("viewBox", "0 0 16 16");
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "1.75");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("aria-hidden", "true");
  svg.setAttribute("class", "h-3 w-3");
  const path = document.createElementNS(ns, "path");
  path.setAttribute("d", "M4.5 4.5 11.5 11.5M11.5 4.5 4.5 11.5");
  svg.append(path);
  return svg;
}

function iconCopy() {
  const ns = "http://www.w3.org/2000/svg";
  const svg = document.createElementNS(ns, "svg");
  svg.setAttribute("viewBox", "0 0 16 16");
  svg.setAttribute("fill", "none");
  svg.setAttribute("stroke", "currentColor");
  svg.setAttribute("stroke-width", "1.4");
  svg.setAttribute("stroke-linecap", "round");
  svg.setAttribute("stroke-linejoin", "round");
  svg.setAttribute("aria-hidden", "true");
  svg.setAttribute("class", "h-3 w-3");
  const rect = document.createElementNS(ns, "rect");
  rect.setAttribute("x", "5.5");
  rect.setAttribute("y", "5.5");
  rect.setAttribute("width", "7.5");
  rect.setAttribute("height", "7.5");
  rect.setAttribute("rx", "1");
  const path = document.createElementNS(ns, "path");
  path.setAttribute("d", "M3.5 10.5H3a1 1 0 0 1-1-1V3a1 1 0 0 1 1-1h6.5a1 1 0 0 1 1 1v.5");
  svg.append(rect, path);
  return svg;
}

function el(tag, attrs, children) {
  const node = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (k === "class") node.className = v;
    else if (k === "onclick" && typeof v === "function") node.addEventListener("click", v);
    else if (v !== null && v !== undefined) node.setAttribute(k, v);
  }
  for (const child of children || []) {
    node.append(child instanceof Node ? child : document.createTextNode(String(child)));
  }
  return node;
}
