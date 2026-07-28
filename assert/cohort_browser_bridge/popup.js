async function send(cmd) {
  // popup 页面不能直接访问 background.js 的变量。
  // Chrome 扩展里通常通过 runtime.sendMessage 向 background 请求数据。
  return await chrome.runtime.sendMessage({ cmd });
}

function setText(id, value) {
  // 小工具函数：把 background 返回的状态写到 popup 页面上。
  document.getElementById(id).textContent = value;
}

async function refresh() {
  // status 用于看 WebSocket 是否连上 Cohort。
  const statusResp = await send("status");

  // tabs 用于看插件当前能感知到哪些 http/https 标签页。
  const tabsResp = await send("tabs");

  const status = statusResp?.data || {};
  const tabs = tabsResp?.data || [];

  setText("status", status.connected ? "connected" : "waiting for Cohort");
  setText("wsUrl", status.wsUrl || "");
  setText("tabCount", String(tabs.length));
  setText("tabs", tabs.map((tab) => {
    const active = tab.active ? "active" : "inactive";
    // popup 只做人工调试，所以这里用简单文本展示 tab 信息即可。
    return `${tab.id} [${active}]\n${tab.title}\n${tab.url}`;
  }).join("\n\n") || "No scriptable http/https tabs.");
}

// 手动刷新 popup 展示，不影响插件和 Cohort 的真实连接。
document.getElementById("refresh").addEventListener("click", refresh);

// 手动触发 background.js 重连 Cohort，便于本地调试 Go 侧 server。
document.getElementById("reconnect").addEventListener("click", async () => {
  await send("reconnect");
  await refresh();
});

// popup 打开时自动刷新一次状态。
refresh().catch((err) => {
  setText("status", "error");
  setText("tabs", err.message || String(err));
});
