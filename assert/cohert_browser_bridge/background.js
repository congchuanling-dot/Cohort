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

async function openTab(request) {
  // browser_open 的插件侧实现。
  // 没传 tab_id 时新建标签页；传了 tab_id 时复用已有标签页导航到目标 URL。
  const targetURL = String(request.url || "").trim();
  if (!/^https?:\/\//i.test(targetURL)) {
    throw new Error("open requires an absolute http/https URL");
  }

  const active = request.active !== false;
  let tab;
  const rawTabID = request.tab_id || request.tabId;
  if (rawTabID !== undefined && rawTabID !== null && String(rawTabID).trim() !== "") {
    tab = await chrome.tabs.update(Number(rawTabID), { url: targetURL, active });
  } else {
    tab = await chrome.tabs.create({ url: targetURL, active });
  }

  tab = await waitForTabComplete(tab.id, 8000);
  return {
    status: "success",
    tab_id: String(tab.id),
    title: tab.title || "",
    url: tab.url || targetURL
  };
}

async function waitForTabComplete(tabId, timeoutMs) {
  // tabs.create/tabs.update 返回时页面往往还没加载完。
  // 这里最多等 8 秒，能让 browser_open 后紧接 browser_scan 的天气查询链路更稳定。
  const start = Date.now();
  while (Date.now() - start < timeoutMs) {
    const tab = await chrome.tabs.get(tabId);
    if (tab.status === "complete") return tab;
    await new Promise((resolve) => setTimeout(resolve, 250));
  }
  return await chrome.tabs.get(tabId);
}

function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function normalizeWaitTiming(request) {
  // wait 工具必须有硬上限，避免模型给出过大的 timeout 后长期占用 bridge 请求。
  const timeoutMs = Math.min(Math.max(Number(request.timeout_ms || request.timeoutMs || 10000), 500), 30000);
  const intervalMs = Math.min(Math.max(Number(request.interval_ms || request.intervalMs || 200), 100), 1000);
  return { timeoutMs, intervalMs };
}

async function readPageState(tabId) {
  // 读取页面的轻量状态。失败时返回 unknown，让 wait 继续轮询而不是过早失败。
  try {
    const [{ result }] = await chrome.scripting.executeScript({
      target: { tabId },
      world: "MAIN",
      func: () => ({
        readyState: document.readyState || "",
        url: location.href,
        title: document.title || "",
        textLength: document.body ? (document.body.innerText || "").length : 0,
        interactiveCount: document.querySelectorAll("a,button,input,textarea,select,[role=button],[contenteditable=true]").length
      })
    });
    return result || { readyState: "unknown" };
  } catch (_err) {
    return { readyState: "unknown" };
  }
}

async function waitForCondition(tabId, timing, check) {
  const start = Date.now();
  let last = null;
  while (Date.now() - start <= timing.timeoutMs) {
    last = await check();
    if (last && last.matched) {
      return {
        status: "success",
        tab_id: String(tabId),
        elapsed_ms: Date.now() - start,
        ...last
      };
    }
    await sleep(timing.intervalMs);
  }
  return {
    status: "timeout",
    tab_id: String(tabId),
    elapsed_ms: Date.now() - start,
    ...(last || { matched: false })
  };
}

async function waitForLoad(request) {
  const tabId = await resolveTabId(request.tab_id || request.tabId);
  const timing = normalizeWaitTiming(request);
  return await waitForCondition(tabId, timing, async () => {
    const tab = await chrome.tabs.get(tabId);
    const page = await readPageState(tabId);
    const readyState = page.readyState || "unknown";
    const matched = tab.status === "complete" && (readyState === "interactive" || readyState === "complete");
    return {
      mode: "load",
      matched,
      ready_state: readyState,
      tab_status: tab.status || "",
      url: page.url || tab.url || "",
      title: page.title || tab.title || ""
    };
  });
}

async function waitForSelector(request) {
  const tabId = await resolveTabId(request.tab_id || request.tabId);
  const selector = String(request.selector || "").trim();
  if (!selector) {
    throw new Error("selector is required");
  }
  const state = String(request.state || "visible").trim();
  const allowed = new Set(["attached", "visible", "hidden", "detached"]);
  if (!allowed.has(state)) {
    throw new Error("unsupported selector state: " + state);
  }
  const timing = normalizeWaitTiming(request);
  return await waitForCondition(tabId, timing, async () => {
    const [{ result }] = await chrome.scripting.executeScript({
      target: { tabId },
      world: "MAIN",
      args: [selector],
      func: (selectorArg) => {
        const element = document.querySelector(selectorArg);
        if (!element) {
          return { exists: false, visible: false };
        }
        const rect = element.getBoundingClientRect();
        const style = getComputedStyle(element);
        const visible = rect.width > 0
          && rect.height > 0
          && style.display !== "none"
          && style.visibility !== "hidden"
          && Number(style.opacity || "1") > 0;
        return {
          exists: true,
          visible,
          rect: {
            x: rect.x,
            y: rect.y,
            width: rect.width,
            height: rect.height,
            top: rect.top,
            right: rect.right,
            bottom: rect.bottom,
            left: rect.left
          }
        };
      }
    });
    const exists = !!result?.exists;
    const visible = !!result?.visible;
    return {
      mode: "selector",
      selector,
      state,
      matched: (state === "attached" && exists)
        || (state === "visible" && visible)
        || (state === "hidden" && exists && !visible)
        || (state === "detached" && !exists),
      exists,
      visible,
      rect: result?.rect || null
    };
  });
}

async function waitForText(request) {
  const tabId = await resolveTabId(request.tab_id || request.tabId);
  const text = String(request.text || "").trim();
  if (!text) {
    throw new Error("text is required");
  }
  const timing = normalizeWaitTiming(request);
  return await waitForCondition(tabId, timing, async () => {
    const [{ result }] = await chrome.scripting.executeScript({
      target: { tabId },
      world: "MAIN",
      args: [text],
      func: (targetText) => {
        const bodyText = document.body ? document.body.innerText || "" : "";
        return {
          matched: bodyText.includes(targetText),
          textLength: bodyText.length
        };
      }
    });
    return {
      mode: "text",
      text,
      matched: !!result?.matched,
      text_length: result?.textLength || 0
    };
  });
}

async function waitForStable(request) {
  const tabId = await resolveTabId(request.tab_id || request.tabId);
  const timing = normalizeWaitTiming(request);
  const stableMs = Math.min(Math.max(Number(request.stable_ms || request.stableMs || 800), 300), 5000);
  let stableSince = 0;
  let previousKey = "";
  return await waitForCondition(tabId, timing, async () => {
    const tab = await chrome.tabs.get(tabId);
    const page = await readPageState(tabId);
    const key = [
      tab.status || "",
      page.readyState || "",
      page.url || tab.url || "",
      page.title || tab.title || "",
      page.textLength,
      page.interactiveCount
    ].join("|");
    const now = Date.now();
    if (key !== previousKey) {
      previousKey = key;
      stableSince = now;
    }
    const stableFor = now - stableSince;
    const matched = stableFor >= stableMs
      && tab.status === "complete"
      && (page.readyState === "interactive" || page.readyState === "complete");
    return {
      mode: "stable",
      matched,
      stable_ms: stableMs,
      stable_for_ms: stableFor,
      ready_state: page.readyState || "unknown",
      tab_status: tab.status || "",
      url: page.url || tab.url || "",
      title: page.title || tab.title || "",
      text_length: page.textLength ?? -1,
      interactive_count: page.interactiveCount ?? -1
    };
  });
}

async function waitInTab(request) {
  // wait 是给 Agent 的“耐心”工具：页面没加载完、元素没出现、文本没刷出来时，不要立刻改方案。
  const mode = String(request.mode || request.wait_for || request.waitFor || "").trim();
  if (mode === "load") return await waitForLoad(request);
  if (mode === "selector") return await waitForSelector(request);
  if (mode === "text") return await waitForText(request);
  if (mode === "stable") return await waitForStable(request);
  throw new Error("unknown wait mode: " + mode);
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

  const shouldMonitor = request.no_monitor !== true && request.noMonitor !== true;
  const before = shouldMonitor ? await collectLightSnapshot(tabId) : { tab: await chrome.tabs.get(tabId) };
  const [{ result }] = await chrome.scripting.executeScript({
    target: { tabId },
    world: "MAIN",
    // 这里的 eval 不是执行用户原始脚本，而是执行 buildPageEvalScript 生成的包装脚本。
    // 包装脚本会捕获异常并把返回值做序列化，避免错误直接炸掉 background。
    func: async (wrapped) => await eval(wrapped),
    args: [buildPageEvalScript(script)]
  });
  const after = shouldMonitor ? await collectLightSnapshot(tabId) : { tab: await chrome.tabs.get(tabId) };

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
    diff: shouldMonitor ? buildLightDiff(before, after) : "monitor disabled"
  };
}

function buildLightDiff(before, after) {
  // 轻量 diff 只回答“页面有没有明显变化”，不回传完整 DOM，避免把工具结果撑大。
  // 这里覆盖 URL、标题、正文长度、可交互元素数量和新 tab 数量，足够指导模型是否继续下一步。
  const changes = [];
  const beforeTab = before.tab || before;
  const afterTab = after.tab || after;
  if ((beforeTab.url || "") !== (afterTab.url || "")) changes.push("url changed");
  if ((beforeTab.title || "") !== (afterTab.title || "")) changes.push("title changed");
  if (before.page && after.page) {
    if (before.page.textLength !== after.page.textLength) {
      changes.push(`body text length ${before.page.textLength}->${after.page.textLength}`);
    }
    if (before.page.interactiveCount !== after.page.interactiveCount) {
      changes.push(`interactive elements ${before.page.interactiveCount}->${after.page.interactiveCount}`);
    }
    if (before.page.activeElement !== after.page.activeElement) {
      changes.push(`focus ${before.page.activeElement}->${after.page.activeElement}`);
    }
  }
  if (before.tabs && after.tabs && before.tabs.length !== after.tabs.length) {
    changes.push(`tab count ${before.tabs.length}->${after.tabs.length}`);
  }
  if (changes.length === 0) return "url and title unchanged";
  return changes.join(", ");
}

async function collectLightSnapshot(tabId) {
  // 动作类工具不能只返回“执行成功”，还要告诉模型页面是否真的变化。
  // 快照只取轻量指标，不取完整正文，防止工具结果污染上下文。
  const tab = await chrome.tabs.get(tabId);
  const tabs = await listTabs();
  let page = {
    textLength: -1,
    interactiveCount: -1,
    activeElement: ""
  };
  if (isScriptable(tab.url)) {
    try {
      const [{ result }] = await chrome.scripting.executeScript({
        target: { tabId },
        world: "MAIN",
        func: () => {
          const active = document.activeElement;
          const activeElement = active
            ? `${active.tagName || ""}${active.id ? "#" + active.id : ""}${active.name ? "[name=" + active.name + "]" : ""}`
            : "";
          return {
            textLength: document.body ? (document.body.innerText || "").length : 0,
            interactiveCount: document.querySelectorAll("a,button,input,textarea,select,[role=button],[contenteditable=true]").length,
            activeElement
          };
        }
      });
      page = result || page;
    } catch (_err) {
      // 页面可能正在跳转或注入失败。diff 只是辅助信息，不能让主动作因为快照失败而失败。
    }
  }
  return { tab, tabs, page };
}

async function withDebugger(tabId, fn) {
  // debugger attach 会让 Chrome 认为扩展正在控制这个 tab。
  // 点击、输入这类动作必须在同一次 attach 生命周期中连续发送事件，避免三次 attach 之间页面状态漂移。
  const target = { tabId };
  await chrome.debugger.attach(target, "1.3");
  try {
    return await fn(target);
  } finally {
    try {
      await chrome.debugger.detach(target);
    } catch (_err) {
      // detach 失败通常说明 tab 已关闭或导航中断，不应覆盖真实动作错误。
    }
  }
}

async function sendDebuggerCommand(target, method, params) {
  return await chrome.debugger.sendCommand(target, method, params || {});
}

async function handleCDP(request) {
  // 原始 CDP 命令入口，供 Go 层 browser_cdp 使用。
  // 常规点击和输入不要让模型拼这里的参数，应该走 click/type 封装。
  const tabId = await resolveTabId(request.tab_id || request.tabId);
  const method = String(request.method || "").trim();
  if (!method) {
    throw new Error("cdp method is required");
  }
  const shouldMonitor = request.no_monitor !== true && request.noMonitor !== true;
  const before = shouldMonitor ? await collectLightSnapshot(tabId) : null;
  const result = await withDebugger(tabId, async (target) => {
    return await sendDebuggerCommand(target, method, request.params || {});
  });
  const after = shouldMonitor ? await collectLightSnapshot(tabId) : null;
  return {
    status: "success",
    tab_id: String(tabId),
    method,
    result: result || {},
    diff: shouldMonitor ? buildLightDiff(before, after) : "monitor disabled"
  };
}

async function clickTab(request) {
  // 使用 CDP 三事件序列模拟真实鼠标点击。
  // 坐标是 viewport 坐标，和 getBoundingClientRect() 返回的坐标系一致。
  const tabId = await resolveTabId(request.tab_id || request.tabId);
  const x = Number(request.x);
  const y = Number(request.y);
  if (!Number.isFinite(x) || !Number.isFinite(y)) {
    throw new Error("click requires finite x and y");
  }
  const shouldMonitor = request.no_monitor !== true && request.noMonitor !== true;
  const before = shouldMonitor ? await collectLightSnapshot(tabId) : null;
  await withDebugger(tabId, async (target) => {
    await sendDebuggerCommand(target, "Page.bringToFront", {});
    // 预热一次 debugger 输入链路。首次 attach 可能影响页面布局，后续元素点击会先测 rect 再进入这里。
    await sendDebuggerCommand(target, "Input.dispatchMouseEvent", { type: "mouseMoved", x: 0, y: 0 });
    await sendDebuggerCommand(target, "Input.dispatchMouseEvent", { type: "mouseMoved", x, y });
    await sendDebuggerCommand(target, "Input.dispatchMouseEvent", {
      type: "mousePressed",
      x,
      y,
      button: "left",
      clickCount: 1
    });
    await sendDebuggerCommand(target, "Input.dispatchMouseEvent", {
      type: "mouseReleased",
      x,
      y,
      button: "left",
      clickCount: 1
    });
  });
  await new Promise((resolve) => setTimeout(resolve, 300));
  const after = shouldMonitor ? await collectLightSnapshot(tabId) : null;
  return {
    status: "success",
    tab_id: String(tabId),
    clicked_at: { x, y },
    diff: shouldMonitor ? buildLightDiff(before, after) : "monitor disabled"
  };
}

async function typeInTab(request) {
  // 输入使用 CDP，而不是 JS 直接改 value。
  // 这样页面能收到更接近真实用户输入的事件链，React/Vue 等框架也更容易同步状态。
  const tabId = await resolveTabId(request.tab_id || request.tabId);
  const text = String(request.text || "");
  if (!text) {
    throw new Error("type requires non-empty text");
  }
  const clear = request.clear === true;
  const shouldMonitor = request.no_monitor !== true && request.noMonitor !== true;
  const before = shouldMonitor ? await collectLightSnapshot(tabId) : null;
  await withDebugger(tabId, async (target) => {
    await sendDebuggerCommand(target, "Page.bringToFront", {});
    if (clear) {
      await sendDebuggerCommand(target, "Input.dispatchKeyEvent", {
        type: "keyDown",
        key: "a",
        code: "KeyA",
        windowsVirtualKeyCode: 65,
        nativeVirtualKeyCode: 65,
        modifiers: 4
      });
      await sendDebuggerCommand(target, "Input.dispatchKeyEvent", {
        type: "keyUp",
        key: "a",
        code: "KeyA",
        windowsVirtualKeyCode: 65,
        nativeVirtualKeyCode: 65,
        modifiers: 4
      });
      await sendDebuggerCommand(target, "Input.dispatchKeyEvent", {
        type: "keyDown",
        key: "Backspace",
        code: "Backspace",
        windowsVirtualKeyCode: 8,
        nativeVirtualKeyCode: 8
      });
      await sendDebuggerCommand(target, "Input.dispatchKeyEvent", {
        type: "keyUp",
        key: "Backspace",
        code: "Backspace",
        windowsVirtualKeyCode: 8,
        nativeVirtualKeyCode: 8
      });
    }
    await sendDebuggerCommand(target, "Input.insertText", { text });
  });
  await new Promise((resolve) => setTimeout(resolve, 300));
  const after = shouldMonitor ? await collectLightSnapshot(tabId) : null;
  return {
    status: "success",
    tab_id: String(tabId),
    text,
    clear,
    diff: shouldMonitor ? buildLightDiff(before, after) : "monitor disabled"
  };
}

async function handleCommand(message) {
  // Cohert Go 侧发来的命令在这里统一分发。
  // 协议保持小而稳定：tabs / scan / open / execute_js / cdp / click / type / wait。
  const command = message.command || message.cmd;
  if (command === "tabs") {
    return { status: "success", tabs: await listTabs() };
  }
  if (command === "scan") {
    return await scanTab(message);
  }
  if (command === "open") {
    return await openTab(message);
  }
  if (command === "execute_js") {
    return await executeJs(message);
  }
  if (command === "cdp") {
    return await handleCDP(message);
  }
  if (command === "click") {
    return await clickTab(message);
  }
  if (command === "type") {
    return await typeInTab(message);
  }
  if (command === "wait") {
    return await waitInTab(message);
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
