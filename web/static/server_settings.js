/* Each workspace filters immutable server snapshots; forms retain server authorization. */
(function initializeServerSettingsNavigation() {
  "use strict";
  const serverPanels = Array.from(document.querySelectorAll("[data-expense-server-panel]"));
  if (!serverPanels.length) return;
  const selector = document.querySelector("[data-expense-server-select]");
  const rootTabs = document.querySelector("[data-server-root-tabs]");
  const installationsWorkspace = document.querySelector("[data-server-installations-container]");
  const centralClients = document.querySelector("[data-central-clients-container]");
  const centralBilling = document.querySelector("[data-central-billing-container]");
  const rootWorkspaces = {
    servers: document.querySelector('[data-hosting-installation-panel="servers"]'),
    "temporary-sites": document.querySelector('[data-hosting-installation-panel="temporary-sites"]'),
    installations: installationsWorkspace, clients: centralClients, billing: centralBilling
  };
  const ledgers = [];
  let invoiceRecords = [];
  let invoiceLoadFailed = false;
  try {
    const invoiceSource = JSON.parse(document.querySelector("[data-invoice-model]")?.textContent || "null");
    if (invoiceSource !== null && !Array.isArray(invoiceSource)) throw new Error("Invalid invoice snapshot");
    invoiceRecords = invoiceSource || [];
  } catch (parseError) {
    invoiceLoadFailed = true;
    console.error("Unable to read invoice snapshot", parseError);
  }

  function localPanel() {
    return serverPanels.find(panel => panel.dataset.serverLocal === "true") || serverPanels[0];
  }
  function readNavigation() {
    const fragment = window.location.hash.slice(1);
    const parameters = new URLSearchParams(fragment);
    const pageQuery = new URLSearchParams(window.location.search);
    if (["installations", "temporary-sites", "clients", "billing"].includes(fragment)) parameters.set("workspace", fragment);
    if (fragment.startsWith("server-") && !parameters.has("server-id")) parameters.set("section", fragment === "server-update" ? "updates" : fragment.slice(7));
    // Query parameters select the initial lazy-loaded HTML only. Once a
    // sidebar click writes a route in the hash, stale query values must never
    // pull navigation back to the previous workspace.
    const explicitRoute = parameters.has("workspace") || parameters.has("server-id") || parameters.has("section");
    if (!explicitRoute) {
      if (["installations", "temporary-sites", "clients", "billing"].includes(pageQuery.get("workspace"))) {
        parameters.set("workspace", pageQuery.get("workspace"));
      }
      if (pageQuery.has("installation")) parameters.set("server-id", pageQuery.get("installation"));
      if (pageQuery.has("section")) parameters.set("section", pageQuery.get("section"));
    }
    return {
      workspace: parameters.get("workspace") || "servers",
      serverID: parameters.get("server-id") || "",
      section: parameters.get("section") || "overview",
      status: parameters.get("status") || "all",
      customer: parameters.get("customer") || "",
      query: parameters.get("query") || "",
      installation: parameters.get("installation") || "all",
      page: Math.max(1, Number(parameters.get("page")) || 1)
    };
  }
  function writeNavigation(next, replace) {
    const parameters = new URLSearchParams();
    if (next.workspace === "servers") {
      parameters.set("server-id", next.serverID || localPanel().dataset.serverId);
      parameters.set("section", next.section || "overview");
    } else parameters.set("workspace", next.workspace);
    if (next.status && next.status !== "all") parameters.set("status", next.status);
    if (next.customer) parameters.set("customer", next.customer);
    if (next.query) parameters.set("query", next.query);
    if (next.installation && next.installation !== "all") parameters.set("installation", next.installation);
    if (next.page > 1) parameters.set("page", String(next.page));
    const fragment = "#" + parameters.toString();
    if (fragment !== window.location.hash) history[replace ? "replaceState" : "pushState"](null, "", fragment);
    displayNavigation();
  }
  function openServer(serverID, section) {
    const selectedSection = section || "overview";
    if (!serverPanels.some(panel => panel.dataset.serverId === serverID)) {
      // Installation details are requested only when opened from the catalogue.
      const url = new URL(window.location.href);
      url.searchParams.set("installation", serverID);
      url.searchParams.delete("workspace");
      url.searchParams.set("section", selectedSection);
      url.hash = new URLSearchParams({"server-id": serverID, section: selectedSection}).toString();
      window.location.assign(url.href);
      return;
    }
    writeNavigation({workspace: "servers", serverID, section: selectedSection});
  }
  function showRoot(section, selectedServerSection, targetPanel) {
    const selected = rootWorkspaces[section] ? section : "servers";
    for (const [workspaceName, workspaceElement] of Object.entries(rootWorkspaces)) {
      if (workspaceElement) workspaceElement.hidden = workspaceName !== selected;
    }
    for (const button of rootTabs?.querySelectorAll("[data-server-root-target]") || []) {
      const shortcut = button.dataset.serverConfigShortcut;
      // A section may be intentionally absent until selected. Authorization
      // and local/remote availability were already decided by the server.
      const active = button.dataset.serverRootTarget === selected &&
        (selected !== "servers" || shortcut === selectedServerSection);
      button.classList.toggle("is-active", active);
      if (active) button.setAttribute("aria-current", "page");
      else button.removeAttribute("aria-current");
    }
  }
  // Load the authenticated updater into the page instead of nesting an iframe.
  // Its own command channel and authorization are unchanged.
  function openUpdater(layout) {
    const host = layout.querySelector("[data-server-update-content]");
    if (!host || host.dataset.loading === "true" || host.dataset.loaded === "true") return;
    const loading = layout.querySelector("[data-server-update-loading]");
    const error = layout.querySelector("[data-server-update-error]");
    host.dataset.loading = "true";
    loading.hidden = false;
    error.hidden = true;
    const controller = new AbortController();
    const timeout = window.setTimeout(() => controller.abort(), 20000);
    fetch("/?server_update&embedded", {
      credentials: "same-origin", headers: {"Accept": "text/html"}, signal: controller.signal
    }).then(async response => {
      if (!response.ok) throw new Error("Updater responded with HTTP " + response.status);
      const page = new DOMParser().parseFromString(await response.text(), "text/html");
      const updateRoot = page.querySelector("[data-server-update]");
      if (!updateRoot || !updateRoot.dataset.csrf) throw new Error("Updater response is unavailable");
      host.replaceChildren(document.importNode(updateRoot, true));
      if (!document.querySelector('link[href="/p/static/server_update.css"]')) {
        const stylesheet = document.createElement("link");
        stylesheet.rel = "stylesheet";
        stylesheet.href = "/p/static/server_update.css";
        document.head.appendChild(stylesheet);
      }
      await new Promise((resolve, reject) => {
        const script = document.createElement("script");
        script.src = "/p/static/server_update.js";
        script.onload = resolve;
        script.onerror = () => reject(new Error("Updater script is unavailable"));
        document.head.appendChild(script);
      });
      host.dataset.loaded = "true";
      loading.hidden = true;
    }).catch(loadError => {
      console.error("Unable to open updater", loadError);
      host.replaceChildren();
      error.hidden = false;
      loading.hidden = true;
    }).finally(() => {
      window.clearTimeout(timeout);
      host.dataset.loading = "false";
    });
  }
  function choose(panel, wanted) {
    const layout = panel.querySelector("[data-server-config-layout]");
    if (!layout) return;
    const views = Array.from(layout.querySelectorAll("[data-server-config-view]"));
    const section = views.some(view => view.dataset.serverConfigView === wanted) ? wanted : "overview";
    for (const view of views) view.hidden = view.dataset.serverConfigView !== section;
    for (const button of layout.querySelectorAll("[data-server-config-target]")) {
      const active = button.dataset.serverConfigTarget === section;
      button.setAttribute("aria-current", active ? "page" : "false");
      button.classList.toggle("is-active", active);
    }
    layout.dataset.serverConfigSelected = section;
    if (section === "updates") openUpdater(layout);
  }

  function invoiceBelongsToPanel(invoice, panel) {
    if (!panel) return true;
    if (invoice.InstallationID === panel.dataset.serverId) return true;
    return panel.dataset.serverLocal === "true" && (invoice.InstallationID === "local" || invoice.InstallationID === "local:" + panel.dataset.serverName.toLowerCase());
  }
  function invoiceMatchesStatus(invoice, status) {
    if (status === "all") return true;
    if (status === "overdue") return invoice.Overdue;
    if (status === "paid-month") return invoice.Status === "paid" && invoice.PaidAt.startsWith(new Date().toISOString().slice(0, 7));
    if (status === "open") return invoice.Status === "issued" || invoice.Status === "payment_error";
    return invoice.Status === status;
  }
  function renderInvoice(ledger, invoice) {
    const fragment = ledger.element.querySelector("[data-invoice-row-template]").content.cloneNode(true);
    const writeText = (selectorText, contentText) => { fragment.querySelector(selectorText).textContent = contentText || ""; };
    writeText("[data-invoice-number]", invoice.Number);
    writeText("[data-invoice-customer]", invoice.CustomerEmail + " · " + invoice.Domain);
    writeText("[data-invoice-context]", invoice.ServerName || invoice.InstallationID || ledger.element.dataset.legacyLabel);
    writeText("[data-invoice-period]", invoice.PeriodLabel);
    writeText("[data-invoice-amount]", invoice.AmountLabel);
    writeText("[data-invoice-status]", invoice.StatusLabel);
    fragment.querySelector("[data-invoice-status]").classList.toggle("status-bad", invoice.Overdue);
    const paymentLink = fragment.querySelector("[data-invoice-pay]");
    // JSON bypasses html/template's URL sanitizer; accept payment links only
    // at this external boundary and never interpret an executable URL scheme.
    let paymentURL;
    try { paymentURL = new URL(invoice.PaymentURL, window.location.href); } catch (urlError) { paymentURL = null; }
    if (invoice.CanPay && paymentURL && ["http:", "https:"].includes(paymentURL.protocol)) paymentLink.href = paymentURL.href;
    else paymentLink.remove();
    return fragment;
  }
  function renderLedger(ledger) {
    const requested = ledger.model;
    const matching = invoiceRecords.filter(invoice => {
      if (!invoiceBelongsToPanel(invoice, ledger.panel)) return false;
      if (requested.installation === "legacy" && invoice.InstallationID) return false;
      if (!["all", "legacy"].includes(requested.installation)) {
        const requestedPanel = serverPanels.find(panel => panel.dataset.serverId === requested.installation);
        if (!requestedPanel || !invoiceBelongsToPanel(invoice, requestedPanel)) return false;
      }
      if (!invoiceMatchesStatus(invoice, requested.status)) return false;
      if (requested.customer && invoice.CustomerEmail.toLowerCase() !== requested.customer.toLowerCase()) return false;
      const searchable = [invoice.Number, invoice.CustomerEmail, invoice.Domain, invoice.ServerName, invoice.InstallationID].join(" ").toLocaleLowerCase();
      return !requested.query || searchable.includes(requested.query.toLocaleLowerCase());
    });
    const pageCount = Math.max(1, Math.ceil(matching.length / 25));
    requested.page = Math.min(pageCount, Math.max(1, Math.floor(requested.page)));
    const list = ledger.element.querySelector("[data-billing-invoice-list]");
    list.replaceChildren();
    for (const invoice of matching.slice((requested.page - 1) * 25, requested.page * 25)) list.appendChild(renderInvoice(ledger, invoice));
    ledger.element.querySelector("[data-billing-invoice-search]").value = requested.query;
    ledger.element.querySelector("[data-billing-invoice-status]").value = requested.status;
    ledger.element.querySelector("[data-invoice-installation]").value = requested.installation;
    ledger.element.querySelector("[data-billing-invoice-empty]").hidden = invoiceLoadFailed || matching.length !== 0;
    ledger.element.querySelector("[data-billing-invoice-error]").hidden = !invoiceLoadFailed;
    ledger.element.querySelector("[data-invoice-previous]").disabled = requested.page <= 1;
    ledger.element.querySelector("[data-invoice-next]").disabled = requested.page >= pageCount;
    ledger.element.querySelector("[data-billing-invoice-count]").textContent = requested.page + " / " + pageCount + " · " + matching.length + " / " + invoiceRecords.filter(invoice => invoiceBelongsToPanel(invoice, ledger.panel)).length;
    const customerFilter = ledger.element.querySelector("[data-invoice-customer-filter]");
    customerFilter.hidden = !requested.customer;
    customerFilter.querySelector("span").textContent = requested.customer;
  }
  function updateLedgerNavigation(ledger, update, replace) {
    writeNavigation({...readNavigation(), ...update, page: update.page || 1}, replace);
  }
  for (const element of document.querySelectorAll("[data-invoice-ledger]")) {
    const ledger = {element, panel: element.closest("[data-expense-server-panel]"), model: {query: "", customer: "", status: "all", installation: "all", page: 1}};
    ledgers.push(ledger);
    element.querySelector("[data-invoice-installation]").closest("label").hidden = Boolean(ledger.panel);
    element.addEventListener("input", event => {
      if (event.target.matches("[data-billing-invoice-search]")) updateLedgerNavigation(ledger, {query: event.target.value}, true);
    });
    element.addEventListener("change", event => {
      if (event.target.matches("[data-billing-invoice-status]")) updateLedgerNavigation(ledger, {status: event.target.value});
      if (event.target.matches("[data-invoice-installation]")) updateLedgerNavigation(ledger, {installation: event.target.value});
    });
    element.addEventListener("click", event => {
      if (event.target.closest("[data-invoice-previous]")) updateLedgerNavigation(ledger, {page: ledger.model.page - 1});
      if (event.target.closest("[data-invoice-next]")) updateLedgerNavigation(ledger, {page: ledger.model.page + 1});
      if (event.target.closest("[data-invoice-customer-clear]")) updateLedgerNavigation(ledger, {customer: ""});
    });
  }
  function filterClients(workspace) {
    const search = workspace.querySelector("[data-server-clients-search]");
    if (!search) return;
    const query = search.value.trim().toLocaleLowerCase();
    const status = workspace.querySelector("[data-server-clients-filter]").value;
    const statusFields = {unpaid: "clientUnpaid", overdue: "clientOverdue", "over-limit": "clientOverLimit", attention: "clientAttention"};
    let visibleCount = 0;
    for (const row of workspace.querySelectorAll("[data-server-client-row]")) {
      const matchesStatus = status === "all" || row.dataset[statusFields[status]] === "true";
      row.hidden = !matchesStatus || Boolean(query && !row.dataset.clientSearch.toLocaleLowerCase().includes(query));
      if (!row.hidden) visibleCount++;
    }
    const empty = workspace.querySelector("[data-clients-no-results]");
    if (empty) empty.hidden = visibleCount !== 0;
  }
  for (const workspace of [...serverPanels, centralClients].filter(Boolean)) {
    workspace.addEventListener("input", event => { if (event.target.matches("[data-server-clients-search]")) filterClients(workspace); });
    workspace.addEventListener("change", event => { if (event.target.matches("[data-server-clients-filter]")) filterClients(workspace); });
    filterClients(workspace);
  }
  function displayNavigation() {
    const navigation = readNavigation();
    const target = serverPanels.find(panel => panel.dataset.serverId === navigation.serverID) || localPanel();

    const needClients = navigation.workspace === "clients" && centralClients?.dataset.clientsDeferred === "true";
    const needBilling = navigation.workspace === "billing" && centralBilling?.dataset.billingDeferred === "true";
    const needInstallations = navigation.workspace === "installations" && installationsWorkspace?.dataset.installationsDeferred === "true";
    const requestedSection = navigation.section;
    const serverShortcutAvailable = Array.from(rootTabs?.querySelectorAll("[data-server-config-shortcut]") || [])
      .some(button => button.dataset.serverConfigShortcut === requestedSection);
    const serverSectionLoaded = Array.from(target.querySelectorAll("[data-server-config-view]"))
      .some(view => view.dataset.serverConfigView === requestedSection);
    const needServerSection = navigation.workspace === "servers" && serverShortcutAvailable && !serverSectionLoaded;
    if (needClients || needBilling || needInstallations || needServerSection) {
      const url = new URL(window.location.href);
      if (needServerSection) {
        url.searchParams.set("section", requestedSection);
        url.searchParams.set("installation", target.dataset.serverId);
        url.searchParams.delete("workspace");
      } else {
        url.searchParams.set("workspace", navigation.workspace);
        url.searchParams.delete("section");
        url.searchParams.delete("installation");
      }
      if (url.href !== window.location.href) {
        window.location.assign(url.href);
        return;
      }
      // A section still missing after the correct lazy request is an error,
      // not a reason to silently substitute the overview.
      const destination = rootWorkspaces[navigation.workspace] || rootWorkspaces.servers;
      for (const panel of serverPanels) panel.hidden = panel !== target;
      for (const [name, workspace] of Object.entries(rootWorkspaces)) {
        if (workspace) workspace.hidden = workspace !== destination;
      }
      let notice = destination.querySelector("[data-server-navigation-error]");
      if (!notice) {
        notice = document.createElement("p");
        notice.dataset.serverNavigationError = "true";
        notice.setAttribute("role", "alert");
        notice.className = "alert alert-danger";
        destination.prepend(notice);
      }
      const russian = document.documentElement.lang.toLowerCase().startsWith("ru");
      notice.textContent = russian
        ? "Не удалось загрузить раздел настроек. Обновите страницу или проверьте журнал сервера."
        : "The settings section could not be loaded. Reload the page or check the server log.";
      notice.hidden = false;
      showRoot(navigation.workspace, navigation.section, target);
      return;
    }
    for (const notice of document.querySelectorAll("[data-server-navigation-error]")) notice.hidden = true;

    for (const panel of serverPanels) panel.hidden = panel !== target;
    if (selector) selector.value = target.dataset.expenseServerPanel;
    choose(target, navigation.section);
    const activeSection = target.querySelector("[data-server-config-layout]")?.dataset.serverConfigSelected || "overview";
    showRoot(navigation.workspace, activeSection, target);
    // Rendering invoices is work for the billing workspace only; the server
    // overview must not render an invisible ledger on every navigation.
    const activeLedger = ledgers.find(ledger => navigation.workspace === "billing"
      ? !ledger.panel
      : navigation.workspace === "servers" && activeSection === "billing" && ledger.panel === target);
    if (activeLedger) {
      activeLedger.model = {query: navigation.query, customer: navigation.customer, status: navigation.status, installation: navigation.installation, page: navigation.page};
      renderLedger(activeLedger);
    }
  }
  document.addEventListener("click", event => {
    const openButton = event.target.closest("[data-open-server-id]");
    if (openButton) { openServer(openButton.dataset.openServerId, openButton.dataset.openServerSection); return; }
    const centralInvoiceButton = event.target.closest("[data-central-client-invoices]");
    if (centralInvoiceButton) { writeNavigation({workspace: "billing", customer: centralInvoiceButton.dataset.centralClientInvoices}); return; }
    const rootButton = event.target.closest("[data-server-root-target]");
    if (rootButton) {
      const shortcut = rootButton.dataset.serverConfigShortcut;
      const activePanel = serverPanels.find(panel => !panel.hidden) || localPanel();
      // Sidebar shortcuts describe the local installation; a previously viewed
      // remote installation must never redirect local network/update controls.
      const shortcutPanel = shortcut ? localPanel() : activePanel;
      writeNavigation({workspace: rootButton.dataset.serverRootTarget, serverID: shortcutPanel.dataset.serverId, section: shortcut || "overview"});
      return;
    }
    const panel = event.target.closest("[data-expense-server-panel]");
    if (!panel) return;
    const sectionButton = event.target.closest("[data-server-config-target]");
    if (sectionButton) {
      writeNavigation({workspace: "servers", serverID: panel.dataset.serverId, section: sectionButton.dataset.serverConfigTarget, customer: sectionButton.dataset.billingClientOpen || ""});
    }
    const filterButton = event.target.closest("[data-billing-filter-open]");
    if (filterButton) writeNavigation({workspace: "servers", serverID: panel.dataset.serverId, section: "billing", status: filterButton.dataset.billingFilterOpen});
    if (event.target.closest("[data-billing-create-toggle], [data-billing-create-cancel]")) {
      const composer = panel.querySelector("[data-billing-create-panel]");
      const toggle = panel.querySelector("[data-billing-create-toggle]");
      composer.hidden = event.target.closest("[data-billing-create-cancel]") ? true : !composer.hidden;
      toggle.setAttribute("aria-expanded", String(!composer.hidden));
      if (!composer.hidden) composer.querySelector("[data-billing-site-customer]").focus();
    }
  });
  for (const panel of serverPanels) {
    const layout = panel.querySelector("[data-server-config-layout]");
    panel.addEventListener("change", event => {
      if (!event.target.matches("[data-billing-site-customer]")) return;
      const option = event.target.selectedOptions[0];
      if (!option?.value) return;
      const form = event.target.closest("form");
      form.elements.customer_email.value = option.dataset.customerEmail;
      form.elements.domain.value = option.value;
    });
    panel.addEventListener("submit", event => {
      const form = event.target;
      if (!(form instanceof HTMLFormElement) || form.method.toLowerCase() !== "post") return;
      const parameters = new URLSearchParams({"server-id": panel.dataset.serverId, section: layout.dataset.serverConfigSelected || "overview"});
      form.action = form.action.split("#", 1)[0] + "#" + parameters.toString();
    });
  }
  if (selector) selector.addEventListener("change", () => {
    const panel = serverPanels.find(candidate => candidate.dataset.expenseServerPanel === selector.value);
    if (panel) openServer(panel.dataset.serverId);
  });
  window.addEventListener("hashchange", displayNavigation);
  window.addEventListener("popstate", displayNavigation);
  displayNavigation();

  function filterInstallations() {
    if (!installationsWorkspace) return;
    const query = (installationsWorkspace.querySelector("[data-installations-search]")?.value || "").trim().toLocaleLowerCase();
    const sort = installationsWorkspace.querySelector("[data-installations-sort]")?.value || "sites";
    const filter = installationsWorkspace.querySelector("[data-installations-filter]")?.value || "active";
    const list = installationsWorkspace.querySelector("[data-installations-list]");
    if (!list) return;
    const rows = Array.from(list.querySelectorAll("[data-installation-row]"));
    const text = contentText => (contentText || "").toLocaleLowerCase();
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
        (["connected", "disconnected", "unqualified"].includes(filter) && row.dataset.installationConnection === filter) ||
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

  if (installationsWorkspace) {
    installationsWorkspace.addEventListener("input", event => { if (event.target.matches("[data-installations-search]")) filterInstallations(); });
    installationsWorkspace.addEventListener("change", event => { if (event.target.matches("[data-installations-sort], [data-installations-filter]")) filterInstallations(); });
    filterInstallations();
  }

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
      const changed = [cost.value,currency.value,percent.value,threshold.value,mode()].some((fieldContent, index) => fieldContent !== initial[index]);
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