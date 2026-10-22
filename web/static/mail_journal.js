(function startMailJournal() {
  'use strict';
  const settingsElement = document.querySelector('[data-mail-settings]');
  const labelsElement = document.getElementById('mail-journal-labels');
  if (!settingsElement || !labelsElement) return;
  const journalModel = { labels: {}, snapshot: null, feedback: '', connection: null, retryTimer: null, expanded: new Set(), initialPositionRestored: false };
  try { journalModel.labels = JSON.parse(labelsElement.textContent); }
  catch (parseError) { document.querySelector('[data-mail-feedback]').textContent = String(parseError); return; }
  const dateFormatter = new Intl.DateTimeFormat(settingsElement.dataset.language || navigator.language, { dateStyle: 'long', timeStyle: 'long' });
  const securityView = settingsElement.dataset.mode === 'security';

  function label(labelName) { return journalModel.labels[labelName] || labelName; }
  function normalizeSnapshot(snapshot) {
    // Journal operations use Go field names; their embedded events use explicit JSON names.
    (snapshot.Servers || []).forEach(function normalizeServer(server) {
      (server.MailOperations || []).forEach(function normalizeOperation(operation) {
        operation.OriginInstallationID = String(operation.OriginInstallationID || '');
        const receivedSteps = Array.isArray(operation.Steps) ? operation.Steps : [];
        operation.Steps = receivedSteps.filter(function validStep(step) { return step !== null && typeof step === 'object' && !Array.isArray(step); }).map(function normalizeStep(step) {
          return {
            Stage: String(step.stage || step.Stage || ''),
            AcceptedBy: String(step.accepted_by || step.AcceptedBy || ''),
            CreatedAt: String(step.created_at || step.CreatedAt || ''),
            Transport: String(step.transport || step.Transport || ''),
            Route: String(step.route || step.Route || ''),
            Attempt: step.attempt || step.Attempt || 0,
            SMTPCode: String(step.smtp_code || step.SMTPCode || ''),
            Detail: String(step.detail || step.Detail || '')
          };
        });
      });
    });
    return snapshot;
  }
  function readableTime(timestamp) {
    const parsedDate = new Date(String(timestamp).replace(/(\.\d{3})\d+(Z|[+-])/, '$1$2'));
    return Number.isNaN(parsedDate.getTime()) ? label('mail_journal_unknown') : dateFormatter.format(parsedDate);
  }
  function element(tagName, textContent, className) {
    const createdElement = document.createElement(tagName);
    if (textContent !== undefined) createdElement.textContent = textContent;
    if (className) createdElement.className = className;
    return createdElement;
  }
  function showFeedback() {
    document.querySelectorAll('[data-mail-feedback]').forEach(function updateFeedback(feedbackElement) { feedbackElement.textContent = journalModel.feedback; });
  }
  function sendCommand(command) {
    if (!journalModel.connection || journalModel.connection.readyState !== WebSocket.OPEN) {
      journalModel.feedback = label('mail_journal_disconnected'); showFeedback(); return;
    }
    command.CSRF = settingsElement.dataset.csrf;
    journalModel.feedback = label('mail_journal_working'); showFeedback();
    journalModel.connection.send(JSON.stringify(command));
  }
  function actionButton(buttonLabel, command) {
    const buttonElement = element('button', buttonLabel, 'btn btn-outline-secondary btn-sm me-2');
    buttonElement.type = 'button';
    buttonElement.addEventListener('click', function submitJournalAction() { sendCommand(command); });
    return buttonElement;
  }
  function visibleBlocks() {
    const accountBlocks = (journalModel.snapshot.AccountBlocks || []).map(function accountBlock(entry) { return {IP: entry.IP, Reason: entry.Reason, ExpiresAt: entry.Until, Scope: 'installation', OriginInstallationID: journalModel.snapshot.LocalInstallationID, Source: 'account_mail'}; });
    return [...(journalModel.snapshot.Blocks || []), ...accountBlocks];
  }
  function blockFor(originID, address) {
    return visibleBlocks().find(function matchesBlock(block) { return block.IP === address && (block.Scope === 'global' || block.OriginInstallationID === originID); });
  }
  function addressDescription(address) {
    if (!address) return label('mail_journal_unknown');
    const geography = (journalModel.snapshot.IPDetails || {})[address];
    return address + ' · ' + (geography && geography.Location ? geography.Location : label('mail_journal_geography_unknown'));
  }
  function blockButton(originID, address, operationID) {
    const blockedEntry = blockFor(originID, address);
    const geography = (journalModel.snapshot.IPDetails || {})[address];
    if (!securityView || (!blockedEntry && (!geography || !geography.CanBlock))) return null;
    const buttonElement = actionButton(label(blockedEntry ? 'mail_journal_unblock' : 'mail_journal_block'), { Action: blockedEntry ? 'unblock' : 'block', OperationID: operationID || '', OriginInstallationID: originID, IP: address, Source: blockedEntry ? blockedEntry.Source || '' : '' });
    if (blockedEntry && !journalModel.snapshot.Central && blockedEntry.Scope !== 'installation') { buttonElement.disabled = true; buttonElement.textContent = label('mail_journal_central_managed'); }
    return buttonElement;
  }
  function humanTransport(report) {
    return String(report || '').split('; ').map(function describeConnection(connectionReport) {
      const fields = new Map();
      connectionReport.split(/\s+/).forEach(function parseField(transportField) {
        const separator = transportField.indexOf('=');
        if (separator > 0) fields.set(transportField.slice(0, separator), transportField.slice(separator + 1));
      });
      if ((fields.get('transport') || '').startsWith('netchan')) return 'NetChan ' + (fields.get('netchan_sender') || '') + ' / ' + (fields.get('protocol') || '').toUpperCase() + ' / ' + (fields.get('network') || '').toUpperCase() + (fields.get('destination') ? ' → ' + fields.get('destination') : '');
      if (fields.get('transport') === 'smtp') return 'SMTP ' + (fields.get('mx') || '') + (fields.get('destination') ? ' → ' + fields.get('destination') : '') + (fields.get('protocol') === 'starttls' ? ' / STARTTLS' : '');
      if (fields.get('transport') === 'https') return 'HTTPS / ' + (fields.get('protocol') || '');
      return connectionReport;
    }).filter(Boolean).join(' → ');
  }
  function readableDetail(detail) {
    try {
      const diagnostic = JSON.parse(detail);
      if (diagnostic && typeof diagnostic === 'object' && !Array.isArray(diagnostic)) {
        return [diagnostic.smtp_server, diagnostic.smtp_code, diagnostic.smtp_reply || diagnostic.failure].filter(Boolean).join(' · ');
      }
    } catch (parseError) {
      return detail;
    }
    return detail;
  }
  function stepLabel(step) {
    if (step.Stage === 'accepted' && step.AcceptedBy === 'central_relay') return label('mail_monitor_stage_relay_accepted');
    return label('mail_monitor_stage_' + step.Stage);
  }
  function renderOperation(operation) {
    const operationElement = element('div', undefined, 'expense-mail-monitor-event border-bottom py-2');
    operationElement.append(element('strong', label('mail_monitor_stage_' + operation.Stage) + ' · ' + operation.Recipient));
    operationElement.append(element('div', readableTime(operation.UpdatedAt) + ' · ' + operation.Domain, 'small text-muted'));
    operationElement.append(element('div', label('mail_journal_initiator') + ': ' + addressDescription(operation.InitiatorIP) + ' · ' + label('mail_journal_installation') + ': ' + (operation.InstallationIP ? addressDescription(operation.InstallationIP) + ' (' + label('mail_journal_reported_ip') + ')' : operation.OriginInstallationID.slice(0, 12) || label('mail_journal_unknown')), 'small'));
    const steps = [...operation.Steps].sort(function compareSteps(firstStep, secondStep) { return (firstStep.CreatedAt || '\uffff').localeCompare(secondStep.CreatedAt || '\uffff'); });
    const summaries = [...new Set(steps.filter(function selectProgress(step) { return step.Stage !== 'queued' && step.Stage !== 'retry' && (step.Stage !== 'attempt' || step.Route === 'local'); }).map(function summarizeStep(step) { return step.Stage === 'attempt' ? label('mail_journal_smtp_started') : stepLabel(step) + (step.Transport ? ' (' + humanTransport(step.Transport) + ')' : ''); }))];
    operationElement.append(element('div', summaries.join(' → '), 'small'));
    const detailsElement = element('details');
    detailsElement.open = journalModel.expanded.has(operation.ID);
    detailsElement.addEventListener('toggle', function rememberExpandedOperation() { if (detailsElement.open) journalModel.expanded.add(operation.ID); else journalModel.expanded.delete(operation.ID); });
    detailsElement.append(element('summary', label('mail_journal_steps')));
    detailsElement.append(element('p', operation.OriginInstallationID + ' · ' + operation.MessageID + ' · ' + operation.FlowID, 'small text-muted'));
    steps.forEach(function renderStep(step) {
      const fields = [readableTime(step.CreatedAt), stepLabel(step)];
      if (step.Transport) fields.push(humanTransport(step.Transport));
      if (step.Attempt) fields.push(label('mail_monitor_attempt_label') + ' ' + step.Attempt);
      if (step.SMTPCode) fields.push('SMTP ' + step.SMTPCode);
      if (step.Detail) fields.push(readableDetail(step.Detail));
      detailsElement.append(element('p', fields.join(' · '), 'small mb-1'));
    });
    operationElement.append(detailsElement);
    const smtpAttempts = steps.filter(function smtpWork(step) { return step.Route === 'local' && step.Stage === 'attempt'; });
    const retrySteps = steps.filter(function retryWork(step) { return step.Route === 'local' && step.Stage === 'retry'; });
    const smtpStartedAt = smtpAttempts.length ? smtpAttempts[smtpAttempts.length - 1].CreatedAt : '';
    const retryAt = retrySteps.length ? retrySteps[retrySteps.length - 1].CreatedAt : '';
    const smtpAttemptActive = smtpAttempts.length > 0 && (!smtpStartedAt || !retryAt || retryAt <= smtpStartedAt);
    if (!smtpAttemptActive && !['accepted', 'failed', 'cancelled', 'recovery_complete', 'registration_complete', 'cancel_requested', 'cancel_too_late'].includes(operation.Stage) && operation.MessageID) operationElement.append(actionButton(label('mail_journal_cancel'), { Action: 'cancel', OperationID: operation.ID }));
    if (operation.InitiatorIP) {
      const blockAction = blockButton(operation.OriginInstallationID, operation.InitiatorIP, operation.ID);
      if (blockAction) operationElement.append(blockAction);
    }
    return operationElement;
  }
  function renderOperations(operations) {
    const fragment = document.createDocumentFragment();
    const groups = new Map();
    operations.forEach(function groupOperation(operation) {
      const bucket = Math.floor(new Date(operation.CreatedAt).getTime() / 600000);
      const groupKey = operation.InitiatorIP ? [operation.OriginInstallationID, operation.InitiatorIP, operation.Kind, bucket].join('|') : operation.ID;
      if (!groups.has(groupKey)) groups.set(groupKey, []);
      groups.get(groupKey).push(operation);
    });
    groups.forEach(function renderGroup(groupedOperations) {
      if (groupedOperations.length < 5) { groupedOperations.forEach(function renderSingle(operation) { fragment.append(renderOperation(operation)); }); return; }
      const seriesElement = element('details', undefined, 'border-bottom py-2');
      seriesElement.append(element('summary', addressDescription(groupedOperations[0].InitiatorIP) + ' · ' + label('mail_journal_series') + ': ' + groupedOperations.length));
      groupedOperations.forEach(function renderSeriesOperation(operation) { seriesElement.append(renderOperation(operation)); });
      fragment.append(seriesElement);
    });
    if (!operations.length) fragment.append(element('p', label('mail_monitor_empty')));
    return fragment;
  }
  function renderSecurity() {
    document.querySelectorAll('[data-mail-blocks]').forEach(function renderBlockList(containerElement) {
      containerElement.replaceChildren();
      visibleBlocks().forEach(function renderBlock(block) {
        const rowElement = element('div', addressDescription(block.IP) + ' · ' + label(block.Scope === 'global' ? 'mail_journal_global' : 'mail_journal_local') + ' · ' + (block.ExpiresAt ? label('mail_journal_until') + ' ' + readableTime(block.ExpiresAt) : readableTime(block.CreatedAt)) + (block.Reason ? ' · ' + block.Reason : ''), 'small mb-2');
        const blockAction = blockButton(block.OriginInstallationID, block.IP);
        if (blockAction) rowElement.append(blockAction);
        containerElement.append(rowElement);
      });
    });
    document.querySelectorAll('[data-mail-abuse]').forEach(function renderAbuseList(containerElement) {
      containerElement.replaceChildren();
      (journalModel.snapshot.Abuse || []).forEach(function renderAbuse(summary) {
        const summaryElement = element('details', undefined, 'small border-top py-2');
        summaryElement.append(element('summary', addressDescription(summary.IP) + ' · ' + label('mail_journal_attempts') + ': ' + summary.Attempts + ' · ' + readableTime(summary.FirstAt) + ' — ' + readableTime(summary.LastAt)));
        (summary.Samples || []).forEach(function renderSample(sample) { summaryElement.append(element('p', sample, 'mb-1')); });
        if (summary.IP && summary.OriginInstallationID !== 'overflow') {
          const blockAction = blockButton(summary.OriginInstallationID, summary.IP);
          if (blockAction) summaryElement.append(blockAction);
        }
        containerElement.append(summaryElement);
      });
    });
  }
  function renderPagination(containerElement, pagination) {
    containerElement.replaceChildren(element('span', label('mail_monitor_page') + ' ' + pagination.Page + ' / ' + pagination.Pages + ' · ' + label('mail_monitor_total') + ': ' + pagination.Count));
    const links = [{URL: pagination.FirstURL, Label: label('mail_monitor_first')}, {URL: pagination.PreviousURL, Label: label('mail_monitor_previous')}];
    (pagination.Links || []).forEach(function pageLink(page) { links.push({ URL: page.URL, Label: String(page.Number), Current: page.Current }); });
    links.push({URL: pagination.NextURL, Label: label('mail_monitor_next')}, {URL: pagination.LastURL, Label: label('mail_monitor_last')});
    links.forEach(function renderLink(page) {
      if (!page.URL) return;
      const linkElement = element('a', page.Label, 'btn btn-sm ' + (page.Current ? 'btn-primary' : 'btn-outline-primary'));
      linkElement.href = page.URL;
      if (page.Current) linkElement.setAttribute('aria-current', 'page');
      containerElement.append(linkElement);
    });
  }
  function render() {
    if (!journalModel.snapshot) { showFeedback(); return; }
    (journalModel.snapshot.Servers || []).forEach(function renderServer(server) {
      document.querySelectorAll('[data-mail-journal]').forEach(function updateJournal(containerElement) { if (containerElement.dataset.mailJournal === server.Server.ID) containerElement.replaceChildren(renderOperations(server.MailOperations || [])); });
      document.querySelectorAll('[data-mail-pagination]').forEach(function updatePagination(containerElement) { if (containerElement.dataset.mailPagination === server.Server.ID) renderPagination(containerElement, server.MailPagination); });
      document.querySelectorAll('[data-mail-count]').forEach(function updateCounter(counterElement) { if (counterElement.dataset.mailServer === server.Server.ID) counterElement.textContent = server.MailLastDay[counterElement.dataset.mailCount] + ' / ' + server.MailLast90Days[counterElement.dataset.mailCount]; });
    });
    if (securityView) renderSecurity();
    showFeedback();
  }
  function restoreJournalPosition() {
    if (!securityView || journalModel.initialPositionRestored) return;
    journalModel.initialPositionRestored = true;
    const fragment = new URL(window.location.href).hash;
    if (fragment !== '#mail-security' && !/^#mail-monitor-\d+$/.test(fragment)) return;
    const sectionElement = document.getElementById(fragment.slice(1));
    if (sectionElement) sectionElement.scrollIntoView({block: 'start'});
  }
  function connect() {
    const socketURL = new URL(window.location.href);
    socketURL.protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    socketURL.searchParams.set('mail_monitor_ws', '1');
    if (securityView) {
      socketURL.searchParams.delete('expenses');
      socketURL.searchParams.set('analytics', '');
      socketURL.searchParams.set('tab', 'security');
    } else {
      socketURL.searchParams.delete('analytics');
      socketURL.searchParams.delete('tab');
      socketURL.searchParams.set('expenses', '');
    }
    socketURL.hash = '';
    journalModel.connection = new WebSocket(socketURL);
    journalModel.connection.addEventListener('open', function connected() { journalModel.feedback = ''; showFeedback(); });
    journalModel.connection.addEventListener('message', function receiveSnapshot(messageEvent) {
      try {
        const response = JSON.parse(messageEvent.data);
        if (response.Error) journalModel.feedback = response.Error;
        else if (response.Status) journalModel.feedback = label('mail_journal_result_' + response.Status);
        else {
          journalModel.snapshot = normalizeSnapshot(response);
          render();
          restoreJournalPosition();
          return;
        }
        render();
      } catch (parseError) { journalModel.feedback = String(parseError); showFeedback(); }
    });
    journalModel.connection.addEventListener('error', function connectionFailed() { journalModel.feedback = label('mail_journal_disconnected'); showFeedback(); });
    journalModel.connection.addEventListener('close', function disconnected() {
      journalModel.feedback = label('mail_journal_disconnected'); showFeedback();
      if (!journalModel.retryTimer) journalModel.retryTimer = window.setTimeout(function reconnect() { journalModel.retryTimer = null; connect(); }, 3000);
    });
  }
  document.querySelectorAll('time[datetime]').forEach(function formatInitialTime(timeElement) { timeElement.textContent = readableTime(timeElement.dateTime); });
  connect();
})();
