// MV3 的 background 是 service worker 环境，不能直接用普通 HTML 的 <script>。
// importScripts 会把 config.js 加载进同一个 worker 作用域，里面的配置挂在 self.COHERT_BRIDGE_CONFIG 上。
importScripts("config.js");

// ws 保存插件主动连到 Cohert Go 进程的 WebSocket 连接。
// 这条连接是整个浏览器桥的主链路：Go 发命令，插件执行，再把结果回传。
let ws = null;

// popup 不能直接读 background 的局部变量，所以 popup 会通过 chrome.runtime.sendMessage
// 询问 background 当前状态。lastStatus 就是给 popup 展示用的轻量状态快照。
let lastStatus = {
  connected: false,
  wsUrl: COHERT_BRIDGE_CONFIG.wsUrl,
  lastError: "",
  tabCount: 0
};

// Chrome 内部页面、扩展页面、文件页面都不能稳定执行脚本。
// 第一版只处理 http/https，避免 browser_scan 对 chrome://extensions 这类页面报权限错误。
const isScriptable = (url) => typeof url === "string" && /^https?:\/\//i.test(url);

function updateStatus(patch) {
  lastStatus = { ...lastStatus, ...patch };
}

function scheduleProbe() {
  // MV3 service worker 会被 Chrome 挂起，不能依赖 setInterval 长期运行。
  // chrome.alarms 是扩展里更稳定的定时唤醒方式，这里用于定期重连 Cohert。
  chrome.alarms.create(COHERT_BRIDGE_CONFIG.probeAlarmName, {
    delayInMinutes: COHERT_BRIDGE_CONFIG.probeDelayMinutes
  });
}

function scheduleKeepalive() {
  // WebSocket 连接建立后，定期发 ping，既能检查连接是否还活着，也能降低 worker 被挂起的概率。
  chrome.alarms.create(COHERT_BRIDGE_CONFIG.keepaliveAlarmName, {
    delayInMinutes: COHERT_BRIDGE_CONFIG.keepaliveDelayMinutes
  });
}

function safeSend(payload) {
  // 所有写 WebSocket 的地方都走这里，避免连接未建立时直接 ws.send 抛异常。
  if (!ws || ws.readyState !== WebSocket.OPEN) return false;
  ws.send(JSON.stringify(payload));
  return true;
}

async function listTabs() {
  // chrome.tabs.query({}) 会拿到当前 Chrome 里所有窗口的所有标签页。
  // 这里后续会成为 Cohert 的 browser_tabs 工具数据源。
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
  // Go 侧指定 tab_id 时优先使用指定 tab；不指定时默认使用当前窗口的 active tab。
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
  // 浏览器页面文本通常很长，必须在插件层先截断一次。
  // 后续 Context Manager 还会兜底压缩工具结果，但工具自身不能无限返回。
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
  // browser_scan 的第一版实现：找到目标 tab，在真实页面上下文里读取 title/url/body.innerText。
  const tabId = await resolveTabId(request.tab_id || request.tabId);
  const tab = await chrome.tabs.get(tabId);
  if (!isScriptable(tab.url)) {
    throw new Error("tab is not scriptable: " + (tab.url || ""));
  }

  const [{ result }] = await chrome.scripting.executeScript({
    target: { tabId },
    // MAIN 表示脚本在页面主世界执行，能看到页面自己的 window/document。
    // 这里读取的是最终渲染后的页面，不是原始 HTML 响应。
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
  // JS 执行结果可能是 DOM 节点、window、document、复杂对象。
  // 这些对象不能直接 JSON.stringify，所以这里把常见不可序列化对象转成字符串。
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
  // chrome.scripting.executeScript 只能传函数和参数。
  // 用户脚本会被包进一个 async function 里执行，所以需要返回值时建议显式写 return。
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
  // browser_execute_js 的第一版实现：在目标 tab 里执行一段读取型 JS，并把结果截断后回传。
  // 高风险操作确认不应该放在插件层做，后续应放在 Cohert 工具层统一判断。
  const tabId = await resolveTabId(request.tab_id || request.tabId);
  const script = request.script || request.code || "";
  if (!String(script).trim()) {
    throw new Error("script is required");
  }

  const before = await chrome.tabs.get(tabId);
  const [{ result }] = await chrome.scripting.executeScript({
    target: { tabId },
    world: "MAIN",
    // 这里的 eval 不是执行用户原始脚本，而是执行 buildPageEvalScript 生成的包装脚本。
    // 包装脚本会捕获异常并把返回值做序列化，避免错误直接炸掉 background。
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
  // 第一版只做轻量 diff：URL 和标题是否变化。
  // 后续可以扩展成正文长度、按钮数量、新 tab 等更细的页面变化摘要。
  const changes = [];
  if ((before.url || "") !== (after.url || "")) changes.push("url changed");
  if ((before.title || "") !== (after.title || "")) changes.push("title changed");
  if (changes.length === 0) return "url and title unchanged";
  return changes.join(", ");
}

async function handleCommand(message) {
  // Cohert Go 侧发来的命令在这里统一分发。
  // 协议保持小而稳定：tabs / scan / execute_js。
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
  // 用 request id 把响应和请求配对，Go 侧可以并发发多个请求。
  safeSend({ type: "result", id, result });
}

function sendError(id, error) {
  // 错误也必须带 request id，Go 侧才能知道是哪一次 browser 命令失败。
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
  // ext_ready：插件刚连上 Cohert。
  // tabs_update：标签页发生变化。
  // 两者都附带当前 tab 快照，让 Go 侧不用每次主动拉取。
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
  // 避免重复连接：CONNECTING(0) 或 OPEN(1) 都说明当前已有连接在工作。
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
    // 连接建立后，立刻把插件身份和当前 tabs 发给 Cohert。
    // Go 侧收到 ext_ready 后，就知道浏览器桥已经可用了。
    updateStatus({ connected: true, lastError: "" });
    scheduleKeepalive();
    await sendReady("ext_ready");
  };

  ws.onmessage = async (event) => {
    // Cohert 发来的消息格式大致是：
    // { "id": "...", "command": "tabs|scan|execute_js", ... }
    let payload;
    try {
      payload = JSON.parse(event.data);
    } catch (err) {
      sendError("", err);
      return;
    }
    if (!payload.id) return;
    try {
      // 执行命令成功时回 result，失败时回 error；两者都保留同一个 id。
      sendResult(payload.id, await handleCommand(payload));
    } catch (err) {
      sendError(payload.id, err);
    }
  };

  ws.onclose = () => {
    // Cohert 进程退出、端口没监听或网络断开时会走这里。
    // 插件不报死错，而是切回 probe 模式，等待 Go 侧重新启动。
    updateStatus({ connected: false });
    ws = null;
    scheduleProbe();
  };

  ws.onerror = () => {
    // WebSocket error 只记录状态，真正的重连由 onclose/probe 处理。
    updateStatus({ connected: false, lastError: "websocket error" });
  };
}

async function sendTabsUpdate() {
  // tab 创建、关闭、加载完成后，把最新 tab 快照推给 Cohert。
  if (!ws || ws.readyState !== WebSocket.OPEN) return;
  await sendReady("tabs_update");
}

chrome.alarms.onAlarm.addListener((alarm) => {
  // keepalive：连接活着就 ping；发不出去说明连接失效，切回重连探测。
  if (alarm.name === COHERT_BRIDGE_CONFIG.keepaliveAlarmName) {
    if (safeSend({ type: "ping" })) {
      scheduleKeepalive();
    } else {
      scheduleProbe();
    }
  }
  // probe：未连接时周期性尝试 connectWS。
  if (alarm.name === COHERT_BRIDGE_CONFIG.probeAlarmName) {
    connectWS();
  }
});

chrome.runtime.onMessage.addListener((message, _sender, sendResponse) => {
  // popup.js 不能直接访问 background 的局部变量，所以通过 runtime message 问状态。
  // return true 表示 sendResponse 会异步调用，这是 Chrome 扩展消息机制的要求。
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

// 插件安装、Chrome 启动时都主动连接 Cohert。
chrome.runtime.onInstalled.addListener(() => connectWS());
chrome.runtime.onStartup.addListener(() => connectWS());

// 标签页变化时主动通知 Cohert，避免 Go 侧拿到过期 tab 列表。
chrome.tabs.onUpdated.addListener((_tabId, changeInfo) => {
  if (changeInfo.status === "complete") sendTabsUpdate();
});
chrome.tabs.onCreated.addListener(() => sendTabsUpdate());
chrome.tabs.onRemoved.addListener(() => sendTabsUpdate());

// service worker 首次加载时立即尝试连接。
connectWS();
