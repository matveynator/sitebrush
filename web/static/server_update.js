(function initializeServerUpdate() {
  'use strict';
  const updateRoot = document.querySelector('[data-server-update]');
  if (!updateRoot) return;
  const announcementRoot = updateRoot.querySelector('[data-announcements]');
  const updateModel = { connected: false, state: null, error: '', pending: false, socket: null, stopped: false, preview: null, campaigns: [], reason: 'security' };
  const stageLabels = Object.fromEntries(Array.from(updateRoot.querySelectorAll('[data-stage]'), function readStageLabel(stageElement) {
    return [stageElement.dataset.stage, stageElement.textContent];
  }));

  function renderUpdate() {
    const updateState = updateModel.state;
    const operationBusy = updateModel.pending || Boolean(updateState && updateState.busy);
    updateRoot.querySelector('[data-update-current]').textContent = updateState ? updateState.current.version : '—';
    updateRoot.querySelector('[data-update-available]').textContent = updateState && updateState.available ? updateState.available.version : '—';
    updateRoot.querySelector('[data-update-status]').textContent = updateModel.connected && updateState ? (stageLabels[updateState.stage] || updateRoot.dataset.waiting) : updateRoot.dataset.waiting;
    const errorElement = updateRoot.querySelector('[data-update-error]');
    errorElement.textContent = updateModel.error;
    errorElement.hidden = !updateModel.error;
    updateRoot.querySelector('[data-update-check]').disabled = !updateModel.connected || operationBusy;
    const newerRelease = updateState && updateState.available && /^\d+$/.test(updateState.current.version) && BigInt(updateState.available.version) > BigInt(updateState.current.version);
    updateRoot.querySelector('[data-update-install]').disabled = !updateModel.connected || operationBusy || !newerRelease;
    updateRoot.querySelector('[data-update-rollback]').disabled = !updateModel.connected || operationBusy || !updateState || !updateState.rollback_available;
    updateRoot.querySelector('[data-update-rollback-reason]').hidden = !updateState || !updateState.previous || updateState.rollback_available;
    updateRoot.querySelector('[data-update-notification-error]').hidden = !updateState || !updateState.notification_error;
    renderAnnouncements();
  }

  function renderAnnouncements() {
    if (!announcementRoot) return;
    const preview = updateModel.preview && updateModel.preview.Reason === updateModel.reason ? updateModel.preview : null;
    announcementRoot.querySelector('[data-announcement-reason]').value = updateModel.reason;
    announcementRoot.querySelector('[data-announcement-send]').disabled = !preview || updateModel.pending || !updateModel.connected || updateModel.campaigns.some(function alreadyAnnounced(campaign) { return campaign.Version === preview.Version; });
    announcementRoot.querySelector('[data-announcement-preview]').disabled = updateModel.pending || !updateModel.connected;
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
        cancelButton.disabled = updateModel.pending || !updateModel.connected;
        cancelButton.addEventListener('click', function cancelCampaign() { submitUpdateCommand('campaign_cancel', { version: campaign.Version }); });
        campaignRow.append(cancelButton);
      }
      campaignList.append(campaignRow);
    });
  }

  function submitUpdateCommand(actionName, announcementOptions) {
    if (!updateModel.socket || updateModel.socket.readyState !== WebSocket.OPEN || updateModel.pending) return;
    updateModel.pending = true;
    if (actionName !== 'status') updateModel.error = '';
    const command = { action: actionName, csrf: updateRoot.dataset.csrf, manifest_hash: updateModel.state ? updateModel.state.manifest_hash : '' };
    if (announcementOptions) Object.assign(command, announcementOptions);
    updateModel.socket.send(JSON.stringify(command));
    renderUpdate();
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
      if (updateResponse.Preview) updateModel.preview = updateResponse.Preview;
      if (updateResponse.Campaigns) updateModel.campaigns = updateResponse.Campaigns;
      if (updateResponse.State && updateResponse.State.current && updateResponse.State.current.version) updateModel.state = updateResponse.State;
      updateModel.error = updateResponse.Error || (updateModel.state && updateModel.state.error) || '';
      renderUpdate();
    });
    updateModel.socket.addEventListener('close', function handleUpdateClose() {
      updateModel.connected = false;
      updateModel.pending = false;
      renderUpdate();
      if (!updateModel.stopped) window.setTimeout(connectUpdateSocket, 2000);
    });
    updateModel.socket.addEventListener('error', function handleUpdateError() {
      updateModel.error = updateRoot.dataset.unavailable;
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
  updateRoot.querySelector('[data-update-check]').addEventListener('click', function checkRelease() { submitUpdateCommand('check'); });
  updateRoot.querySelector('[data-update-install]').addEventListener('click', function installRelease() { submitUpdateCommand('upgrade'); });
  updateRoot.querySelector('[data-update-rollback]').addEventListener('click', function restoreRelease() { submitUpdateCommand('rollback'); });
  const pollTimer = window.setInterval(function pollUpdateStatus() { submitUpdateCommand('status'); }, 2000);
  window.addEventListener('pagehide', function closeUpdateSubscription() { updateModel.stopped = true; window.clearInterval(pollTimer); if (updateModel.socket) updateModel.socket.close(); });
  renderUpdate();
  connectUpdateSocket();
})();
