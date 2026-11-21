/* The server UI changes only presentation. Existing forms own their mutations. */
(function initializeServerSettingsNavigation() {
  "use strict";

  const layouts = Array.from(document.querySelectorAll("[data-server-config-layout]"));
  if (layouts.length === 0) return;
  const permittedSections = new Set(["overview", "updates", "registration", "demo", "clients"]);

  function sectionFromHash() {
    const hash = window.location.hash.slice(1);
    const section = hash === "server-update" ? "updates"
      : hash.startsWith("server-") ? hash.slice(7) : "";
    return permittedSections.has(section) ? section : "overview";
  }

  function choose(layout, section) {
    const buttons = Array.from(layout.querySelectorAll("[data-server-config-target]"));
    const panels = Array.from(layout.querySelectorAll("[data-server-config-view]"));
    const available = new Set(panels.map(panel => panel.dataset.serverConfigView));
    const selected = available.has(section) ? section : "overview";
    for (const panel of panels) {
      panel.hidden = panel.dataset.serverConfigView !== selected;
    }
    for (const button of buttons) {
      const active = button.dataset.serverConfigTarget === selected;
      button.setAttribute("aria-current", active ? "page" : "false");
    }
    layout.dataset.serverConfigSelected = selected;
  }

  function selectVisibleServer() {
    for (const layout of layouts) {
      if (layout.closest("[data-expense-server-panel]")?.hidden) continue;
      choose(layout, sectionFromHash());
    }
  }

  for (const layout of layouts) {
    const nav = layout.querySelector(".server-config-nav");
    if (!nav) continue;

    // With scripts disabled, all sections stay in document order.
    choose(layout, sectionFromHash());
    nav.hidden = false;

    nav.addEventListener("click", function navigateServer(event) {
      const button = event.target.closest("[data-server-config-target]");
      if (button && nav.contains(button)) {
        const section = button.dataset.serverConfigTarget;
        choose(layout, section);
        const fragment = section === "updates" ? "server-update" : "server-" + section;
        if (window.location.hash !== "#" + fragment) {
          window.location.hash = fragment;
        }
      }

      const networkLink = event.target.closest("[data-server-config-network]");
      if (networkLink && nav.contains(networkLink)) {
        const disclosure = document.getElementById("server-network");
        if (disclosure) disclosure.open = true;
      }
    });

    layout.addEventListener("submit", function preserveSectionAfterSave(event) {
      const form = event.target;
      if (!(form instanceof HTMLFormElement)) return;
      if (!form.closest("[data-server-config-view]")) return;
      if (form.method.toLowerCase() !== "post") return;
      const section = layout.dataset.serverConfigSelected || "overview";
      const fragment = section === "updates" ? "server-update" : "server-" + section;
      form.action = form.action.split("#", 1)[0] + "#" + fragment;
    });
  }

  window.addEventListener("hashchange", function onSectionHashChange() {
    const network = document.getElementById("server-network");
    if (network && window.location.hash === "#server-network") network.open = true;
    selectVisibleServer();
  });
  const selector = document.querySelector("[data-expense-server-select]");
  if (selector) selector.addEventListener("change", selectVisibleServer);
  const network = document.getElementById("server-network");
  if (network && window.location.hash === "#server-network") network.open = true;
})();
