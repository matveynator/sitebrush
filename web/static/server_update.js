(function initializeServerUpdate() {
  'use strict';
  const updateRoot = document.querySelector('[data-server-update]');
  if (!updateRoot) return;
  const announcementRoot = updateRoot.querySelector('[data-announcements]');
  const updateModel = { serverVersion: updateRoot.dataset.serverVersion || '', lastContact: 0, pendingAction: '', queuedCommand: null, logText: '', renderedLog: '', connected: false, state: null, error: '', pending: false, socket: null, stopped: false, preview: null, campaigns: [], reason: 'security', backupPolicy: {databases: true, analytics: false, files: false, settings: false} };
  const stageLabels = Object.fromEntries(Array.from(updateRoot.querySelectorAll('[data-stage]'), function readStageLabel(stageElement) {
    return [stageElement.dataset.stage, stageElement.textContent];
  }));

  const interfaceLabels = Object.fromEntries(Array.from(updateRoot.querySelectorAll('[data-label]'), function readInterfaceLabel(labelElement) {
    return [labelElement.dataset.label, labelElement.textContent];
  }));
  const upgradeStages = ['preparing', 'downloading', 'verifying', 'inspecting', 'preparing_backup', 'stopping', 'backing_up', 'installing', 'restarting', 'checking', 'activating'];
  const rollbackStages = ['preparing', 'inspecting', 'preparing_backup', 'stopping', 'backing_up', 'installing', 'restarting', 'checking', 'activating'];

  function label(labelName) { return interfaceLabels[labelName] || labelName; }
  function stageLabel(stageName) { return stageLabels[stageName] || label(stageName); }
  function transferAdvancing(progress) { return updateModel.connected && (!progress.advanced_at || Date.now() - timestamp(progress.advanced_at) < 10000); }
  function timestamp(timeText) { const milliseconds = Date.parse(timeText); return Number.isFinite(milliseconds) && milliseconds > 0 ? milliseconds : 0; }
  function duration(milliseconds) {
    const seconds = Math.max(0, Math.floor(milliseconds / 1000));
    return String(Math.floor(seconds / 3600)).padStart(2, '0') + ':' + String(Math.floor(seconds / 60) % 60).padStart(2, '0') + ':' + String(seconds % 60).padStart(2, '0');
  }
  function bytes(byteCount) {
    const units = ['B', 'KiB', 'MiB', 'GiB', 'TiB'];
    const exponent = byteCount > 0 ? Math.min(units.length - 1, Math.floor(Math.log(byteCount) / Math.log(1024))) : 0;
    return (byteCount / Math.pow(1024, exponent)).toLocaleString(document.documentElement ? document.documentElement.lang : undefined, {maximumFractionDigits: exponent ? 1 : 0}) + ' ' + units[exponent];
  }
  function putText(selector, text) {
    const textElement = updateRoot.querySelector(selector);
    if (textElement.textContent !== text) textElement.textContent = text;
  }

  function renderUpdate() {
    const updateState = updateModel.state;
    const operationBusy = Boolean(updateModel.queuedCommand) || (updateModel.pending && updateModel.pendingAction !== 'status') || Boolean(updateState && updateState.busy);
    putText('[data-update-current]', updateModel.serverVersion || (updateState ? updateState.current.version : '—'));
    putText('[data-update-launcher-version]', updateState && updateState.launcher_version ? label('launcher_version').replace('%s', updateState.launcher_version) : '');
    putText('[data-update-available]', updateState && updateState.available ? updateState.available.version : '—');
    putText('[data-update-previous]', updateState && updateState.previous ? updateState.previous.version : '—');
    const selectedVersion = updateRoot.querySelector('[data-update-selected]');
    selectedVersion.hidden = !updateState || !updateModel.serverVersion || updateState.current.version === updateModel.serverVersion;
    selectedVersion.textContent = updateState ? label('selected').replace('%s', updateState.current.version) : '';
    const progress = updateState && updateState.progress && updateState.progress.action ? updateState.progress : null;
    let operationStage = progress ? progress.stage : updateState && updateState.stage;
    if (progress && !timestamp(progress.finished_at) && ['completed', 'rolled_back'].includes(progress.stage)) operationStage = 'cleanup';
    if (updateState && updateState.checking) operationStage = 'checking_release';
    putText('[data-update-status]', operationStage ? stageLabel(operationStage) : updateRoot.dataset.waiting);
    updateRoot.dataset.connected = String(updateModel.connected);
    updateRoot.dataset.outcome = operationStage || '';
    const errorElement = updateRoot.querySelector('[data-update-error]');
    const operationError = updateState && (updateState.error || updateState.operation_error);
    errorElement.textContent = updateModel.error || operationError || '';
    errorElement.hidden = !errorElement.textContent;
    const cleanupElement = updateRoot.querySelector('[data-update-cleanup-error]');
    cleanupElement.textContent = updateState && updateState.cleanup_error ? label('cleanup_warning') + ': ' + updateState.cleanup_error : '';
    cleanupElement.hidden = !cleanupElement.textContent;
    updateRoot.querySelector('[data-update-check]').disabled = !updateModel.connected || operationBusy;
    const newerRelease = updateState && updateState.available && /^\d+$/.test(updateState.current.version) && /^\d+$/.test(updateState.available.version) && BigInt(updateState.available.version) > BigInt(updateState.current.version);
    updateRoot.querySelector('[data-update-install]').disabled = !updateModel.connected || operationBusy || !newerRelease || !updateState.backup_options || Boolean(updateState.recovery_required);
    updateRoot.querySelector('[data-update-rollback]').disabled = !updateModel.connected || operationBusy || !updateState || !updateState.rollback_available || !updateState.backup_options || Boolean(updateState.recovery_required);
    updateRoot.querySelector('[data-update-rollback-reason]').hidden = !updateState || !updateState.previous || updateState.rollback_available;
    const notificationElement = updateRoot.querySelector('[data-update-notification-error]');
    notificationElement.hidden = !updateState || !updateState.notification_error;
    notificationElement.textContent = updateState && updateState.notification_error ? label('notification_' + (updateState.notification_code || 'contact')) + ' ' + updateState.notification_error : '';
    const displayedPolicy = updateState && updateState.busy && !updateState.checking && updateState.backup_policy ? updateState.backup_policy : updateModel.backupPolicy;
    updateRoot.querySelector('[data-update-backup-warning]').hidden = Object.values(displayedPolicy).every(Boolean);
    updateRoot.querySelector('[data-update-launcher-warning]').hidden = !updateState || updateState.backup_options === true;
    updateRoot.querySelector('[data-update-backup-options]').disabled = operationBusy;
    const backupGroups = updateState && updateState.backup_groups ? updateState.backup_groups : [];
    putText('[data-update-backup-estimate]', backupGroups.filter(function selectedBackupGroup(group) { return updateState.backup_policy && updateState.backup_policy[group.name]; }).map(function backupGroupEstimate(group) { return label('backup_' + group.name) + ': ' + bytes(group.bytes) + ' (' + group.files + ')'; }).join(' · '));
    const cancelButton = updateRoot.querySelector('[data-update-cancel]');
    cancelButton.hidden = !updateState || !updateState.busy || !updateState.backup_options || ['completed', 'rolled_back', 'failed', 'interrupted'].includes(operationStage);
    cancelButton.disabled = !updateModel.connected || updateModel.pending;
    for (const groupName of ['databases', 'analytics', 'files', 'settings']) {
      const checkbox = updateRoot.querySelector('[data-backup-group="' + groupName + '"]');
      checkbox.checked = displayedPolicy[groupName];
      checkbox.disabled = operationBusy;
    }
    renderOperation(progress);
    renderConnection();
    renderAnnouncements();
  }

  function renderConnection() {
    const connectionText = updateModel.connected ? label('connected') : (updateModel.lastContact ? (updateModel.state && updateModel.state.busy && !updateModel.state.checking ? label('restarting_notice') : label('reconnecting')) : updateRoot.dataset.waiting);
    putText('[data-update-connection]', connectionText);
    putText('[data-update-last-contact]', updateModel.lastContact ? ' · ' + label('last_contact') + ' ' + new Date(updateModel.lastContact).toLocaleTimeString() : '');
  }

  function operationStages(progress) {
    const stages = progress && progress.action === 'rollback' ? rollbackStages : upgradeStages;
    const backupPolicy = updateModel.state && updateModel.state.backup_policy;
    if (!backupPolicy || Object.values(backupPolicy).some(Boolean)) return stages;
    return stages.filter(function omitDisabledBackupStage(stageName) { return !['inspecting', 'preparing_backup', 'backing_up'].includes(stageName); });
  }

  function renderOperation(progress) {
    const events = progress && progress.events ? progress.events : [];
    const recovering = events.some(function rollbackStarted(operationEvent) { return operationEvent.stage === 'rolling_back'; });
    putText('[data-update-operation]', progress ? label(recovering ? 'auto_rollback' : progress.action) : label('no_telemetry'));
    putText('[data-update-route]', progress ? (recovering ? progress.target_version + ' → ' + progress.from_version : progress.from_version + ' → ' + progress.target_version) : '');
    const stages = operationStages(progress);
    const effectiveStage = progress && progress.stage === 'failed' ? progress.failed_stage : progress && progress.stage;
    const stageIndex = stages.indexOf(effectiveStage);
    const work = progress && progress.work ? progress.work : {};
    let stageFraction = 0;
    if (work.total_bytes > 0) stageFraction = Math.min(1, work.bytes / work.total_bytes);
    else if (work.total_files > 0) stageFraction = Math.min(1, work.files / work.total_files);
    // Inspection discovers its denominator; it cannot promise a fraction yet.
    if (effectiveStage === 'inspecting') stageFraction = 0;
    let percent = stageIndex >= 0 ? Math.floor(100 * (stageIndex + stageFraction) / stages.length) : 0;
    const finishedAt = progress ? timestamp(progress.finished_at) : 0;
    const expectedVersion = progress && recovering ? progress.from_version : progress && progress.target_version;
    const confirmed = progress && finishedAt && (!updateModel.state.busy || updateModel.state.checking) && updateModel.state.current.version === expectedVersion && updateModel.serverVersion === expectedVersion;
    if (recovering) percent = 0;
    if (progress && ['completed', 'rolled_back'].includes(progress.stage)) percent = confirmed ? 100 : 99;
    percent = Math.max(0, Math.min(100, percent));
    putText('[data-update-percent]', progress ? percent + '%' : '—');
    const progressElement = updateRoot.querySelector('[data-update-progress]');
    if (progress) progressElement.setAttribute('aria-valuenow', String(percent));
    else progressElement.removeAttribute('aria-valuenow');
    progressElement.setAttribute('aria-valuetext', progress ? stageLabel(progress.stage) + ' · ' + percent + '%' : label('no_telemetry'));
    updateRoot.querySelector('[data-update-progress-fill]').style.width = percent + '%';
    const workParts = [];
    if (work.total_bytes > 0) workParts.push((effectiveStage === 'inspecting' ? '' : bytes(work.bytes || 0) + ' / ') + bytes(work.total_bytes));
    if (work.total_files > 0) workParts.push(label('files').replace('%s', (effectiveStage === 'inspecting' ? '' : String(work.files || 0) + ' / ') + String(work.total_files)));
    if (work.speed > 0 && transferAdvancing(progress) && !finishedAt) workParts.push(label('speed').replace('%s', bytes(work.speed)));
    putText('[data-update-work]', workParts.join(' · '));
    renderTimes(progress);
    renderSteps(progress, stages, events);
    renderJournal(progress, events);
  }

  function renderTimes(progress) {
    const startedAt = progress ? timestamp(progress.started_at) : 0;
    const stageAt = progress ? timestamp(progress.stage_at) : 0;
    const finishedAt = progress ? timestamp(progress.finished_at) : 0;
    const endingAt = finishedAt || Date.now();
    putText('[data-update-started]', startedAt ? new Date(startedAt).toLocaleString() : '—');
    putText('[data-update-elapsed]', startedAt ? duration(endingAt - startedAt) : '—');
    putText('[data-update-stage-elapsed]', stageAt ? duration(endingAt - stageAt) : '—');
    const heartbeatElement = updateRoot.querySelector('[data-update-heartbeat]');
    heartbeatElement.hidden = !progress || !progress.updated_at;
    heartbeatElement.textContent = progress && progress.updated_at ? label('last_contact') + ' ' + new Date(progress.updated_at).toLocaleString() + (progress.advanced_at ? ' · ' + label('backup_progress') + ': ' + new Date(progress.advanced_at).toLocaleString() : '') : '';
    let remaining = progress && !finishedAt ? label('calculating') : '—';
    const work = progress && progress.work;
    if (work && work.speed > 0 && work.total_bytes > work.bytes && transferAdvancing(progress) && !finishedAt && ['downloading', 'preparing_backup', 'backing_up'].includes(progress.stage)) {
      remaining = label('stage_remaining').replace('%s', duration(1000 * (work.total_bytes - work.bytes) / work.speed));
    }
    putText('[data-update-remaining]', remaining);
  }

  function renderSteps(progress, stages, events) {
    updateRoot.querySelector('[data-update-steps-card]').hidden = !progress;
    const stepList = updateRoot.querySelector('[data-update-steps]');
    stepList.replaceChildren();
    if (!progress) return;
    const visibleStages = events.some(function hasRecovery(operationEvent) { return operationEvent.stage === 'rolling_back'; }) ? stages.concat(['rolling_back']) : stages;
    visibleStages.forEach(function renderStage(stageName, stageNumber) {
      const startEvent = events.find(function stageStarted(operationEvent) { return operationEvent.stage === stageName && operationEvent.code === 'stage_started'; });
      const endEvent = events.find(function stageFinished(operationEvent) { return operationEvent.stage === stageName && operationEvent.code === 'stage_finished'; });
      const failed = progress.failed_stage === stageName;
      const failureEvent = events.find(function stageFailed(operationEvent) { return operationEvent.stage === stageName && operationEvent.code === 'stage_failed'; });
      const finalSuccess = ['completed', 'rolled_back'].includes(progress.stage) && timestamp(progress.finished_at);
      const completed = endEvent || (finalSuccess && ((stageName === 'rolling_back') || (stageName === 'activating' && !visibleStages.includes('rolling_back'))));
      const running = progress.stage === stageName && !timestamp(progress.finished_at);
      const stepState = failed ? 'failed' : completed ? 'done' : running ? 'running' : 'waiting';
      const stageRow = document.createElement('li');
      stageRow.dataset.state = stepState;
      const icon = document.createElement('span');
      icon.className = 'update-step-icon';
      icon.textContent = stepState === 'done' ? '✓' : stepState === 'failed' ? '!' : String(stageNumber + 1);
      icon.setAttribute('aria-hidden', 'true');
      const description = document.createElement('span');
      description.textContent = stageLabel(stageName);
      const stageDetail = document.createElement('small');
      stageDetail.textContent = label('step_' + stepState);
      const stageEnding = endEvent ? timestamp(endEvent.at) : failureEvent ? timestamp(failureEvent.at) : running ? Date.now() : timestamp(progress.finished_at);
      if (startEvent && stageEnding) stageDetail.textContent += ' · ' + duration(stageEnding - timestamp(startEvent.at));
      description.append(stageDetail);
      stageRow.append(icon, description);
      stepList.append(stageRow);
    });
  }

  function renderJournal(progress, events) {
    updateRoot.querySelector('[data-update-truncated]').hidden = !progress || !progress.truncated;
    putText('[data-update-event-count]', events.length ? '(' + events.length + ')' : '');
    updateModel.logText = events.map(function formatOperationEvent(operationEvent) {
      return new Date(operationEvent.at).toISOString() + ' [' + stageLabel(operationEvent.stage) + '] ' + label(operationEvent.code) + (operationEvent.detail ? ': ' + operationEvent.detail : '');
    }).join('\n');
    if (progress) updateModel.logText = 'SiteBrush ' + progress.from_version + ' → ' + progress.target_version + ' · ' + label(progress.action) + '\n' + (progress.truncated ? label('log_truncated') + '\n' : '') + updateModel.logText;
    const journal = updateRoot.querySelector('[data-update-log]');
    const journalText = updateModel.logText || updateRoot.dataset.logEmpty;
    if (updateModel.renderedLog !== journalText) {
      const following = journal.scrollHeight - journal.scrollTop - journal.clientHeight < 40;
      journal.textContent = journalText;
      updateModel.renderedLog = journalText;
      if (following) journal.scrollTop = journal.scrollHeight;
    }
    updateRoot.querySelector('[data-update-copy]').disabled = !updateModel.logText;
    updateRoot.querySelector('[data-update-download]').disabled = !updateModel.logText;
  }

  function renderAnnouncements() {
    if (!announcementRoot) return;
    const commandBusy = Boolean(updateModel.queuedCommand) || (updateModel.pending && updateModel.pendingAction !== 'status');
    const preview = updateModel.preview && updateModel.preview.Reason === updateModel.reason ? updateModel.preview : null;
    announcementRoot.querySelector('[data-announcement-reason]').value = updateModel.reason;
    announcementRoot.querySelector('[data-announcement-send]').disabled = !preview || commandBusy || !updateModel.connected || updateModel.campaigns.some(function alreadyAnnounced(campaign) { return campaign.Version === preview.Version; });
    announcementRoot.querySelector('[data-announcement-preview]').disabled = commandBusy || !updateModel.connected;
    announcementRoot.querySelector('[data-announcement-recipients]').textContent = preview ? announcementRoot.dataset.recipientCount.replace('%d', String(preview.Recipients)) : '';
    const previewFrame = announcementRoot.querySelector('[data-announcement-email]');
    previewFrame.hidden = !preview;
    if (preview && previewFrame.srcdoc !== preview.HTML) previewFrame.srcdoc = preview.HTML;
    const campaignList = announcementRoot.querySelector('[data-announcement-campaigns]');
    campaignList.replaceChildren();
    updateModel.campaigns.forEach(function renderCampaign(campaign) {
      const campaignRow = document.createElement('li');
      const fields = [campaign.Version, campaign.Sent, campaign.Waiting, campaign.Skipped, campaign.Failed];
      let countIndex = 0;
      campaignRow.textContent = announcementRoot.dataset.campaignCounts.replace(/%[sd]/g, function nextCampaignField() { return String(fields[countIndex++]); });
      if (campaign.LastError) campaignRow.append(document.createTextNode(' — ' + campaign.LastError));
      if (!campaign.Canceled && Date.parse(campaign.ExpiresAt) > Date.now()) {
        const cancelButton = document.createElement('button');
        cancelButton.type = 'button';
        cancelButton.className = 'btn btn-outline-secondary';
        cancelButton.textContent = announcementRoot.querySelector('[data-announcement-cancel-label]').content.textContent;
        cancelButton.disabled = commandBusy || !updateModel.connected;
        cancelButton.addEventListener('click', function cancelCampaign() { submitUpdateCommand('campaign_cancel', { version: campaign.Version }); });
        campaignRow.append(cancelButton);
      }
      campaignList.append(campaignRow);
    });
  }

  function sendUpdateCommand(command) {
    updateModel.pending = true;
    updateModel.pendingAction = command.action;
    updateModel.socket.send(JSON.stringify(command));
    renderUpdate();
  }

  function submitUpdateCommand(actionName, announcementOptions) {
    if (!updateModel.socket || updateModel.socket.readyState !== WebSocket.OPEN) return;
    const command = { action: actionName, csrf: updateRoot.dataset.csrf, manifest_hash: updateModel.state ? updateModel.state.manifest_hash : '' };
    if (announcementOptions) Object.assign(command, announcementOptions);
    if (updateModel.pending) {
      if (actionName !== 'status' && updateModel.pendingAction === 'status' && !updateModel.queuedCommand) {
        updateModel.queuedCommand = command;
        renderUpdate();
      }
      return;
    }
    if (actionName !== 'status') { updateModel.error = ''; putText('[data-update-copy-status]', ''); }
    sendUpdateCommand(command);
  }

  function connectUpdateSocket() {
    if (updateModel.stopped) return;
    const socketAddress = new URL('/?server_update_ws', window.location.href);
    socketAddress.protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
    updateModel.socket = new WebSocket(socketAddress.href);
    updateModel.socket.addEventListener('open', function handleUpdateOpen() {
      updateModel.connected = true;
      submitUpdateCommand('status');
    });
    updateModel.socket.addEventListener('message', function handleUpdateMessage(messageEvent) {
      let updateResponse;
      try { updateResponse = JSON.parse(messageEvent.data); }
      catch (parseError) { updateModel.error = String(parseError); updateModel.pending = false; renderUpdate(); return; }
      updateModel.pending = false;
      updateModel.pendingAction = '';
      updateModel.lastContact = Date.now();
      if (updateResponse.ServerVersion) updateModel.serverVersion = updateResponse.ServerVersion;
      if (!updateModel.serverVersion && updateResponse.State && updateResponse.State.current) updateModel.serverVersion = updateResponse.State.current.version;
      if (updateResponse.Preview) updateModel.preview = updateResponse.Preview;
      if (updateResponse.Campaigns) updateModel.campaigns = updateResponse.Campaigns;
      if (updateResponse.State && updateResponse.State.current && updateResponse.State.current.version) {
        if (updateResponse.State.progress && updateResponse.State.progress.finished_at && updateModel.state && updateModel.state.progress && !updateModel.state.progress.finished_at) updateModel.backupPolicy = {databases: true, analytics: false, files: false, settings: false};
        updateModel.state = updateResponse.State;
      }
      updateModel.error = updateResponse.Error || '';
      renderUpdate();
      if (updateModel.queuedCommand) {
        const queuedCommand = updateModel.queuedCommand;
        updateModel.queuedCommand = null;
        updateModel.error = '';
        sendUpdateCommand(queuedCommand);
      }
    });
    updateModel.socket.addEventListener('close', function handleUpdateClose() {
      updateModel.connected = false;
      updateModel.pending = false;
      updateModel.pendingAction = '';
      updateModel.queuedCommand = null;
      renderUpdate();
      if (!updateModel.stopped) window.setTimeout(connectUpdateSocket, 2000);
    });
    updateModel.socket.addEventListener('error', function handleUpdateError() {
      updateModel.connected = false;
      renderUpdate();
    });
  }

  if (announcementRoot) {
    const reasonSelector = announcementRoot.querySelector('[data-announcement-reason]');
    reasonSelector.addEventListener('change', function changeAnnouncementReason(changeEvent) { updateModel.reason = changeEvent.target.value; updateModel.preview = null; renderUpdate(); });
    announcementRoot.querySelector('[data-announcement-preview]').addEventListener('click', function previewAnnouncement() { submitUpdateCommand('campaign_preview', { reason: updateModel.reason }); });
    announcementRoot.querySelector('[data-announcement-send]').addEventListener('click', function sendAnnouncement() {
      if (!updateModel.preview || updateModel.preview.Reason !== updateModel.reason) return;
      submitUpdateCommand('campaign_start', { reason: updateModel.reason, version: updateModel.preview.Version, manifest_hash: updateModel.preview.ManifestHash });
    });
  }
  for (const groupName of ['databases', 'analytics', 'files', 'settings']) {
    updateRoot.querySelector('[data-backup-group="' + groupName + '"]').addEventListener('change', function changeBackupGroup(changeEvent) { updateModel.backupPolicy[groupName] = changeEvent.target.checked; renderUpdate(); });
  }
  updateRoot.querySelector('[data-update-cancel]').addEventListener('click', function cancelUpdate() { submitUpdateCommand('cancel'); });
  updateRoot.querySelector('[data-update-check]').addEventListener('click', function checkRelease() { submitUpdateCommand('check'); });
  updateRoot.querySelector('[data-update-install]').addEventListener('click', function installRelease() { submitUpdateCommand('upgrade_with_options', {backup_policy: {...updateModel.backupPolicy}}); });
  updateRoot.querySelector('[data-update-rollback]').addEventListener('click', function restoreRelease() { submitUpdateCommand('rollback_with_options', {backup_policy: {...updateModel.backupPolicy}}); });
  updateRoot.querySelector('[data-update-copy]').addEventListener('click', async function copyUpdateLog() {
    try {
      await navigator.clipboard.writeText(updateModel.logText);
      putText('[data-update-copy-status]', label('copied'));
    } catch (copyError) {
      putText('[data-update-copy-status]', label('copy_failed'));
    }
  });
  updateRoot.querySelector('[data-update-download]').addEventListener('click', function downloadUpdateLog() {
    const downloadAddress = URL.createObjectURL(new Blob([updateModel.logText], {type: 'text/plain;charset=utf-8'}));
    const downloadLink = document.createElement('a');
    downloadLink.href = downloadAddress;
    downloadLink.download = 'sitebrush-update-' + (updateModel.state.operation || 'log') + '.txt';
    document.body.append(downloadLink);
    downloadLink.click();
    downloadLink.remove();
    window.setTimeout(function releaseDownloadAddress() { URL.revokeObjectURL(downloadAddress); }, 1000);
  });
  const clockTimer = window.setInterval(function updateOperationClock() {
    const progress = updateModel.state && updateModel.state.progress;
    renderTimes(progress && progress.action ? progress : null);
  }, 1000);
  const pollTimer = window.setInterval(function pollUpdateStatus() { submitUpdateCommand('status'); }, 2000);
  window.addEventListener('pagehide', function closeUpdateSubscription() { updateModel.stopped = true; window.clearInterval(pollTimer); window.clearInterval(clockTimer); if (updateModel.socket) updateModel.socket.close(); });
  renderUpdate();
  connectUpdateSocket();
})();
