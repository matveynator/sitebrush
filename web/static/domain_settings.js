(function initWebsiteSettingsNavigation() {
  "use strict";
  const layout = document.querySelector(".settings-layout");
  if (!layout) return;
  const panels = Array.from(layout.querySelectorAll("[data-settings-panel]"));
  const links = Array.from(layout.querySelectorAll("[data-settings-target]"));
  const sections = new Set(panels.map(panel => panel.id));

  function selectedSection() {
    const section = window.location.hash.slice(1);
    return sections.has(section) ? section : "overview";
  }

  function showSection(section) {
    for (const panel of panels) panel.hidden = panel.id !== section;
    for (const link of links) {
      if (link.dataset.settingsTarget === section) {
        link.setAttribute("aria-current", "page");
      } else {
        link.removeAttribute("aria-current");
      }
    }
  }

  // Without JavaScript all sections remain visible and links scroll to anchors.
  showSection(selectedSection());
  window.addEventListener("hashchange", function onSectionChange() {
    showSection(selectedSection());
  });

  layout.addEventListener("click", function showLinkedSection(event) {
    const link = event.target.closest('a[href^="#"]');
    if (!link) return;
    const section = link.getAttribute("href").slice(1);
    if (sections.has(section)) showSection(section);
  });

  // Form fragments are client-side only; keep the active section on redirect.
  layout.addEventListener("submit", function preserveSection(event) {
    const form = event.target;
    if (!(form instanceof HTMLFormElement)) return;
    const panel = form.closest("[data-settings-panel]");
    if (!panel || form.method.toLowerCase() !== "post") return;
    form.action = form.action.split("#", 1)[0] + "#" + panel.id;
  });
})();
