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
  const chipsEl = document.getElementById("chips");
  const logEl = document.getElementById("log");
  const metaEl = document.getElementById("logmeta");
  const qInput = document.getElementById("q");
  const streamSel = document.getElementById("stream");
  const followBtn = document.getElementById("follow");
  let hosts = [];
  let groups = [];
  let collapsed = loadCollapsed();
  let containers = [];
  let lines = [];
  let hostId = "";
  let containerId = "";
  let q = "";
  let stream = "";
  let follow = true;
  let gen = 0;
  let ready = false;
  let polling = false;
  let viewer = null;

  document.getElementById("logout").addEventListener("click", async () => {
    try { await api("/api/v1/logout", { method: "POST", body: "{}" }); } catch (e) { /* redirect anyway */ }
    location.href = "/login";
  });
  followBtn.addEventListener("click", () => {
    follow = !follow;
    followBtn.classList.toggle("on", follow);
    followBtn.classList.toggle("btn-warning", follow);
    followBtn.classList.toggle("btn-outline-secondary", !follow);
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
  document.addEventListener("selectionchange", () => {
    logEl.style.overflowAnchor = selectingLog() ? "none" : "";
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
  hostQEl.addEventListener("input", () => paintHosts());
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
        : "No hosts yet. Start an agent with the shared certificate and it will register itself.";
      hostsEl.append(el("p", { class: "text-secondary small px-3 py-2 mb-0" }, [msg]));
    } else if (!visible.length && !showedGroup) {
      hostsEl.append(el("p", { class: "text-secondary small px-3 py-2 mb-0" }, ["No matching hosts."]));
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
      class: "host-group d-flex justify-content-between align-items-baseline" + (loose ? " is-loose" : "") + (open ? "" : " is-collapsed"),
      "aria-expanded": open ? "true" : "false",
      onclick: () => toggleGroup(key),
    }, [
      el("span", { class: "d-flex align-items-center gap-2 min-w-0" }, [
        el("span", { class: "chevron", "aria-hidden": "true" }),
        el("span", { class: "label text-truncate" }, [name]),
      ]),
      el("span", { class: "count" }, [String(count)]),
    ]);
  }

  function hostButton(h) {
    const dotClass = h.status !== "enabled" ? "dot off" : (h.online ? "dot on" : "dot");
    return el("button", {
      type: "button",
      class: "list-group-item list-group-item-action host d-flex gap-2 text-start" + (h.id === hostId ? " active" : ""),
      onclick: () => selectHost(h.id),
    }, [
      el("span", { class: dotClass }),
      el("span", { class: "min-w-0" }, [
        el("span", { class: "d-block fw-semibold name text-truncate" }, [hostLabel(h)]),
        el("span", { class: "d-block small sub opacity-75" }, [hostDetail(h)]),
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
    chipsEl.append(chip("", "All containers", containerId === "", false));
    for (const c of containers) {
      const label = c.name + (c.state === "running" ? "" : " · " + c.state);
      chipsEl.append(chip(c.id, label, c.id === containerId, c.state !== "running"));
    }
  }

  function chip(id, label, selected, exited) {
    return el("button", {
      type: "button",
      class: "btn btn-sm rounded-pill text-nowrap " + (selected ? "btn-warning" : "btn-outline-secondary") + (exited ? " opacity-50" : ""),
      onclick: () => {
        containerId = id;
        paintChips();
        reload();
      },
    }, [label]);
  }

  async function reload() {
    const my = ++gen;
    ready = false;
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
        logEl.append(lineNode(ln));
      }
      if (lines.length > 3000) {
        lines = lines.slice(lines.length - 3000);
        paintLines({ stick });
      } else {
        const label = lineLabel(lines.length);
        if (metaEl.textContent !== label) metaEl.textContent = label;
        if (stick) pinBottom();
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
    const prevHeight = logEl.scrollHeight;
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

  function pinBottom() {
    const top = Math.max(0, logEl.scrollHeight - logEl.clientHeight);
    if (logEl.scrollTop !== top) logEl.scrollTop = top;
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
    const changed = !sameLineIds(want);
    const manageScroll = changed && !!(opts.stick || opts.anchorHeight != null);
    if (manageScroll) logEl.style.overflowAnchor = "none";
    if (changed) {
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
        logEl.insertBefore(found || lineNode(lines[i]), cursor);
      }
      while (cursor) {
        const next = cursor.nextElementSibling;
        cursor.remove();
        cursor = next;
      }
    }
    const label = hostId ? (lines.length ? lineLabel(lines.length) : "No lines") : "Select a host";
    if (metaEl.textContent !== label) metaEl.textContent = label;
    if (opts.stick) pinBottom();
    else if (opts.anchorHeight != null) {
      const top = logEl.scrollHeight - opts.anchorHeight;
      if (logEl.scrollTop !== top) logEl.scrollTop = top;
    }
    if (manageScroll) logEl.style.overflowAnchor = selectingLog() ? "none" : "";
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
  const parts = [status, ago(h.last_seen_at), h.container_count + " containers"];
  if (String(h.display_name || "").trim()) parts.push(h.hostname);
  if (h.dropped) parts.push("dropped " + h.dropped);
  return parts.join(" · ");
}

function statusBadge(h) {
  if (h.status !== "enabled") return el("span", { class: "badge text-bg-danger" }, ["disabled"]);
  if (h.online) return el("span", { class: "badge text-bg-success" }, ["online"]);
  return el("span", { class: "badge text-bg-secondary" }, ["offline"]);
}

function lineNode(ln) {
  const streamClass = ln.stream === "stderr" ? "text-bg-danger" : "text-bg-secondary";
  return el("div", {
    class: "log-line" + (ln.stream === "stderr" ? " stderr" : ""),
    title: ln.image || "",
    "data-id": String(ln.id),
  }, [
    el("span", { class: "ts" }, [formatTime(ln.timestamp)]),
    el("span", { class: "name" }, [ln.container_name || ln.container_id]),
    el("span", { class: "badge " + streamClass }, [ln.stream]),
    el("span", { class: "msg" }, [ln.message]),
  ]);
}

function lineLabel(n) {
  return n === 1 ? "1 line" : n + " lines";
}

function nearBottom() {
  const node = document.getElementById("log");
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
  const sections = ["hosts", "general", "registration", "certificate", "groups", "accounts", "directory"];
  let hosts = [];
  let groups = [];
  let savedSettings = null;
  let generalDirty = false;
  let registrationDirty = false;
  let directoryDirty = false;

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
      result.classList.remove("text-danger");
      result.classList.add("text-success");
      result.textContent = "Connected to the directory.";
      result.hidden = false;
    } catch (err) {
      result.classList.remove("text-success");
      result.classList.add("text-danger");
      result.textContent = err.message;
      result.hidden = false;
    } finally {
      button.disabled = false;
      button.textContent = label;
    }
  });
  document.getElementById("generate-cert").addEventListener("click", generateCert);
  document.getElementById("replace-cert").addEventListener("submit", replaceCert);
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
      const msg = hosts.length ? "No matching hosts." : "No hosts yet. Start an agent with the shared certificate and it will register itself.";
      hostList.append(el("div", { class: "list-group-item text-secondary" }, [msg]));
      return;
    }
    for (const h of visible) hostList.append(hostRow(h));
  }

  function hostRow(h) {
    const row = el("div", { class: "list-group-item admin-host" });
    const deleteBtn = el("button", { type: "button", class: "btn btn-sm btn-outline-danger" }, ["Delete"]);
    const statusBtn = h.status === "enabled"
      ? el("button", { type: "button", class: "btn btn-sm btn-outline-warning" }, ["Disable"])
      : el("button", { type: "button", class: "btn btn-sm btn-outline-success" }, ["Enable"]);
    deleteBtn.addEventListener("click", () => removeHost(h));
    statusBtn.addEventListener("click", () => setStatus(h, h.status === "enabled" ? "disable" : "enable"));
    const sel = el("select", { class: "form-select form-select-sm admin-group-select", "aria-label": "Group for " + h.hostname });
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
      class: "form-control form-control-sm admin-display-name",
      type: "text",
      maxlength: "64",
      autocomplete: "off",
      spellcheck: "false",
      placeholder: "Display name",
      "aria-label": "Display name for " + h.hostname,
    });
    nameInput.value = h.display_name || "";
    const saveName = el("button", { type: "button", class: "btn btn-sm btn-outline-secondary" }, ["Save name"]);
    saveName.addEventListener("click", () => saveDisplayName(h, nameInput.value));
    nameInput.addEventListener("keydown", (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        saveDisplayName(h, nameInput.value);
      }
    });
    const meta = h.hostname + " · seen " + ago(h.last_seen_at) + (h.dropped ? " · dropped " + h.dropped : "") + " · " + h.container_count + " containers";
    row.append(
      el("div", { class: "d-flex flex-wrap align-items-center gap-2" }, [
        el("span", { class: "fw-semibold text-truncate me-auto" }, [hostLabel(h)]),
        statusBadge(h),
        el("div", { class: "btn-group" }, [statusBtn, deleteBtn]),
      ]),
      el("div", { class: "text-secondary small mt-1" }, [meta]),
      el("div", { class: "d-flex flex-wrap gap-2 my-2" }, [nameInput, saveName]),
      sel,
    );
    return row;
  }

  function paintGroups() {
    groupList.replaceChildren();
    if (!groups.length) {
      groupList.append(el("div", { class: "list-group-item text-secondary" }, ["No groups yet."]));
      return;
    }
    for (const g of groups) groupList.append(groupRow(g));
  }

  function groupRow(g) {
    const row = el("div", { class: "list-group-item d-flex align-items-center gap-2" });
    const renameBtn = el("button", { type: "button", class: "btn btn-sm btn-outline-secondary" }, ["Rename"]);
    const deleteBtn = el("button", { type: "button", class: "btn btn-sm btn-outline-danger" }, ["Delete"]);
    renameBtn.addEventListener("click", () => beginRenameGroup(g, row));
    deleteBtn.addEventListener("click", () => deleteGroup(g));
    row.append(
      el("div", { class: "min-w-0 flex-grow-1" }, [
        el("div", { class: "fw-semibold text-truncate" }, [g.name]),
        el("div", { class: "text-secondary small" }, [g.host_count === 1 ? "1 host" : g.host_count + " hosts"]),
      ]),
      el("div", { class: "btn-group flex-shrink-0" }, [renameBtn, deleteBtn]),
    );
    return row;
  }

  function beginRenameGroup(g, row) {
    const input = el("input", {
      class: "form-control form-control-sm",
      type: "text",
      maxlength: "64",
      autocomplete: "off",
      spellcheck: "false",
      "aria-label": "Group name",
    });
    input.value = g.name;
    const save = el("button", { type: "button", class: "btn btn-sm btn-warning" }, ["Save"]);
    const cancel = el("button", { type: "button", class: "btn btn-sm btn-outline-secondary" }, ["Cancel"]);
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
    row.replaceChildren(el("div", { class: "d-flex gap-2 w-100" }, [input, save, cancel]));
    input.classList.add("flex-grow-1");
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
      const [data, cert] = await Promise.all([
        api("/api/v1/admin/settings"),
        api("/api/v1/admin/certificate"),
      ]);
      fillSettings(data);
      paintCert(cert);
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
    const list = document.getElementById("pending-hosts");
    if (!list) return;
    list.replaceChildren();
    if (!items.length) {
      list.append(el("div", { class: "list-group-item text-secondary" }, ["No hosts are waiting."]));
      return;
    }
    for (const item of items) {
      const approve = el("button", { type: "button", class: "btn btn-sm btn-warning" }, ["Approve"]);
      const dismiss = el("button", { type: "button", class: "btn btn-sm btn-outline-secondary" }, ["Dismiss"]);
      approve.addEventListener("click", () => decidePending(item.hostname, "approve"));
      dismiss.addEventListener("click", () => decidePending(item.hostname, "dismiss"));
      const row = el("div", { class: "list-group-item d-flex flex-wrap align-items-center gap-2" }, [
        el("div", { class: "me-auto" }, [
          el("div", { class: "fw-semibold" }, [item.hostname]),
          el("div", { class: "text-secondary small" }, [item.agent_version ? "agent " + item.agent_version : "agent"]),
        ]),
        el("div", { class: "btn-group" }, [approve, dismiss]),
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

  function paintCert(info) {
    document.getElementById("cert-fingerprint").textContent = info && info.fingerprint ? info.fingerprint : "";
    const until = info && info.not_after ? new Date(info.not_after) : null;
    document.getElementById("cert-expiry").textContent = until && !Number.isNaN(until.getTime()) ? "Valid until " + until.toLocaleString() : "";
    const paths = document.getElementById("cert-paths");
    paths.textContent = info && info.cert_path ? info.cert_path + "  ·  " + info.key_path : "";
  }

  async function generateCert() {
    if (!confirm("Generate a new agent certificate? Agents cannot register until they have the new certificate and key. Hosts that already have a token keep sending logs.")) return;
    try {
      const info = await api("/api/v1/admin/certificate", { method: "POST", body: JSON.stringify({ action: "generate" }) });
      paintCert(info);
      showIssuedCert(info);
      toast("New certificate is ready. Download the key before you leave this page.");
    } catch (err) {
      toast(err.message);
    }
  }

  function showIssuedCert(info) {
    const once = document.getElementById("cert-once");
    const certBtn = el("button", { type: "button", class: "btn btn-sm btn-outline-light" }, ["Download certificate"]);
    const keyBtn = el("button", { type: "button", class: "btn btn-sm btn-warning" }, ["Download private key"]);
    certBtn.addEventListener("click", () => downloadText("agent.crt", info.cert_pem || ""));
    keyBtn.addEventListener("click", () => downloadText("agent.key", info.key_pem || ""));
    once.replaceChildren(
      el("p", { class: "text-warning small" }, ["Download the private key now. A later visit to this page does not show it again. The same files are stored on the server."]),
      el("div", { class: "d-flex flex-wrap gap-2" }, [certBtn, keyBtn]),
    );
    once.hidden = false;
  }

  async function replaceCert(e) {
    e.preventDefault();
    const certFile = document.getElementById("cert-file").files && document.getElementById("cert-file").files[0];
    const keyFile = document.getElementById("key-file").files && document.getElementById("key-file").files[0];
    if (!certFile || !keyFile) {
      toast("Choose a certificate and a private key");
      return;
    }
    if (!confirm("Replace the agent certificate? Agents cannot register until they have this certificate and key. Hosts that already have a token keep sending logs.")) return;
    try {
      const certPEM = await certFile.text();
      const keyPEM = await keyFile.text();
      const info = await api("/api/v1/admin/certificate", {
        method: "POST",
        body: JSON.stringify({ action: "replace", cert_pem: certPEM, key_pem: keyPEM }),
      });
      e.target.reset();
      document.getElementById("cert-once").hidden = true;
      document.getElementById("cert-once").replaceChildren();
      paintCert(info);
      toast("Certificate replaced. Install it on every agent.");
    } catch (err) {
      toast(err.message);
    }
  }
}

function initAccount() {
  document.getElementById("logout").addEventListener("click", async () => {
    try { await api("/api/v1/logout", { method: "POST", body: "{}" }); } catch (e) { /* redirect anyway */ }
    location.href = "/login";
  });
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

  document.getElementById("logout").addEventListener("click", async () => {
    try { await api("/api/v1/logout", { method: "POST", body: "{}" }); } catch (e) { /* redirect anyway */ }
    location.href = "/login";
  });
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
      listEl.append(el("div", { class: "list-group-item text-secondary" }, ["No accounts."]));
      return;
    }
    for (const u of users) listEl.append(accountRow(u));
  }

  function accountRow(u) {
    const directory = u.source === "directory";
    const who = u.role === "admin" ? "Admin" : "User";
    const del = el("button", { type: "button", class: "btn btn-sm btn-outline-danger" }, ["Delete"]);
    if (me && u.id === me.id) del.disabled = true;
    del.addEventListener("click", () => removeAccount(u));
    const row = el("div", { class: "list-group-item d-flex flex-wrap align-items-center gap-2" }, [
      el("div", { class: "me-auto min-w-0" }, [
        el("div", { class: "fw-semibold text-truncate" }, [u.username]),
        el("div", { class: "text-secondary small" }, [directory ? who + " · Directory" : who]),
      ]),
    ]);
    if (!directory) {
      const role = el("select", { class: "form-select form-select-sm w-auto", "aria-label": "Permission for " + u.username }, [
        accountOption("user", "User", u.role === "user"),
        accountOption("admin", "Admin", u.role === "admin"),
      ]);
      role.addEventListener("change", () => saveRole(u, role));
      const password = el("input", {
        class: "form-control form-control-sm account-password",
        type: "password",
        autocomplete: "new-password",
        placeholder: "New password",
        "aria-label": "New password for " + u.username,
      });
      const setPassword = el("button", { type: "button", class: "btn btn-sm btn-outline-secondary" }, ["Set password"]);
      setPassword.addEventListener("click", () => savePassword(u, password));
      row.append(role, password, setPassword);
    }
    row.append(del);
    const access = accessEditor(u);
    if (access) row.append(access);
    return row;
  }

  function accessEditor(u) {
    if (u.role !== "user") return null;
    const all = el("input", { class: "form-check-input", type: "checkbox" });
    all.checked = !!u.all_hosts;
    const box = el("div", { class: "account-groups" });
    const selected = new Set(u.group_ids || []);
    if (!groups.length) {
      box.append(el("span", { class: "text-secondary small" }, ["No groups yet."]));
    }
    for (const g of groups) {
      const input = el("input", { class: "form-check-input", type: "checkbox", value: g.id });
      input.checked = selected.has(g.id);
      box.append(el("label", { class: "form-check form-check-inline mb-0" }, [
        input,
        el("span", { class: "form-check-label" }, [g.name]),
      ]));
    }
    const sync = () => { box.hidden = all.checked; };
    all.addEventListener("change", sync);
    sync();
    const save = el("button", { type: "button", class: "btn btn-sm btn-outline-secondary" }, ["Save access"]);
    save.addEventListener("click", () => saveAccess(u, all, box));
    return el("div", { class: "w-100 d-flex flex-wrap align-items-center gap-2" }, [
      el("label", { class: "form-check mb-0" }, [
        all,
        el("span", { class: "form-check-label" }, ["All hosts"]),
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
  if (!node || !body || typeof bootstrap === "undefined") return;
  body.textContent = message;
  bootstrap.Toast.getOrCreateInstance(node, { delay: 2400 }).show();
}

function showAdminLinks() {
  for (const id of ["admin-link", "settings-link"]) {
    const node = document.getElementById(id);
    if (node) node.hidden = false;
  }
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
