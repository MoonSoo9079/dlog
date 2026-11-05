const loginError = document.getElementById("login-error");
if (loginError && new URLSearchParams(location.search).get("error")) {
  loginError.hidden = false;
}

if (document.getElementById("hosts")) {
  initApp();
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
  const drawer = document.getElementById("drawer");
  const drawerBody = document.getElementById("drawer-body");
  const offcanvas = bootstrap.Offcanvas.getOrCreateInstance(drawer);

  let hosts = [];
  let groups = [];
  let drawerQuery = "";
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
  document.getElementById("manage").addEventListener("click", openDrawer);
  document.getElementById("drawer-close").addEventListener("click", closeDrawer);
  streamSel.addEventListener("change", () => { stream = streamSel.value; reload(); });
  let qTimer = 0;
  qInput.addEventListener("input", () => {
    clearTimeout(qTimer);
    qTimer = setTimeout(() => { q = qInput.value.trim(); reload(); }, 250);
  });
  hostQEl.addEventListener("input", () => paintHosts());
  document.addEventListener("keydown", (e) => {
    if (e.key === "/" && !/^(INPUT|TEXTAREA|SELECT)$/.test(document.activeElement.tagName)) {
      e.preventDefault();
      qInput.focus();
    }
    if (e.key === "Escape") closeDrawer();
  });

  refreshHosts();
  api("/api/v1/meta").then((m) => {
    const version = document.getElementById("version");
    version.textContent = m.version ? "v" + m.version : "";
  }).catch(() => {});
  setInterval(() => { if (!document.hidden) refreshHosts(); }, 5000);
  setInterval(() => { if (!document.hidden) pollLogs(); }, 1000);

  async function refreshHosts() {
    try {
      const data = await api("/api/v1/hosts");
      hosts = data.hosts || [];
      groups = (data.groups || []).slice().sort((a, b) => a.name.localeCompare(b.name, undefined, { sensitivity: "base" }) || a.id.localeCompare(b.id));
      if (hostId && !hosts.some((h) => h.id === hostId)) hostId = "";
      if (!hostId && hosts.length) hostId = hosts[0].id;
      paintHosts();
      if (hostId && !ready) reload();
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
      hostsEl.append(groupLabel(g.name, members.length, false));
      for (const h of members) hostsEl.append(hostButton(h));
    }
    if (ungrouped.length && groups.length) {
      showedGroup = true;
      hostsEl.append(groupLabel("Ungrouped", ungrouped.length, true));
    }
    for (const h of ungrouped) hostsEl.append(hostButton(h));
    if (!hosts.length) {
      hostsEl.append(el("p", { class: "text-secondary small px-3 py-2 mb-0" }, ["No hosts yet. Start an agent with the shared certificate and it will register itself."]));
    } else if (!visible.length && !showedGroup) {
      hostsEl.append(el("p", { class: "text-secondary small px-3 py-2 mb-0" }, ["No matching hosts."]));
    }
  }

  function matchesHost(h, query) {
    if (!query) return true;
    if (String(h.hostname || "").toLowerCase().includes(query)) return true;
    const g = groups.find((item) => item.id === (h.group_id || ""));
    if (g && g.name.toLowerCase().includes(query)) return true;
    if (!g && "ungrouped".includes(query)) return true;
    return false;
  }

  function groupLabel(name, count, loose) {
    return el("div", { class: "host-group d-flex justify-content-between align-items-baseline" + (loose ? " is-loose" : "") }, [
      el("span", { class: "label text-truncate" }, [name]),
      el("span", { class: "count" }, [String(count)]),
    ]);
  }

  function hostButton(h) {
    const status = h.status !== "enabled" ? "disabled" : (h.online ? "online" : "offline");
    const dotClass = h.status !== "enabled" ? "dot off" : (h.online ? "dot on" : "dot");
    return el("button", {
      type: "button",
      class: "list-group-item list-group-item-action host d-flex gap-2 text-start" + (h.id === hostId ? " active" : ""),
      onclick: () => selectHost(h.id),
    }, [
      el("span", { class: dotClass }),
      el("span", { class: "min-w-0" }, [
        el("span", { class: "d-block fw-semibold name text-truncate" }, [h.hostname]),
        el("span", { class: "d-block small sub opacity-75" }, [status + " · " + ago(h.last_seen_at) + " · " + h.container_count + " containers"]),
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
    lines = [];
    paintLines();
    if (!hostId) { ready = true; return; }
    try {
      const data = await api(logURL());
      if (my !== gen) return;
      lines = data.lines || [];
      paintLines();
      logEl.scrollTop = logEl.scrollHeight;
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
      const stick = nearBottom();
      for (const ln of data.lines) {
        lines.push(ln);
        logEl.append(lineNode(ln));
      }
      if (lines.length > 3000) {
        lines = lines.slice(lines.length - 3000);
        paintLines();
      } else {
        metaEl.textContent = lineLabel(lines.length);
      }
      if (stick) logEl.scrollTop = logEl.scrollHeight;
    } catch (e) {
      if (e.message !== "unauthorized") toast(e.message);
    } finally {
      polling = false;
    }
  }

  async function loadOlder() {
    if (!hostId || !lines.length) { toast("No older lines"); return; }
    const my = gen;
    const prev = logEl.scrollHeight;
    try {
      const data = await api(logURL({ before_id: String(lines[0].id) }));
      if (my !== gen) return;
      if (!data.lines || !data.lines.length) { toast("No older lines"); return; }
      lines = data.lines.concat(lines);
      paintLines();
      logEl.scrollTop = logEl.scrollHeight - prev;
    } catch (e) {
      if (e.message !== "unauthorized") toast(e.message);
    }
  }

  function paintLines() {
    const frag = document.createDocumentFragment();
    for (const ln of lines) frag.append(lineNode(ln));
    logEl.replaceChildren(frag);
    metaEl.textContent = hostId ? (lines.length ? lineLabel(lines.length) : "No lines") : "Select a host";
  }

  function logURL(extra) {
    const p = new URLSearchParams();
    p.set("host_id", hostId);
    if (containerId) p.set("container_id", containerId);
    if (q) p.set("q", q);
    if (stream) p.set("stream", stream);
    p.set("limit", "300");
    if (extra) for (const [k, v] of Object.entries(extra)) p.set(k, v);
    return "/api/v1/logs?" + p.toString();
  }

  function openDrawer() {
    paintDrawer();
    offcanvas.show();
  }
  function closeDrawer() { offcanvas.hide(); }

  function paintDrawer() {
    const prev = document.getElementById("drawer-q");
    const refocus = !!(prev && document.activeElement === prev);
    const start = refocus ? prev.selectionStart : 0;
    const end = refocus ? prev.selectionEnd : 0;

    drawerBody.replaceChildren();
    drawerBody.append(el("p", { class: "text-secondary small" }, ["Agents that hold this server's certificate register themselves. The hostname is the host. Put a host in one group to organize the list. Disable a host to stop it."]));

    drawerBody.append(el("h3", { class: "h6 mt-3 mb-2" }, ["Groups"]));
    const createInput = el("input", {
      id: "group-name",
      class: "form-control form-control-sm",
      type: "text",
      maxlength: "64",
      placeholder: "New group",
      autocomplete: "off",
      spellcheck: "false",
      "aria-label": "New group name",
    });
    const createForm = el("form", { class: "d-flex gap-2 mb-2" }, [
      createInput,
      el("button", { type: "submit", class: "btn btn-sm btn-warning text-nowrap" }, ["Create"]),
    ]);
    createForm.addEventListener("submit", (e) => {
      e.preventDefault();
      createGroup(createInput.value);
    });
    drawerBody.append(createForm);

    const groupList = el("div", { class: "list-group mb-3" });
    if (!groups.length) groupList.append(el("div", { class: "list-group-item text-secondary small" }, ["No groups yet."]));
    for (const g of groups) groupList.append(groupRow(g));
    drawerBody.append(groupList);

    drawerBody.append(el("h3", { class: "h6 mb-2" }, ["Hosts"]));
    const filter = el("input", {
      id: "drawer-q",
      class: "form-control form-control-sm mb-2",
      type: "search",
      placeholder: "Search hosts",
      autocomplete: "off",
      spellcheck: "false",
      "aria-label": "Search hosts in this panel",
    });
    filter.value = drawerQuery;
    filter.addEventListener("input", () => {
      drawerQuery = filter.value;
      paintDrawerHosts();
    });
    drawerBody.append(filter);
    drawerBody.append(el("div", { id: "drawer-hosts", class: "list-group" }));
    paintDrawerHosts();

    if (refocus) {
      const again = document.getElementById("drawer-q");
      again.focus();
      again.setSelectionRange(start, end);
    }
  }

  function groupRow(g) {
    const row = el("div", { class: "list-group-item d-flex align-items-center gap-2" });
    const renameBtn = el("button", { type: "button", class: "btn btn-sm btn-outline-secondary" }, ["Rename"]);
    const deleteBtn = el("button", { type: "button", class: "btn btn-sm btn-outline-danger" }, ["Delete"]);
    renameBtn.addEventListener("click", () => beginRename(g, row));
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

  function beginRename(g, row) {
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
    cancel.addEventListener("click", () => paintDrawer());
    input.addEventListener("keydown", (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        renameGroup(g, input.value);
      } else if (e.key === "Escape") {
        e.stopPropagation();
        paintDrawer();
      }
    });
    row.replaceChildren(el("div", { class: "d-flex gap-2 w-100" }, [input, save, cancel]));
    input.classList.add("flex-grow-1");
    input.focus();
    input.select();
  }

  function paintDrawerHosts() {
    const list = document.getElementById("drawer-hosts");
    if (!list) return;
    const query = drawerQuery.trim().toLowerCase();
    const visible = hosts.filter((h) => matchesHost(h, query));
    list.replaceChildren();
    if (!visible.length) {
      list.append(el("div", { class: "list-group-item text-secondary small" }, [hosts.length ? "No matching hosts." : "None yet."]));
      return;
    }
    for (const h of visible) {
      const current = h.group_id || "";
      const sel = el("select", { class: "form-select form-select-sm", "aria-label": "Group for " + h.hostname });
      const none = el("option", { value: "" }, ["Ungrouped"]);
      if (!current) none.selected = true;
      sel.append(none);
      for (const g of groups) {
        const opt = el("option", { value: g.id }, [g.name]);
        if (g.id === current) opt.selected = true;
        sel.append(opt);
      }
      sel.addEventListener("change", () => assignGroup(h, sel.value));
      const actions = [
        el("button", { type: "button", class: "btn btn-sm btn-outline-danger", onclick: () => removeHost(h) }, ["Delete"]),
      ];
      if (h.status === "enabled") actions.unshift(el("button", { type: "button", class: "btn btn-sm btn-outline-warning", onclick: () => setStatus(h, "disable") }, ["Disable"]));
      else actions.unshift(el("button", { type: "button", class: "btn btn-sm btn-outline-success", onclick: () => setStatus(h, "enable") }, ["Enable"]));
      list.append(el("div", { class: "list-group-item" }, [
        el("div", { class: "d-flex align-items-center gap-2 min-w-0" }, [
          el("span", { class: "fw-semibold text-truncate" }, [h.hostname]),
          statusBadge(h),
        ]),
        el("div", { class: "text-secondary small mt-1 mb-2" }, ["seen " + ago(h.last_seen_at) + (h.dropped ? " · dropped " + h.dropped : "") + " · " + h.container_count + " containers"]),
        el("div", { class: "d-flex flex-wrap align-items-center gap-2" }, [
          sel,
          el("div", { class: "btn-group" }, actions),
        ]),
      ]));
    }
  }

  async function createGroup(name) {
    try {
      await api("/api/v1/admin/groups", { method: "POST", body: JSON.stringify({ name: name.trim() }) });
      await refreshHosts();
      paintDrawer();
    } catch (e) { toast(e.message); }
  }

  async function renameGroup(g, name) {
    try {
      await api("/api/v1/admin/groups/" + encodeURIComponent(g.id), { method: "PATCH", body: JSON.stringify({ name: name.trim() }) });
      await refreshHosts();
      paintDrawer();
    } catch (e) { toast(e.message); }
  }

  async function deleteGroup(g) {
    if (!confirm("Delete group " + g.name + "? Hosts in it become ungrouped.")) return;
    try {
      await api("/api/v1/admin/groups/" + encodeURIComponent(g.id), { method: "DELETE" });
      await refreshHosts();
      paintDrawer();
    } catch (e) { toast(e.message); }
  }

  async function assignGroup(h, groupId) {
    try {
      await api("/api/v1/admin/hosts/" + encodeURIComponent(h.id) + "/group", { method: "PATCH", body: JSON.stringify({ group_id: groupId }) });
      await refreshHosts();
      paintDrawer();
    } catch (e) {
      toast(e.message);
      paintDrawer();
    }
  }

  async function setStatus(h, action) {
    try {
      await api("/api/v1/admin/hosts/" + h.id + "/" + action, { method: "POST", body: "{}" });
      await refreshHosts();
      paintDrawer();
    } catch (e) { toast(e.message); }
  }

  async function removeHost(h) {
    if (!confirm("Delete " + h.hostname + " and its stored logs?")) return;
    try {
      await api("/api/v1/admin/hosts/" + h.id, { method: "DELETE" });
      if (hostId === h.id) { hostId = ""; lines = []; ready = false; }
      await refreshHosts();
      paintDrawer();
    } catch (e) { toast(e.message); }
  }
}

function statusBadge(h) {
  if (h.status !== "enabled") return el("span", { class: "badge text-bg-danger" }, ["disabled"]);
  if (h.online) return el("span", { class: "badge text-bg-success" }, ["online"]);
  return el("span", { class: "badge text-bg-secondary" }, ["offline"]);
}

function lineNode(ln) {
  const streamClass = ln.stream === "stderr" ? "text-bg-danger" : "text-bg-secondary";
  return el("div", { class: "log-line" + (ln.stream === "stderr" ? " stderr" : ""), title: ln.image || "" }, [
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
