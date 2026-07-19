(function () {
  // content script 会被注入到页面里。
  // 如果页面有 iframe，content script 可能在多个 frame 里运行；第一版只在顶层页面显示标记。
  if (window.top !== window.self) return;

  // 防止页面局部刷新或脚本重复注入时创建多个角标。
  if (document.getElementById("cohert-browser-bridge-indicator")) return;

  // 这个角标不是核心功能，只是开发调试用：
  // 看到它就说明插件已经成功注入当前 http/https 页面。
  const indicator = document.createElement("div");
  indicator.id = "cohert-browser-bridge-indicator";
  indicator.textContent = "Cohert bridge";
  indicator.title = "Cohert Browser Bridge is injected on this page";
  indicator.style.cssText = [
    "position:fixed",
    "right:10px",
    "bottom:10px",
    "z-index:2147483647",
    "padding:4px 7px",
    "border-radius:5px",
    "background:rgba(25,118,210,.78)",
    "color:#fff",
    "font:11px -apple-system,BlinkMacSystemFont,Segoe UI,sans-serif",
    "box-shadow:0 2px 8px rgba(0,0,0,.18)",
    "pointer-events:none",
    "opacity:.62"
  ].join(";");

  const append = () => {
    // document_idle 时通常已有 body，但少数页面加载时机特殊，所以这里仍然做一次保护。
    if (document.body) {
      document.body.appendChild(indicator);
    }
  };

  if (document.readyState === "loading") {
    // body 还没准备好时，等 DOMContentLoaded 再挂载角标。
    document.addEventListener("DOMContentLoaded", append, { once: true });
  } else {
    append();
  }
})();
