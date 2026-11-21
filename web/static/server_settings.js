/* Server navigation owns presentation only; pricing and permissions are server-side. */
(function initializeServerSettingsNavigation() {
  "use strict";
  const serverPanels = Array.from(document.querySelectorAll("[data-expense-server-panel]"));
  if (!serverPanels.length) return;
  const selector = document.querySelector("[data-expense-server-select]");
  const validSections = new Set(["overview", "monetization", "network", "updates", "clients", "registration", "demo"]);

  function fromHash() {
    const fragment = window.location.hash.slice(1);
    if (fragment.startsWith("server-id=")) {
      const values = new URLSearchParams(fragment);
      return {id: values.get("server-id") || "", section: values.get("section") || "overview"};
    }
    const legacy = fragment === "server-update" ? "updates" : fragment.startsWith("server-") ? fragment.slice(7) : "";
    return {id:"", section:validSections.has(legacy) ? legacy : "overview"};
  }
  function writeHash(panel, section) {
    const values = new URLSearchParams({"server-id":panel.dataset.serverId || "", "section":section});
    history.replaceState(null, "", "#" + values.toString());
  }
  function openUpdater(layout) {
    const frame = layout.querySelector("[data-server-update-frame]");
    if (!frame || frame.dataset.started) return;
    const loading = layout.querySelector("[data-server-update-frame-loading]");
    const error = layout.querySelector("[data-server-update-frame-error]");
    frame.dataset.started = "true";
    let completed = false;
    function finish(success) {
      if (completed) return;
      completed = true;
      loading.hidden = true;
      frame.hidden = !success;
      error.hidden = success;
    }
    frame.addEventListener("load", () => {
      let valid = false;
      try {
        const documentRoot = frame.contentDocument;
        valid = Boolean(documentRoot && (documentRoot.querySelector("[data-server-update]") || documentRoot.querySelector("[data-server-update-diagnostic]")));
      } catch (_) { /* Do not mistake a blocked frame for a ready updater. */ }
      finish(valid);
    }, {once:true});
    frame.addEventListener("error", () => finish(false), {once:true});
    window.setTimeout(() => finish(false), 20000);
    frame.src = frame.dataset.src;
  }
  function choose(panel, wanted) {
    const layout = panel.querySelector("[data-server-config-layout]");
    if (!layout) return;
    const views = Array.from(layout.querySelectorAll("[data-server-config-view]"));
    const available = new Set(views.map(view => view.dataset.serverConfigView));
    const section = available.has(wanted) ? wanted : "overview";
    for (const view of views) view.hidden = view.dataset.serverConfigView !== section;
    for (const button of layout.querySelectorAll("[data-server-config-target]")) {
      const active = button.dataset.serverConfigTarget === section;
      button.setAttribute("aria-current", active ? "page" : "false");
      button.classList.toggle("is-active", active);
    }
    layout.dataset.serverConfigSelected = section;
    if (section === "updates") openUpdater(layout);
  }
  function displayHash() {
    const state = fromHash();
    let target = state.id ? serverPanels.find(panel => panel.dataset.serverId === state.id) : null;
    if (!target && selector) target = serverPanels.find(panel => panel.dataset.expenseServerPanel === selector.value);
    if (!target) target = serverPanels.find(panel => !panel.hidden) || serverPanels[0];
    if (!target) return;
    for (const panel of serverPanels) panel.hidden = panel !== target;
    if (selector) selector.value = target.dataset.expenseServerPanel;
    choose(target, state.section);
  }

  for (const panel of serverPanels) {
    const layout = panel.querySelector("[data-server-config-layout]");
    if (!layout) continue;
    const nav = layout.querySelector(".server-config-nav");
    if (!nav) continue;
    nav.hidden = false;
    layout.addEventListener("click", event => {
      const button = event.target.closest("[data-server-config-target]");
      if (!button || !layout.contains(button)) return;
      choose(panel, button.dataset.serverConfigTarget);
      writeHash(panel, layout.dataset.serverConfigSelected);
    });
    layout.addEventListener("submit", event => {
      const form = event.target;
      if (!(form instanceof HTMLFormElement) || form.method.toLowerCase() !== "post" || !form.closest("[data-server-config-view]")) return;
      const values = new URLSearchParams({"server-id":panel.dataset.serverId || "", "section":layout.dataset.serverConfigSelected || "overview"});
      form.action = form.action.split("#", 1)[0] + "#" + values.toString();
    });
  }
  if (selector) selector.addEventListener("change", () => {
    const panel = serverPanels.find(candidate => candidate.dataset.expenseServerPanel === selector.value);
    if (!panel) return;
    serverPanels.forEach(candidate => candidate.hidden = candidate !== panel);
    choose(panel, "overview");
    writeHash(panel, "overview");
  });
  window.addEventListener("hashchange", displayHash);
  displayHash();

  // Live preview is explanatory. Go is the only source of billing amounts.
  document.querySelectorAll("[data-server-tariff]").forEach(form => {
    const cost = form.querySelector('[name="monthly_server_expense"]');
    const currency = form.querySelector('[name="currency"]');
    const percent = form.querySelector('[name="billing_capacity_percent"]');
    const threshold = form.querySelector('[name="free_site_threshold_mb"]');
    const capacity = form.querySelector("[data-tariff-capacity]");
    const price = form.querySelector("[data-tariff-price]");
    const confirmRow = form.querySelector("[data-tariff-confirm-row]");
    const confirmation = form.querySelector("[data-tariff-confirm]");
    const button = form.querySelector("[data-tariff-save]");
    const initial = [cost.value,currency.value,percent.value,threshold.value];
    function render() {
      const diskGB = Number(form.dataset.diskBytes) / 1000000000;
      const portion = Number(percent.value);
      const monthly = Number(cost.value);
      const capacityGB = diskGB * portion / 100;
      const valid = Number.isFinite(capacityGB) && capacityGB > 0 && portion>=1 && portion<=100 && Number.isFinite(monthly) && monthly>0;
      capacity.textContent = valid ? new Intl.NumberFormat(document.documentElement.lang || "en", {maximumFractionDigits:1}).format(capacityGB) + " GB" : "—";
      const currencyCode = /^[A-Za-z]{3}$/.test(currency.value) ? currency.value.toUpperCase() : "EUR";
      price.textContent = valid ? new Intl.NumberFormat(document.documentElement.lang || "en", {style:"currency",currency:currencyCode}).format(monthly / capacityGB) + "/GB" : "—";
      const changed = [cost.value,currency.value,percent.value,threshold.value].some((value,i) => value !== initial[i]);
      confirmRow.hidden = !changed;
      if (!changed) confirmation.checked = false;
      button.disabled = changed && !confirmation.checked;
    }
    form.addEventListener("input",render);
    form.addEventListener("change",render);
    form.addEventListener("submit",event => {
      if (!confirmRow.hidden && !confirmation.checked) event.preventDefault();
    });
    render();
  });
})();