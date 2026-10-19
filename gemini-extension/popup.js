document.addEventListener('DOMContentLoaded', () => {
  const statusBadge = document.getElementById('status-badge');
  const statusText = document.getElementById('status-text');
  const lastSync = document.getElementById('last-sync');
  const serverHost = document.getElementById('server-host');
  const syncBtn = document.getElementById('sync-btn');
  const copyBtn = document.getElementById('copy-btn');
  const saveBtn = document.getElementById('save-btn');
  const hostInput = document.getElementById('host-input');
  const tokenInput = document.getElementById('token-input');
  const remoteCheck = document.getElementById('remote-check');
  const noticeBox = document.getElementById('notice');

  let hostPrefilled = false;

  function showNotice(text) {
    if (!text) {
      noticeBox.classList.add('hidden');
      noticeBox.innerText = '';
      return;
    }
    noticeBox.innerText = text;
    noticeBox.classList.remove('hidden');
  }

  function updateUI() {
    chrome.runtime.sendMessage({ type: 'GET_STATUS' }, (response) => {
      if (chrome.runtime.lastError) return;
      if (!response) return;

      if (response.connected) {
        statusBadge.className = 'badge connected';
        statusText.innerText = 'Paired';
        statusText.style.color = '#10b981';
      } else if (!response.tokenSet) {
        statusBadge.className = 'badge disconnected';
        statusText.innerText = 'Token required';
        statusText.style.color = '#f59e0b';
      } else if (!response.verified) {
        statusBadge.className = 'badge disconnected';
        statusText.innerText = 'Server not verified';
        statusText.style.color = '#f59e0b';
      } else {
        statusBadge.className = 'badge disconnected';
        statusText.innerText = 'Disconnected';
        statusText.style.color = '#ef4444';
      }

      serverHost.innerText = (response.configuredHost || '—') + (response.allowRemoteHost ? ' (non-local allowed)' : '');

      if (!hostPrefilled && response.configuredHost) {
        hostInput.value = response.configuredHost;
        hostPrefilled = true;
      }
      remoteCheck.checked = !!response.allowRemoteHost;
      tokenInput.placeholder = response.tokenSet ? '•••••••• (saved)' : 'not configured';

      if (response.lastSyncTime) {
        lastSync.innerText = new Date(response.lastSyncTime).toLocaleTimeString();
      } else {
        lastSync.innerText = 'Never';
      }

      showNotice(response.error || (!response.tokenSet
        ? 'Paste the bridge token your server prints at startup, then press Save Connection.'
        : ''));
    });
  }

  updateUI();

  chrome.runtime.onMessage.addListener((msg) => {
    if (msg.type === 'SYNC_UPDATE') updateUI();
  });

  saveBtn.addEventListener('click', () => {
    saveBtn.disabled = true;
    saveBtn.innerText = 'Saving...';
    chrome.runtime.sendMessage(
      {
        type: 'SET_CONFIG',
        host: hostInput.value,
        token: tokenInput.value,
        allowRemoteHost: remoteCheck.checked
      },
      (response) => {
        saveBtn.disabled = false;
        saveBtn.innerText = '💾 Save Connection';
        if (response && response.ok) {
          tokenInput.value = '';
          showNotice('');
        } else if (response && response.error) {
          showNotice(response.error);
        }
        updateUI();
      }
    );
  });

  syncBtn.addEventListener('click', () => {
    syncBtn.disabled = true;
    syncBtn.innerText = 'Syncing...';
    chrome.runtime.sendMessage({ type: 'FORCE_SYNC' }, (response) => {
      setTimeout(() => {
        syncBtn.disabled = false;
        syncBtn.innerText = 'Force Sync Cookies';
        if (response && response.error) showNotice(response.error);
        updateUI();
      }, 800);
    });
  });

  copyBtn.addEventListener('click', () => {
    copyBtn.disabled = true;
    copyBtn.innerText = 'Extracting...';

    chrome.runtime.sendMessage({ type: 'GET_FORMATTED_COOKIES' }, async (response) => {
      if (chrome.runtime.lastError || !response || !response.cookies || response.cookies.length === 0) {
        copyBtn.innerText = '⚠️ No Cookies Found';
        setTimeout(() => {
          copyBtn.disabled = false;
          copyBtn.innerText = '📋 Copy Cookies';
        }, 2000);
        return;
      }

      const jsonStr = JSON.stringify(response.cookies, null, 2);
      try {
        await navigator.clipboard.writeText(jsonStr);
        copyBtn.className = 'btn btn-copy success';
        copyBtn.innerText = `✅ Copied (${response.cookies.length} Cookies)!`;
      } catch (err) {
        copyBtn.innerText = '❌ Copy Failed';
      }

      setTimeout(() => {
        copyBtn.className = 'btn btn-copy';
        copyBtn.disabled = false;
        copyBtn.innerText = '📋 Copy Cookies';
      }, 2200);
    });
  });
});
