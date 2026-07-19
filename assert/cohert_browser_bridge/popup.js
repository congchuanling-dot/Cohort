async function send(cmd) {
  return await chrome.runtime.sendMessage({ cmd });
}

function setText(id, value) {
  document.getElementById(id).textContent = value;
}

async function refresh() {
  const statusResp = await send("status");
  const tabsResp = await send("tabs");

  const status = statusResp?.data || {};
  const tabs = tabsResp?.data || [];

  setText("status", status.connected ? "connected" : "waiting for Cohert");
  setText("wsUrl", status.wsUrl || "");
  setText("tabCount", String(tabs.length));
  setText("tabs", tabs.map((tab) => {
    const active = tab.active ? "active" : "inactive";
    return `${tab.id} [${active}]\n${tab.title}\n${tab.url}`;
  }).join("\n\n") || "No scriptable http/https tabs.");
}

document.getElementById("refresh").addEventListener("click", refresh);
document.getElementById("reconnect").addEventListener("click", async () => {
  await send("reconnect");
  await refresh();
});

refresh().catch((err) => {
  setText("status", "error");
  setText("tabs", err.message || String(err));
});
