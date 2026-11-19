try {
  if (localStorage.getItem("dlog.hosts-pane") === "closed") {
    document.documentElement.classList.add("hosts-collapsed");
  }
} catch (e) {}
