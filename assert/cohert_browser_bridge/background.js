importScripts("config.js");

let ws = null;
let lastStatus = {
  connected: false,
  wsUrl: COHERT_BRIDGE_CONFIG.wsUrl,
  lastError: "",
  tabCount: 0
};

const isScriptable = (url) => typeof url === "string" && /^https?:\/\//i.test(url);

function updateStatus(patch) {
  lastStatus = { ...lastStatus, ...patch };
}

function scheduleProbe() {
  chrome.alarms.create(COHERT_BRIDGE_CONFIG.probeAlarmName, {
    delayInMinutes: COHERT_BRIDGE_CONFIG.probeDelayMinutes
  });
}

function scheduleKeepalive() {
  chrome.alarms.create(COHERT_BRIDGE_CONFIG.keepaliveAlarmName, {
    delayInMinutes: COHERT_BRIDGE_CONFIG.keepaliveDelayMinutes
  });
}

function safeSend(payload) {
  if (!ws || ws.readyState !== WebSocket.OPEN) return false;
  ws.send(JSON.stringify(payload));
  return true;
}

async function listTabs() {
  const tabs = await chrome.tabs.query({});
  return tabs
    .filter((tab) => isScriptable(tab.url))
    .map((tab) => ({
      id: String(tab.id),
      title: tab.title || "",
      url: tab.url || "",
      active: !!tab.active,
      windowId: tab.windowId
    }));
}

async function resolveTabId(tabId) {
  if (tabId !== undefined && tabId !== null && String(tabId).trim() !== "") {
    return Number(tabId);
  }
  const [active] = await chrome.tabs.query({ active: true, currentWindow: true });
  if (!active || !isScriptable(active.url)) {
    throw new Error("no active scriptable http/https tab");
  }
  return active.id;
}

function truncateText(text, maxChars) {
  const value = String(text ?? "");
  const limit = Number(maxChars) > 0 ? Number(maxChars) : COHERT_BRIDGE_CONFIG.maxScanChars;
  if (value.length <= limit) {
    return { text: value, truncated: false, charCount: value.length, omitted: 0 };
  }
  return {
    text: value.slice(0, limit),
    truncated: true,
    charCount: value.length,
    omitted: value.length - limit
  };
}

async function scanTab(request) {
  const tabId = await resolveTabId(request.tab_id || request.tabId);
  const tab = await chrome.tabs.get(tabId);
  if (!isScriptable(tab.url)) {
    throw new Error("tab is not scriptable: " + (tab.url || ""));
  }

  const [{ result }] = await chrome.scripting.executeScript({
    target: { tabId },
    world: "MAIN",
    func: () => {
      const text = document.body ? document.body.innerText || "" : "";
      return {
        title: document.title || "",
        url: location.href,
        text
      };
    }
  });

  const clipped = truncateText(result?.text || "", request.max_chars || request.maxChars);
  return {
    status: "success",
    tab_id: String(tabId),
    title: result?.title || tab.title || "",
    url: result?.url || tab.url || "",
    text: clipped.text,
    truncated: clipped.truncated,
    char_count: clipped.charCount,
    omitted: clipped.omitted
  };
}

function serializeJsValue(value) {
  if (value === undefined || value === null) return value;
  if (typeof value !== "object") return value;
  try {
    return JSON.parse(JSON.stringify(value, (_key, current) => {
      if (!current || typeof current !== "object") return current;
      if (current.nodeType === 1) return current.outerHTML;
      if (current === window || current === document) return "[Object]";
      return current;
    }));
  } catch (err) {
    return "[unserializable: " + err.message + "]";
  }
}

function buildPageEvalScript(script) {
  return `(async () => {
    const AsyncFunction = Object.getPrototypeOf(async function(){}).constructor;
    const source = ${JSON.stringify(String(script || ""))};
    const serialize = ${serializeJsValue.toString()};
    try {
      const result = await (new AsyncFunction(source))();
      return { ok: true, value: serialize(result) };
    } catch (err) {
      return {
        ok: false,
        error: {
          name: err.name || "Error",
          message: err.message || String(err),
          stack: err.stack || ""
        }
      };
    }
  })()`;
}

async function executeJs(request) {
  const tabId = await resolveTabId(request.tab_id || request.tabId);
  const script = request.script || request.code || "";
  if (!String(script).trim()) {
    throw new Error("script is required");
  }

  const before = await chrome.tabs.get(tabId);
  const [{ result }] = await chrome.scripting.executeScript({
    target: { tabId },
    world: "MAIN",
    func: async (wrapped) => await eval(wrapped),
    args: [buildPageEvalScript(script)]
  });
  const after = await chrome.tabs.get(tabId);

  if (!result?.ok) {
    return {
      status: "error",
      tab_id: String(tabId),
      error: result?.error || { message: "unknown JavaScript execution error" }
    };
  }

  const clipped = truncateText(
    typeof result.value === "string" ? result.value : JSON.stringify(result.value),
    request.max_return_chars || request.maxReturnChars || COHERT_BRIDGE_CONFIG.maxJsReturnChars
  );
  return {
    status: "success",
    tab_id: String(tabId),
    return: clipped.text,
    truncated: clipped.truncated,
    diff: buildLightDiff(before, after)
  };
}

function buildLightDiff(before, after) {
  const changes = [];
  if ((before.url || "") !== (after.url || "")) changes.push("url changed");
  if ((before.title || "") !== (after.title || "")) changes.push("title changed");
  if (changes.length === 0) return "url and title unchanged";
  return changes.join(", ");
}

async function handleCommand(message) {
  const command = message.command || message.cmd;
  if (command === "tabs") {
    return { status: "success", tabs: await listTabs() };
  }
  if (command === "scan") {
    return await scanTab(message);
  }
  if (command === "execute_js") {
    return await executeJs(message);
  }
  throw new Error("unknown command: " + command);
}

function sendResult(id, result) {
  safeSend({ type: "result", id, result });
}

function sendError(id, error) {
  safeSend({
    type: "error",
    id,
    error: {
      message: error?.message || String(error),
      stack: error?.stack || ""
    }
  });
}

async function sendReady(type) {
  const tabs = await listTabs();
  updateStatus({ tabCount: tabs.length });
  safeSend({
    type,
    name: "Cohert Browser Bridge",
    version: "0.1.0",
    tabs
  });
}

function connectWS() {
  if (ws && ws.readyState <= WebSocket.OPEN) return;
  try {
    ws = new WebSocket(COHERT_BRIDGE_CONFIG.wsUrl);
  } catch (err) {
    updateStatus({ connected: false, lastError: err.message || String(err) });
    ws = null;
    scheduleProbe();
    return;
  }

  ws.onopen = async () => {
    updateStatus({ connected: true, lastError: "" });
    scheduleKeepalive();
    await sendReady("ext_ready");
  };

  ws.onmessage = async (event) => {
    let payload;
    try {
      payload = JSON.parse(event.data);
    } catch (err) {
      sendError("", err);
      return;
    }
    if (!payload.id) return;
    try {
      sendResult(payload.id, await handleCommand(payload));
    } catch (err) {
      sendError(payload.id, err);
    }
  };

  ws.onclose = () => {
    updateStatus({ connected: false });
    ws = null;
    scheduleProbe();
  };

  ws.onerror = () => {
    updateStatus({ connected: false, lastError: "websocket error" });
  };
}

async function sendTabsUpdate() {
  if (!ws || ws.readyState !== WebSocket.OPEN) return;
  await sendReady("tabs_update");
}

chrome.alarms.onAlarm.addListener((alarm) => {
  if (alarm.name === COHERT_BRIDGE_CONFIG.keepaliveAlarmName) {
    if (safeSend({ type: "ping" })) {
      scheduleKeepalive();
    } else {
      scheduleProbe();
    }
  }
  if (alarm.name === COHERT_BRIDGE_CONFIG.probeAlarmName) {
    connectWS();
  }
});

chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
  (async () => {
    if (message?.cmd === "status") {
      sendResponse({ ok: true, data: lastStatus });
      return;
    }
    if (message?.cmd === "tabs") {
      sendResponse({ ok: true, data: await listTabs() });
      return;
    }
    if (message?.cmd === "reconnect") {
      connectWS();
      sendResponse({ ok: true });
      return;
    }
    sendResponse({ ok: false, error: "unknown popup command" });
  })();
  return true;
});

chrome.runtime.onInstalled.addListener(() => connectWS());
chrome.runtime.onStartup.addListener(() => connectWS());
chrome.tabs.onUpdated.addListener((_tabId, changeInfo) => {
  if (changeInfo.status === "complete") sendTabsUpdate();
});
chrome.tabs.onCreated.addListener(() => sendTabsUpdate());
chrome.tabs.onRemoved.addListener(() => sendTabsUpdate());

connectWS();
