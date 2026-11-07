(function showSiteDebtNotice() {
  const scriptElement = document.currentScript;
  const noticeModel = {
    deadline: scriptElement ? scriptElement.dataset.deadline : "",
    debt: scriptElement ? scriptElement.dataset.debt : ""
  };

  async function loadPaymentOptions(panelElement, actionsElement) {
    const paymentOptionsElement = document.createElement("div");
    paymentOptionsElement.textContent = "Загружаем способы оплаты…";
    panelElement.insertBefore(paymentOptionsElement, actionsElement);
    try {
      const response = await fetch("/?site_debt_pay&format=json", {cache: "no-store", credentials: "omit"});
      if (!response.ok) throw new Error("payment options unavailable");
      const invoices = await response.json();
      const listElement = document.createElement("ul");
      for (const invoice of invoices) {
        if (!invoice.payment_url) continue;
        const paymentURL = new URL(invoice.payment_url);
        if (paymentURL.protocol !== "https:") continue;
        const invoiceElement = document.createElement("li");
        const paymentLinkElement = document.createElement("a");
        paymentLinkElement.href = paymentURL.href;
        paymentLinkElement.rel = "noopener noreferrer";
        paymentLinkElement.textContent = "Оплатить счет " + invoice.number + " — " + invoice.amount;
        invoiceElement.appendChild(paymentLinkElement);
        listElement.appendChild(invoiceElement);
      }
      paymentOptionsElement.replaceChildren(listElement);
      if (!listElement.childNodes.length) {
        paymentOptionsElement.textContent = "Для оплаты запросите счет у администратора сервера.";
      }
    } catch (paymentError) {
      paymentOptionsElement.textContent = "Способы оплаты временно недоступны. Запросите счет у администратора сервера.";
      console.error(paymentError);
    }
  }

  function renderNotice() {
    if (!document.body || document.getElementById("SiteBrushDebtNotice")) return;

    const overlayElement = document.createElement("div");
    overlayElement.id = "SiteBrushDebtNotice";
    overlayElement.setAttribute("data-sitebrush-owned", "true");
    overlayElement.setAttribute("role", "dialog");
    overlayElement.setAttribute("aria-label", "Сайт ожидает оплаты");

    const panelElement = document.createElement("div");
    panelElement.className = "panel";
    const headingElement = document.createElement("h2");
    headingElement.textContent = "Сайт заблокирован из-за неоплаты";
    const explanationElement = document.createElement("p");
    const deletionDate = new Date(noticeModel.deadline);
    if (Number.isNaN(deletionDate.getTime())) {
      explanationElement.textContent = "Контент доступен только для просмотра. Сайт будет удален, если долг не погасить.";
    } else {
      const daysRemaining = Math.max(0, Math.ceil((deletionDate.getTime() - Date.now()) / 86400000));
      explanationElement.textContent = "Контент доступен только для просмотра. Если долг не погасить, сайт будет удален через " + daysRemaining + " дн. (" + deletionDate.toLocaleDateString("ru-RU") + ").";
    }
    const debtElement = document.createElement("p");
    debtElement.textContent = "Долг: ";
    const amountElement = document.createElement("strong");
    amountElement.textContent = noticeModel.debt;
    debtElement.appendChild(amountElement);

    const actionsElement = document.createElement("p");
    for (const action of [
      {label: "Оплатить долг", href: "/?site_debt_pay"},
      {label: "Запросить счет", href: "/?site_debt_contact"},
      {label: "Восстановить сайт", href: "/?site_recovery"}
    ]) {
      if (actionsElement.childNodes.length) actionsElement.appendChild(document.createTextNode(" · "));
      const linkElement = document.createElement("a");
      linkElement.href = action.href;
      linkElement.textContent = action.label;
      actionsElement.appendChild(linkElement);
    }

    const closeElement = document.createElement("button");
    closeElement.type = "button";
    closeElement.textContent = "Продолжить просмотр";
    closeElement.addEventListener("click", function closeNotice() {
      overlayElement.remove();
    });

    panelElement.append(headingElement, explanationElement, debtElement, actionsElement, closeElement);
    overlayElement.appendChild(panelElement);
    document.body.appendChild(overlayElement);
    loadPaymentOptions(panelElement, actionsElement);
  }

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", renderNotice, {once: true});
  } else {
    renderNotice();
  }
})();
