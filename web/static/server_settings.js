/* Server navigation owns presentation only; pricing and permissions are server-side. */
(function initializeServerSettingsNavigation() {
  "use strict";
  const serverPanels = Array.from(document.querySelectorAll("[data-expense-server-panel]"));
  if (!serverPanels.length) return;
  const selector = document.querySelector("[data-expense-server-select]");
  const validSections = new Set(["overview", "monetization", "network", "updates", "clients", "billing", "registration", "demo"]);

  const rootTabs = document.querySelector("[data-server-root-tabs]");
  const serversWorkspace = document.querySelector('[data-hosting-installation-panel="servers"]');
  const temporaryWorkspace = document.querySelector('[data-hosting-installation-panel="temporary-sites"]');
  const installationsWorkspace = document.querySelector("[data-server-installations-container]");

  function showRoot(section) {
    if (!rootTabs) return;
    const available = Array.from(rootTabs.querySelectorAll("[data-server-root-target]")).map(button => button.dataset.serverRootTarget);
    const selected = available.includes(section) ? section : "servers";
    if (serversWorkspace) serversWorkspace.hidden = selected !== "servers";
    if (temporaryWorkspace) temporaryWorkspace.hidden = selected !== "temporary-sites";
    if (installationsWorkspace) installationsWorkspace.hidden = selected !== "installations";
    rootTabs.querySelectorAll("[data-server-root-target]").forEach(button => {
      const active = button.dataset.serverRootTarget === selected;
      button.classList.toggle("is-active", active);
      button.setAttribute("aria-current", active ? "page" : "false");
    });
  }

  function filterInstallations() {
    if (!installationsWorkspace) return;
    const query = (installationsWorkspace.querySelector("[data-installations-search]")?.value || "").trim().toLocaleLowerCase();
    const sort = installationsWorkspace.querySelector("[data-installations-sort]")?.value || "sites";
    const filter = installationsWorkspace.querySelector("[data-installations-filter]")?.value || "active";
    const list = installationsWorkspace.querySelector("[data-installations-list]");
    if (!list) return;
    const rows = Array.from(list.querySelectorAll("[data-installation-row]"));
    const text = value => (value || "").toLocaleLowerCase();
    const number = (row, key) => Number(row.dataset[key]) || 0;
    rows.sort((left, right) => {
      if (sort === "name") return text(left.dataset.installationName).localeCompare(text(right.dataset.installationName));
      const field = {sites:"installationSites",forecast:"installationForecast",paid:"installationPaid"}[sort] || "installationSites";
      // Money is ordered within each currency, never compared as if EUR=USD.
      if (sort === "forecast" || sort === "paid") {
        const currencyOrder = text(left.dataset.installationCurrency).localeCompare(text(right.dataset.installationCurrency));
        if (currencyOrder) return currencyOrder;
      }
      return number(right, field) - number(left, field) || text(left.dataset.installationName).localeCompare(text(right.dataset.installationName));
    });
    let visible = 0;
    for (const row of rows) {
      const archived = row.dataset.installationArchived === "true";
      const enabled = row.dataset.installationMonetized === "true";
      const statusMatch = filter === "all" ||
        (filter === "active" && !archived) ||
        (filter === "archived" && archived) ||
        (filter === "monetized" && !archived && enabled) ||
        (filter === "not-monetized" && !archived && !enabled);
      const queryMatch = !query || text(row.dataset.installationSearch).includes(query);
      row.hidden = !(statusMatch && queryMatch);
      if (!row.hidden) visible++;
      list.appendChild(row);
    }
    const count = installationsWorkspace.querySelector("[data-installations-count]");
    if (count) count.textContent = visible + " / " + rows.length;
    const empty = installationsWorkspace.querySelector("[data-installations-no-results]");
    if (empty) empty.hidden = visible !== 0 || rows.length === 0;
  }

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
  function filterBillingInvoices(layout) {
    const search = layout.querySelector("[data-billing-invoice-search]");
    const status = layout.querySelector("[data-billing-invoice-status]");
    if (!search || !status) return;
    const query = search.value.trim().toLocaleLowerCase();
    const requestedStatus = status.value;
    const rows = Array.from(layout.querySelectorAll("[data-billing-invoice-row]"));
    let visible = 0;
    for (const row of rows) {
      const matchesQuery = !query || row.textContent.toLocaleLowerCase().includes(query);
      const matchesStatus = requestedStatus === "all" || row.dataset.billingStatus === requestedStatus;
      row.hidden = !(matchesQuery && matchesStatus);
      if (!row.hidden) visible++;
    }
    const count = layout.querySelector("[data-billing-invoice-count]");
    if (count) count.textContent = visible + " / " + rows.length;
  }
  function filterBillingClients(layout) {
    const search = layout.querySelector("[data-server-clients-search]");
    if (!search) return;
    const query = search.value.trim().toLocaleLowerCase();
    for (const row of layout.querySelectorAll("[data-server-client-row]")) {
      row.hidden = !!query && !row.textContent.toLocaleLowerCase().includes(query);
    }
  }
  function showInvoiceComposer(layout, visible) {
    const composer = layout.querySelector("[data-billing-create-panel]");
    const button = layout.querySelector("[data-billing-create-toggle]");
    if (!composer || !button) return;
    composer.hidden = !visible;
    button.setAttribute("aria-expanded", String(visible));
    if (visible) composer.querySelector('[name="customer_email"]').focus();
  }
  function displayHash() {
    const root = window.location.hash.slice(1);
    if (root === "installations" || root === "temporary-sites") {
      showRoot(root);
      return;
    }
    showRoot("servers");
    const state = fromHash();
    let target = state.id ? serverPanels.find(panel => panel.dataset.serverId === state.id) : null;
    if (!target && selector) target = serverPanels.find(panel => panel.dataset.expenseServerPanel === selector.value);
    if (!target) target = serverPanels.find(panel => !panel.hidden) || serverPanels[0];
    if (!target) return;
    for (const panel of serverPanels) panel.hidden = panel !== target;
    if (selector) selector.value = target.dataset.expenseServerPanel;
    choose(target, state.section);
  }

  if (rootTabs) {
    rootTabs.addEventListener("click", event => {
      const button = event.target.closest("[data-server-root-target]");
      if (!button || !rootTabs.contains(button)) return;
      const section = button.dataset.serverRootTarget;
      if (section === "servers") {
        const selected = selector ? serverPanels.find(panel => panel.dataset.expenseServerPanel === selector.value) : serverPanels.find(panel => !panel.hidden);
        writeHash(selected || serverPanels[0], "overview");
      } else {
        history.replaceState(null, "", "#" + section);
      }
      displayHash();
    });
  }
  if (installationsWorkspace) {
    installationsWorkspace.addEventListener("input", event => {
      if (event.target.matches("[data-installations-search]")) filterInstallations();
    });
    installationsWorkspace.addEventListener("change", event => {
      if (event.target.matches("[data-installations-sort], [data-installations-filter]")) filterInstallations();
    });
    installationsWorkspace.addEventListener("click", event => {
      const open = event.target.closest("[data-open-server-id]");
      if (!open) return;
      const panel = serverPanels.find(candidate => candidate.dataset.serverId === open.dataset.openServerId);
      if (!panel) return;
      if (selector) selector.value = panel.dataset.expenseServerPanel;
      writeHash(panel, "overview");
      displayHash();
      rootTabs?.scrollIntoView({behavior:"smooth",block:"start"});
    });
    filterInstallations();
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
      if (button.dataset.billingClientOpen) {
        const search = layout.querySelector("[data-billing-invoice-search]");
        const status = layout.querySelector("[data-billing-invoice-status]");
        if (search) search.value = button.dataset.billingClientOpen;
        if (status) status.value = "all";
        filterBillingInvoices(layout);
      }
      writeHash(panel, layout.dataset.serverConfigSelected);
    });
    layout.addEventListener("click", event => {
      if (event.target.closest("[data-billing-create-toggle]")) {
        const composer = layout.querySelector("[data-billing-create-panel]");
        showInvoiceComposer(layout, !!composer && composer.hidden);
      } else if (event.target.closest("[data-billing-create-cancel]")) {
        showInvoiceComposer(layout, false);
      }
    });
    layout.addEventListener("input", event => {
      if (event.target.matches("[data-billing-invoice-search]")) filterBillingInvoices(layout);
      if (event.target.matches("[data-server-clients-search]")) filterBillingClients(layout);
    });
    layout.addEventListener("change", event => {
      if (event.target.matches("[data-billing-invoice-status]")) filterBillingInvoices(layout);
    });
    filterBillingInvoices(layout);
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
    showRoot("servers");
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
    const modes = Array.from(form.querySelectorAll('[name="monetization_mode"]'));
    const mode = () => modes.find(input => input.checked)?.value || "disabled";
    const initial = [cost.value,currency.value,percent.value,threshold.value,mode()];
    function render() {
      const enabled = mode() === "enabled";
      form.dataset.monetizationEnabled = String(enabled);
      // Hidden controls must not block opt-out with native constraint validation.
      // The server restores the persisted tariff when the mode alone is saved.
      for (const field of form.querySelectorAll(".server-tariff-fields input, .server-tariff-fields select, .server-tariff-fields textarea")) {
        field.disabled = !enabled;
      }
      cost.min = enabled ? "0.01" : "0";
      const diskGB = Number(form.dataset.diskBytes) / 1000000000;
      const portion = Number(percent.value);
      const monthly = Number(cost.value);
      const capacityGB = diskGB * portion / 100;
      const valid = Number.isFinite(capacityGB) && capacityGB > 0 && portion>=1 && portion<=100 && Number.isFinite(monthly) && monthly>0;
      capacity.textContent = enabled && valid ? new Intl.NumberFormat(document.documentElement.lang || "en", {maximumFractionDigits:1}).format(capacityGB) + " GB" : "—";
      const currencyCode = /^[A-Za-z]{3}$/.test(currency.value) ? currency.value.toUpperCase() : "EUR";
      price.textContent = enabled && valid ? new Intl.NumberFormat(document.documentElement.lang || "en", {style:"currency",currency:currencyCode}).format(monthly / capacityGB) + "/GB" : "—";
      const changed = [cost.value,currency.value,percent.value,threshold.value,mode()].some((value,i) => value !== initial[i]);
      confirmRow.hidden = !changed;
      if (!changed) confirmation.checked = false;
      button.disabled = (changed && !confirmation.checked) || (enabled && !valid);
    }
    form.addEventListener("input",render);
    form.addEventListener("change",render);
    form.addEventListener("submit",event => {
      if (!confirmRow.hidden && !confirmation.checked) event.preventDefault();
    });
    render();
  });
})();