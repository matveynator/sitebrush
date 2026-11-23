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
  const clientModels = new WeakMap();
  const russian = document.documentElement.lang.toLowerCase().startsWith("ru");
  const siteCountRules = new Intl.PluralRules(document.documentElement.lang || "en");

  function siteCountText(count) {
    const plural = siteCountRules.select(count);
    const siteWord = russian ? ({one: "сайт", few: "сайта"}[plural] || "сайтов") :
      (plural === "one" ? "site" : "sites");
    return count + " " + siteWord;
  }

  function initializeClient(row) {
    const sites = Array.from(row.querySelectorAll("[data-client-site-row]"), element => ({
      element,
      domain: element.dataset.clientSiteDomain.toLocaleLowerCase(),
      snapshotSSLError: element.dataset.clientSiteSslError === "true",
      invalidCertificates: new Set(Array.from(element.querySelectorAll("[data-client-ssl-invalid]"),
        label => label.closest("[data-client-certificate]").dataset.clientCertificate))
    }));
    const client = {
      email: row.dataset.customerEmail.toLocaleLowerCase(),
      overdue: row.dataset.clientOverdue === "true",
      overLimit: row.dataset.clientOverLimit === "true",
      sites,
      query: "",
      showAll: false,
      scheduleOpen: false,
      moreButton: row.querySelector("[data-client-show-all]"),
      emptyMessage: row.querySelector("[data-client-no-sites]")
    };
    const siteCount = row.querySelector("[data-client-site-total]");
    if (siteCount && russian) siteCount.textContent = siteCountText(sites.length);
    clientModels.set(row, client);
  }

  function clientHasProblems(client) {
    return client.overdue || client.overLimit || client.sites.some(site =>
      site.snapshotSSLError || site.invalidCertificates.size > 0);
  }

  function renderClientSites(client, clientSearch) {
    const domainSearch = client.email.includes(clientSearch) ? "" : clientSearch;
    let matching = 0;
    for (const site of client.sites) {
      const matches = site.domain.includes(domainSearch) && site.domain.includes(client.query);
      if (matches) matching++;
      site.element.hidden = !matches || Boolean(client.moreButton && !client.showAll && matching > 10);
    }
    if (client.moreButton) {
      client.moreButton.hidden = matching <= 10;
      client.moreButton.setAttribute("aria-expanded", String(client.showAll));
      client.moreButton.textContent = client.showAll
        ? (russian ? "Свернуть ▴" : "Show fewer ▴")
        : (russian ? "Показать все " : "Show all ") + siteCountText(matching) + " ▾";
    }
    if (client.emptyMessage) client.emptyMessage.hidden = matching !== 0;
  }

  function renderClientList(workspace, model) {
    let problemCount = 0;
    let visibleCount = 0;
    for (const row of model.rows) {
      const client = clientModels.get(row);
      const hasProblems = clientHasProblems(client);
      if (hasProblems) problemCount++;
      const emailMatches = client.email.includes(model.query);
      const domainMatches = client.sites.some(site => site.domain.includes(model.query));
      const visible = (emailMatches || domainMatches) && (!model.problemsOnly || hasProblems);
      row.hidden = !visible;
      if (visible) {
        visibleCount++;
        if (domainMatches && !emailMatches) row.open = true;
        renderClientSites(client, model.query);
      }
    }
    workspace.querySelector('[data-client-filter-count="all"]').textContent = String(model.rows.length);
    workspace.querySelector('[data-client-filter-count="attention"]').textContent = String(problemCount);
    workspace.querySelector("[data-clients-no-results]").hidden = visibleCount > 0 || model.rows.length === 0;
    for (const button of workspace.querySelectorAll("[data-server-client-filter]")) {
      const active = (button.dataset.serverClientFilter === "attention") === model.problemsOnly;
      button.classList.toggle("is-active", active);
      button.setAttribute("aria-pressed", String(active));
    }
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
    const searchField = workspace.querySelector("[data-server-clients-search]");
    if (!searchField) continue;
    const model = {
      rows: workspace.querySelectorAll("[data-server-client-row]"),
      query: searchField.value.trim().toLocaleLowerCase(),
      problemsOnly: false
    };
    for (const row of model.rows) initializeClient(row);
    workspace.addEventListener("input", event => {
      if (event.target === searchField) {
        model.query = searchField.value.trim().toLocaleLowerCase();
        renderClientList(workspace, model);
      }
      if (event.target.matches("[data-client-domain-search]")) {
        const client = clientModels.get(event.target.closest("[data-server-client-row]"));
        client.query = event.target.value.trim().toLocaleLowerCase();
        client.showAll = false;
        renderClientSites(client, model.query);
      }
    });
    workspace.addEventListener("change", event => {
      if (event.target.matches("[data-client-account-select]")) {
        updateClientAccountForm(event.target.closest("[data-client-account-form]"));
      }
    });
    workspace.addEventListener("click", event => {
      const filterButton = event.target.closest("[data-server-client-filter]");
      if (filterButton) {
        model.problemsOnly = filterButton.dataset.serverClientFilter === "attention";
        renderClientList(workspace, model);
      }
      if (event.target.closest("[data-client-show-all]")) {
        const client = clientModels.get(event.target.closest("[data-server-client-row]"));
        client.showAll = !client.showAll;
        renderClientSites(client, model.query);
      }
      const scheduleButton = event.target.closest("[data-client-schedule-toggle]");
      if (scheduleButton) {
        const row = scheduleButton.closest("[data-server-client-row]");
        const client = clientModels.get(row);
        client.scheduleOpen = !client.scheduleOpen;
        row.querySelector("[data-client-schedule]").hidden = !client.scheduleOpen;
        scheduleButton.setAttribute("aria-expanded", String(client.scheduleOpen));
      }
    });
    workspace.addEventListener("site-certificate-renewed", event => {
      const client = clientModels.get(event.target.closest("[data-server-client-row]"));
      const site = client.sites.find(candidate => candidate.element === event.target);
      site.invalidCertificates.delete(event.detail);
      renderClientList(workspace, model);
    });
    for (const form of workspace.querySelectorAll("[data-client-account-form]")) {
      updateClientAccountForm(form);
      form.addEventListener("submit", event => {
        if (!form.querySelector("[data-client-account-domain]").value ||
            form.querySelector("[data-client-account-confirm]").disabled) event.preventDefault();
      });
    }
    renderClientList(workspace, model);
  }
  // The shared dialogs live after <main>, unlike the former per-site dialogs.
  // Keep the selected server workspace in the query after POST.
  for (const dialog of ["clientQuotaModal", "clientDeleteModal"]) {
    document.getElementById(dialog)?.addEventListener("submit", event => {
      const form = event.target;
      if (!(form instanceof HTMLFormElement)) return;
      const navigation = readNavigation();
      const actionURL = new URL(form.action, window.location.href);
      actionURL.searchParams.set("section", "clients");
      actionURL.searchParams.set("installation", navigation.serverID || localPanel().dataset.serverId);
      form.action = actionURL.href;
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
