/* One delegated owner for client navigation, independent of other workspaces. */
(function initializeServerClients() {
  "use strict";
  const serverPanels = Array.from(document.querySelectorAll("[data-expense-server-panel]"));
  const centralClients = document.querySelector("[data-central-clients-container]");
  if (!serverPanels.length && !centralClients) return;
  function localPanel() {
    return serverPanels.find(panel => panel.dataset.serverLocal === "true") || serverPanels[0];
  }
  function readNavigation() {
    const fragment = new URLSearchParams(window.location.hash.slice(1));
    const query = new URLSearchParams(window.location.search);
    return { serverID: fragment.get("server-id") || query.get("installation") || "" };
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
          .find(button => button.dataset.certificateRenew === certificate.dataset.clientCertificateDomain);
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
      if (more) {
        more.hidden = showAll;
        more.textContent = document.documentElement.lang.toLowerCase().startsWith("ru")
          ? "Показать все " + matching + " сайтов" : "Show all " + matching + " sites";
      }
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
      const reason = dialog.querySelector("[data-client-delete-reason]");
      if (reason) reason.textContent = pending
        ? (deletion.dataset.deletionReason === "nonpayment" ? reason.dataset.reasonNonpayment : reason.dataset.reasonRequest)
        : "";
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
})();
