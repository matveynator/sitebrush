/* Each workspace filters immutable server snapshots; forms retain server authorization. */
(function initializeServerSettingsNavigation() {
  "use strict";
  const serverPanels = Array.from(document.querySelectorAll("[data-expense-server-panel]"));
  if (!serverPanels.length) return;
  const selector = document.querySelector("[data-expense-server-select]");
  const rootTabs = document.querySelector("[data-server-root-tabs]");
  const contextLabel = document.querySelector("[data-server-context]");
  const themeChoice = document.querySelector("[data-server-theme]");
  if (themeChoice) {
    const key = 'sitebrush.server-settings.theme';
    let selected = 'system';
    try { selected = localStorage.getItem(key) || 'system'; } catch (storageError) { /* Private browsing. */ }
    if (!['light', 'dark', 'system'].includes(selected)) selected = 'system';
    themeChoice.value = selected;
    const media = window.matchMedia ? window.matchMedia('(prefers-color-scheme: dark)') : null;
    const applyTheme = () => document.documentElement.setAttribute('data-bs-theme',
      selected === 'system' ? (media && media.matches ? 'dark' : 'light') : selected);
    themeChoice.addEventListener('change', () => {
      selected = themeChoice.value;
      if (!['light', 'dark', 'system'].includes(selected)) selected = 'system';
      try { localStorage.setItem(key, selected); } catch (storageError) { /* Nonpersistent preference. */ }
      applyTheme();
    });
    if (media?.addEventListener) media.addEventListener('change', () => { if (selected === 'system') applyTheme(); });
    applyTheme();
  }
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
    if (contextLabel) {
      if (["installations", "clients", "billing"].includes(selected)) {
        const tab = Array.from(rootTabs?.querySelectorAll("[data-server-root-target]") || [])
          .find(button => button.dataset.serverRootTarget === selected);
        contextLabel.textContent = contextLabel.dataset.serverContextCentral + ' · ' + (tab?.textContent?.trim() || selected);
      } else {
        const serverName = targetPanel?.dataset.serverName || '';
        const title = contextLabel.dataset.serverContextLocal || '';
        contextLabel.textContent = serverName ? serverName + ' · ' + title : title;
      }
    }
    document.body.dataset.serverSettingsScope = ["installations", "clients", "billing"].includes(selected) ? "central" : "node";
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
    // Never silently replace a requested (possibly deferred) view with Overview.
    // A missing/forbidden view is diagnosed by displayNavigation instead.
    const section = wanted || "overview";
    const loaded = views.some(view => view.dataset.serverConfigView === section);
    for (const view of views) view.hidden = !loaded || view.dataset.serverConfigView !== section;
    for (const button of layout.querySelectorAll("[data-server-config-target]")) {
      const active = button.dataset.serverConfigTarget === section;
      button.setAttribute("aria-current", active ? "page" : "false");
      button.classList.toggle("is-active", active);
    }
    layout.dataset.serverConfigSelected = section;
    if (loaded && section === "updates") openUpdater(layout);
    return loaded;
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
  // All client interactions are delegated to the workspace. There are no
  // event listeners or modal instances per website, even for large customers.
  function clientRows(workspace) {
    return Array.from(workspace.querySelectorAll("[data-server-client-row]"));
  }
  function numberFromLabel(label) {
    // Monetary labels may have different currencies. Compare totals only
    // within currency groups; never sum EUR and USD as one amount.
    const value = String(label || "").replace(/[^0-9,.\-]/g, "");
    const lastComma = value.lastIndexOf(",");
    const lastDot = value.lastIndexOf(".");
    const decimal = Math.max(lastComma, lastDot);
    const normalized = decimal >= 0 && value.length - decimal <= 3
      ? value.slice(0, decimal).replace(/[^0-9\-]/g, "") + "." + value.slice(decimal + 1).replace(/[^0-9]/g, "")
      : value.replace(/[^0-9\-]/g, "");
    return Number(normalized) || 0;
  }
  function bytesFromLabel(label) {
    const match = String(label || "").match(/(\d+(?:[.,]\d+)?)\s*(TiB|GiB|MiB|KiB|TB|GB|MB|KB|B)\b/i);
    if (!match) return 0;
    const power = {b:0,kb:1,mb:2,gb:3,tb:4,kib:1,mib:2,gib:3,tib:4};
    const unit = match[2].toLowerCase();
    const factor = unit.endsWith("ib") ? 1024 : 1000;
    return Number(match[1].replace(",", ".")) * Math.pow(factor, power[unit]);
  }
  function formatStorage(bytes) {
    if (!Number.isFinite(bytes) || bytes < 0) return "—";
    let value = bytes;
    let unit = "B";
    for (const next of ["KB", "MB", "GB", "TB"]) {
      if (value < 1000) break;
      value /= 1000;
      unit = next;
    }
    return new Intl.NumberFormat(document.documentElement.lang || "en", {maximumFractionDigits: 1}).format(value) + " " + unit;
  }
  function activateClientTab(row, selected) {
    for (const pane of row.querySelectorAll("[data-client-pane]")) {
      pane.hidden = pane.dataset.clientPane !== selected;
    }
    for (const tab of row.querySelectorAll("[data-client-tab]")) {
      const active = tab.dataset.clientTab === selected;
      tab.classList.toggle("is-active", active);
      tab.setAttribute("aria-pressed", String(active));
    }
  }
  function certificateNeedsRenewal(certificate) {
    if (certificate.dataset.clientCertificateValid !== "true") return false;
    const expireText = certificate.dataset.clientCertificateExpires || "";
    let expiry = Date.parse(expireText);
    // Localized DD.MM.YYYY dates must not depend on browser Date.parse.
    const european = expireText.match(/\b(\d{1,2})\.(\d{1,2})\.(\d{4})\b/);
    if (european) expiry = Date.UTC(Number(european[3]), Number(european[2])-1, Number(european[1]));
    if (Number.isFinite(expiry)) return expiry - Date.now() <= 14 * 86400000;
    const remaining = certificate.dataset.clientCertificateRemaining || "";
    const days = remaining.match(/(\d+)\s*(?:дн|day|d\b)/i);
    return Boolean(days && Number(days[1]) <= 14);
  }
  function initializeClientRow(row) {
    const sites = Array.from(row.querySelectorAll("[data-client-site-row]"));
    let totalUsed = 0, totalQuota = 0, knownQuotas = 0, problemSites = 0;
    for (const site of sites) {
      const used = Number(site.dataset.clientSiteUsed) || 0;
      const quota = bytesFromLabel(site.dataset.clientSiteLimit);
      totalUsed += used;
      if (quota > 0) { totalQuota += quota; knownQuotas++; }
      const progress = site.querySelector("[data-client-site-progress]");
      if (progress && quota > 0) {
        const percent = Math.max(0, Math.min(100, Math.round(used / quota * 100)));
        progress.setAttribute("aria-valuenow", String(percent));
        progress.querySelector("span").style.width = percent + "%";
        if (used > quota) {
          progress.classList.add("is-over");
          site.dataset.clientSiteProblem = "true";
        }
      }
      const invalid = site.querySelector("[data-client-ssl-invalid]");
      if (invalid) site.dataset.clientSiteProblem = "true";
      if (site.dataset.clientSiteProblem === "true") problemSites++;
      // Valid certificates remain in the rare-actions menu unless due in
      // fourteen days. Move the existing button, never clone its listener.
      for (const certificate of site.querySelectorAll("[data-client-certificate]")) {
        if (!certificateNeedsRenewal(certificate)) continue;
        const renewal = Array.from(site.querySelectorAll("[data-client-valid-ssl-action]"))
          .find(button => button.dataset.certificateRenew === certificate.querySelector("small")?.textContent?.split(" · ")[0]);
        const inline = certificate.querySelector("[data-client-ssl-inline]");
        if (renewal && inline) {
          renewal.classList.remove("dropdown-item");
          renewal.classList.add("btn", "btn-outline-primary", "btn-sm");
          inline.appendChild(renewal);
        }
      }
    }
    row.dataset.clientUsedBytes = String(totalUsed);
    if (knownQuotas === sites.length && sites.length) {
      const summary = row.querySelector("[data-client-quota-summary]");
      if (summary) summary.textContent = formatStorage(totalUsed) + " / " + formatStorage(totalQuota);
    }
    const sslCount = sites.filter(site => Boolean(site.querySelector("[data-client-ssl-invalid]"))).length;
    const sslBadge = row.querySelector("[data-client-ssl-problems]");
    if (sslBadge) {
      sslBadge.hidden = sslCount === 0;
      sslBadge.textContent = document.documentElement.lang.toLowerCase().startsWith("ru") ? "Ошибка SSL: " + sslCount : "SSL errors: " + sslCount;
    }
    const errorCount = row.querySelector("[data-client-error-count]");
    if (errorCount) errorCount.textContent = String(problemSites);
    row.dataset.clientProblemCount = String(problemSites);
    row.dataset.clientCurrency = (row.dataset.clientDebtLabel || "").replace(/[\d\s.,\-]/g, "").trim();
  }
  function updateClientSites(row, globalSearch) {
    const input = row.querySelector("[data-client-domain-search]");
    const siteSearch = (input?.value || "").trim().toLocaleLowerCase();
    const customerEmail = (row.dataset.customerEmail || "").toLocaleLowerCase();
    const search = siteSearch || (globalSearch && !customerEmail.includes(globalSearch) ? globalSearch : "");
    const errorsOnly = row.querySelector("[data-client-errors-only]")?.checked || false;
    const showAll = row.dataset.clientShowAll === "true";
    const sites = Array.from(row.querySelectorAll("[data-client-site-row]"));
    let matching = 0, visible = 0;
    for (const site of sites) {
      const matches = (!search || site.dataset.clientSiteDomain.toLocaleLowerCase().includes(search)) &&
        (!errorsOnly || site.dataset.clientSiteProblem === "true");
      if (matches) matching++;
      const show = matches && (showAll || visible < 10);
      site.hidden = !show;
      if (show) visible++;
    }
    const footer = row.querySelector("[data-client-site-footer]");
    if (footer) {
      footer.hidden = matching <= 10;
      const counter = footer.querySelector("[data-client-site-count]");
      if (counter) counter.textContent = visible + " / " + matching;
      const more = footer.querySelector("[data-client-show-all]");
      if (more) more.hidden = showAll;
    }
    const empty = row.querySelector("[data-client-no-sites]");
    if (empty) empty.hidden = matching !== 0;
  }
  function filterClients(workspace) {
    const searchElement = workspace.querySelector("[data-server-clients-search]");
    if (!searchElement) return;
    const query = searchElement.value.trim().toLocaleLowerCase();
    const selected = workspace.querySelector("[data-server-client-filter].is-active")?.dataset.serverClientFilter || "all";
    const statusFields = {overdue:"clientOverdue", "over-limit":"clientOverLimit", attention:"clientAttention"};
    const rows = clientRows(workspace);
    const counts = {all: rows.length, attention:0, overdue:0, "over-limit":0};
    for (const row of rows) {
      for (const [name, field] of Object.entries(statusFields)) {
        if (row.dataset[field] === "true" || (name === "attention" && Number(row.dataset.clientProblemCount) > 0)) counts[name]++;
      }
    }
    for (const badge of workspace.querySelectorAll("[data-client-filter-count]")) {
      badge.textContent = String(counts[badge.dataset.clientFilterCount] || 0);
    }
    const sorter = workspace.querySelector("[data-server-clients-sort]")?.value || "debt";
    rows.sort((left, right) => {
      if (sorter === "storage") return Number(right.dataset.clientUsedBytes || 0) - Number(left.dataset.clientUsedBytes || 0) ||
        left.dataset.clientSearch.localeCompare(right.dataset.clientSearch);
      if (sorter === "sites") return Number(right.dataset.clientSiteCount || 0) - Number(left.dataset.clientSiteCount || 0) ||
        left.dataset.clientSearch.localeCompare(right.dataset.clientSearch);
      if (sorter === "email") return (left.dataset.customerEmail || left.dataset.clientSearch).localeCompare(right.dataset.customerEmail || right.dataset.clientSearch);
      const leftCurrency = left.dataset.clientCurrency || "", rightCurrency = right.dataset.clientCurrency || "";
      return leftCurrency.localeCompare(rightCurrency) ||
        numberFromLabel(right.dataset.clientDebtLabel) - numberFromLabel(left.dataset.clientDebtLabel) ||
        (left.dataset.customerEmail || left.dataset.clientSearch).localeCompare(right.dataset.customerEmail || right.dataset.clientSearch);
    });
    const list = workspace.querySelector("[data-server-clients-list], .expense-simple-clients");
    if (list) for (const row of rows) list.appendChild(row);
    let visibleCount = 0;
    for (const row of rows) {
      const matchesStatus = selected === "all" || row.dataset[statusFields[selected]] === "true" ||
        (selected === "attention" && Number(row.dataset.clientProblemCount) > 0);
      const matchingSite = Array.from(row.querySelectorAll("[data-client-site-row]"))
        .some(site => site.dataset.clientSiteDomain.toLocaleLowerCase().includes(query));
      const matchesSearch = !query || (row.dataset.customerEmail || "").toLocaleLowerCase().includes(query) ||
        matchingSite || (row.dataset.clientSearch || "").toLocaleLowerCase().includes(query);
      row.hidden = !matchesStatus || !matchesSearch;
      if (!row.hidden) {
        visibleCount++;
        updateClientSites(row, query);
        if (query && matchingSite && !(row.dataset.customerEmail || "").toLocaleLowerCase().includes(query)) {
          row.open = true;
          activateClientTab(row, "sites");
        }
      }
    }
    const empty = workspace.querySelector("[data-clients-no-results]");
    if (empty) empty.hidden = visibleCount > 0;
  }
  function updateClientAccountForm(form) {
    const selection = form.querySelector("[data-client-account-select]")?.selectedOptions[0];
    const domain = selection?.dataset.domain || "";
    const email = selection?.dataset.email || "";
    const protectedAccount = selection?.dataset.protected === "true";
    const pending = selection?.dataset.pending === "true";
    form.querySelector("[data-client-account-domain]").value = domain;
    form.querySelector("[data-client-account-confirm-domain]").value = domain;
    form.querySelector("[data-client-account-email]").value = email;
    const notice = form.querySelector("[data-client-account-protected]");
    const actions = form.querySelector("[data-client-account-actions]");
    notice.hidden = !domain || !protectedAccount;
    actions.hidden = !domain || protectedAccount;
    const choice = form.querySelector("[data-client-account-action]");
    for (const option of choice.options) {
      option.disabled = option.value === "restore_account" ? !pending : option.value === "queue_account" ? pending : false;
      option.hidden = option.disabled;
    }
    choice.value = pending ? "restore_account" : "queue_account";
    const confirmation = form.querySelector("[data-client-account-confirm]");
    confirmation.disabled = !domain || protectedAccount;
    confirmation.value = "";
  }
  for (const workspace of [...serverPanels, centralClients].filter(Boolean)) {
    if (!workspace.querySelector("[data-server-clients-search]")) continue;
    for (const row of clientRows(workspace)) initializeClientRow(row);
    workspace.addEventListener("input", event => {
      if (event.target.matches("[data-server-clients-search]")) filterClients(workspace);
      if (event.target.matches("[data-client-domain-search]")) {
        const row = event.target.closest("[data-server-client-row]");
        row.dataset.clientShowAll = "false";
        updateClientSites(row, workspace.querySelector("[data-server-clients-search]")?.value.trim().toLocaleLowerCase() || "");
      }
    });
    workspace.addEventListener("change", event => {
      if (event.target.matches("[data-server-clients-sort], [data-server-clients-filter]")) filterClients(workspace);
      if (event.target.matches("[data-client-errors-only]")) updateClientSites(event.target.closest("[data-server-client-row]"), "");
      if (event.target.matches("[data-client-account-select]")) updateClientAccountForm(event.target.closest("[data-client-account-form]"));
    });
    workspace.addEventListener("click", event => {
      const chip = event.target.closest("[data-server-client-filter]");
      if (chip) {
        for (const button of workspace.querySelectorAll("[data-server-client-filter]")) {
          const active = button === chip;
          button.classList.toggle("is-active", active);
          button.setAttribute("aria-pressed", String(active));
        }
        filterClients(workspace);
      }
      const tab = event.target.closest("[data-client-tab]");
      if (tab) activateClientTab(tab.closest("[data-server-client-row]"), tab.dataset.clientTab);
      const more = event.target.closest("[data-client-show-all]");
      if (more) {
        const row = more.closest("[data-server-client-row]");
        row.dataset.clientShowAll = "true";
        updateClientSites(row, "");
      }
    });
    for (const form of workspace.querySelectorAll("[data-client-account-form]")) {
      updateClientAccountForm(form);
      form.addEventListener("submit", event => {
        if (!form.querySelector("[data-client-account-domain]").value ||
            form.querySelector("[data-client-account-confirm]").disabled) event.preventDefault();
      });
    }
    filterClients(workspace);
  }
  // The shared dialogs live after <main>, unlike the former per-site dialogs.
  // Keep the selected server workspace in the redirect fragment after POST.
  for (const dialog of ["clientQuotaModal", "clientDeleteModal"]) {
    document.getElementById(dialog)?.addEventListener("submit", event => {
      const form = event.target;
      if (!(form instanceof HTMLFormElement)) return;
      const navigation = readNavigation();
      const parameters = new URLSearchParams({
        "server-id": navigation.serverID || localPanel().dataset.serverId,
        section: "clients"
      });
      form.action = form.action.split("#", 1)[0] + "#" + parameters.toString();
    });
  }
  // Shared Bootstrap dialogs retain the original POST names, domain and CSRF.
  document.addEventListener("click", event => {
    const quota = event.target.closest("[data-client-quota-edit]");
    if (quota) {
      const dialog = document.querySelector("#clientQuotaModal");
      dialog.querySelector("[data-client-quota-domain]").value = quota.dataset.domain;
      dialog.querySelector("[data-client-quota-title]").textContent = quota.dataset.domain;
      dialog.querySelector("[data-client-quota-value]").value = quota.dataset.quota;
    }
    const deletion = event.target.closest("[data-client-site-delete]");
    if (deletion) {
      const dialog = document.querySelector("#clientDeleteModal");
      dialog.querySelector("[data-client-delete-domain]").value = deletion.dataset.domain;
      dialog.querySelector("[data-client-delete-title]").textContent = deletion.dataset.domain;
      dialog.querySelector("[data-client-delete-size]").textContent = deletion.dataset.deletionSize;
      const pending = Boolean(deletion.dataset.deletionReason);
      dialog.querySelector("[data-client-delete-pending]").hidden = !pending;
      dialog.querySelector("[data-client-delete-deadline]").textContent = deletion.dataset.deletionDeadline || "";
      const action = dialog.querySelector("[data-client-delete-action]");
      for (const option of action.options) {
        option.disabled = option.value === "restore_site" ? !pending :
          ["queue_site", "suspend_site_nonpayment"].includes(option.value) ? pending : false;
        option.hidden = option.disabled;
      }
      action.value = pending ? "restore_site" : "queue_site";
      const confirm = dialog.querySelector("[data-client-delete-confirm]");
      confirm.value = "";
      confirm.placeholder = deletion.dataset.domain;
    }
  });
  function displayNavigation() {
    const navigation = readNavigation();
    const requestedPanel = serverPanels.find(panel => panel.dataset.serverId === navigation.serverID);
    // A direct link to an unloaded installation must hydrate that installation.
    // Falling back to the local panel here would silently show the wrong server.
    const missingRequestedServer = navigation.workspace === "servers" && Boolean(navigation.serverID) && !requestedPanel;
    const target = requestedPanel || localPanel();

    const needClients = navigation.workspace === "clients" && centralClients?.dataset.clientsDeferred === "true";
    const needBilling = navigation.workspace === "billing" && centralBilling?.dataset.billingDeferred === "true";
    const needInstallations = navigation.workspace === "installations" && installationsWorkspace?.dataset.installationsDeferred === "true";
    const requestedSection = navigation.section;
    // Lazy views must be resolvable by direct URL as well as by sidebar click.
    // Unknown sections fail visibly without a reload loop.
    const validServerSections = ["overview", "network", "updates", "registration", "demo", "clients", "billing", "monetization"];
    const knownServerSection = validServerSections.includes(requestedSection);
    const serverSectionLoaded = Array.from(target.querySelectorAll("[data-server-config-view]"))
      .some(view => view.dataset.serverConfigView === requestedSection);
    const needServerSection = navigation.workspace === "servers" && knownServerSection &&
      !missingRequestedServer && !serverSectionLoaded;
    const invalidServerSection = navigation.workspace === "servers" &&
      !knownServerSection && !missingRequestedServer;
    if (needClients || needBilling || needInstallations || needServerSection || missingRequestedServer || invalidServerSection) {
      const url = new URL(window.location.href);
      if (needServerSection || missingRequestedServer) {
        url.searchParams.set("section", requestedSection);
        url.searchParams.set("installation", missingRequestedServer ? navigation.serverID : target.dataset.serverId);
        url.searchParams.delete("workspace");
      } else {
        url.searchParams.set("workspace", navigation.workspace);
        url.searchParams.delete("section");
        url.searchParams.delete("installation");
      }
      if (!invalidServerSection && url.href !== window.location.href) {
        window.location.assign(url.href);
        return;
      }
      // A section still missing after the correct lazy request is an error,
      // not a reason to silently substitute the overview.
      const destination = rootWorkspaces[navigation.workspace] || rootWorkspaces.servers;
      for (const panel of serverPanels) panel.hidden = panel !== target;
      for (const workspace of Object.values(rootWorkspaces)) {
        if (workspace) workspace.hidden = workspace !== destination;
      }
      // Keep an unavailable view blank rather than exposing a stale Overview.
      if (navigation.workspace === "servers") choose(target, requestedSection);
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

  // The central catalog is filtered, sorted and paged on the server. Never
  // filter only the 40 visible cards: doing so would hide matching archives.
  if (installationsWorkspace) {
    const form = installationsWorkspace.querySelector('[data-installations-query-form]');
    if (form) {
      form.addEventListener('change', event => {
        if (event.target.matches('[data-installations-filter], [data-installations-sort]')) form.requestSubmit();
      });
    }
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