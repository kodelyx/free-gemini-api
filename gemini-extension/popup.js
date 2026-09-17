document.addEventListener('DOMContentLoaded', () => {
  const statusBadge = document.getElementById('status-badge');
  const statusText = document.getElementById('status-text');
  const lastSync = document.getElementById('last-sync');
  const syncBtn = document.getElementById('sync-btn');

  const serverHostInput = document.getElementById('server-host-input');
  const saveHostBtn = document.getElementById('save-host-btn');
  const chips = document.querySelectorAll('.chip');

  function updateUI() {
    chrome.runtime.sendMessage({ type: 'GET_STATUS' }, (response) => {
      if (chrome.runtime.lastError) return;
      if (!response) return;

      if (response.connected) {
        statusBadge.className = 'badge connected';
        statusText.innerText = 'Connected';
        statusText.style.color = '#10b981';
      } else {
        statusBadge.className = 'badge disconnected';
        statusText.innerText = 'Disconnected';
        statusText.style.color = '#ef4444';
      }

      if (response.lastSyncTime) {
        const date = new Date(response.lastSyncTime);
        lastSync.innerText = date.toLocaleTimeString();
      } else {
        lastSync.innerText = 'Never';
      }

      if (response.serverHost && document.activeElement !== serverHostInput) {
        serverHostInput.value = response.serverHost;
      }
    });
  }

  function saveHost(hostValue) {
    const host = (hostValue || serverHostInput.value || '127.0.0.1').trim();
    serverHostInput.value = host;
    saveHostBtn.disabled = true;
    chrome.runtime.sendMessage({ type: 'SET_SERVER_HOST', host }, () => {
      saveHostBtn.className = 'btn-save saved';
      saveHostBtn.innerText = 'Saved!';
      setTimeout(() => {
        saveHostBtn.className = 'btn-save';
        saveHostBtn.innerText = 'Save';
        saveHostBtn.disabled = false;
        updateUI();
      }, 1000);
    });
  }

  saveHostBtn.addEventListener('click', () => saveHost());
  serverHostInput.addEventListener('keydown', (e) => {
    if (e.key === 'Enter') saveHost();
  });

  chips.forEach(chip => {
    chip.addEventListener('click', () => {
      const ip = chip.getAttribute('data-ip');
      if (ip) {
        serverHostInput.value = ip;
        saveHost(ip);
      }
    });
  });

  // Initial update
  updateUI();

  // Listen for real-time updates from background service worker
  chrome.runtime.onMessage.addListener((msg) => {
    if (msg.type === 'SYNC_UPDATE') {
      updateUI();
    }
  });

  const copyBtn = document.getElementById('copy-btn');

  syncBtn.addEventListener('click', () => {
    syncBtn.disabled = true;
    syncBtn.innerText = 'Syncing...';
    chrome.runtime.sendMessage({ type: 'FORCE_SYNC' }, () => {
      setTimeout(() => {
        syncBtn.disabled = false;
        syncBtn.innerText = 'Force Sync Cookies';
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
        copyBtn.className = 'btn-copy success';
        copyBtn.innerText = `✅ Copied (${response.cookies.length} Cookies)!`;
      } catch (err) {
        copyBtn.innerText = '❌ Copy Failed';
      }

      setTimeout(() => {
        copyBtn.className = 'btn-copy';
        copyBtn.disabled = false;
        copyBtn.innerText = '📋 Copy Cookies';
      }, 2200);
    });
  });
});
