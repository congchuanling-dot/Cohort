(function () {
  if (window.top !== window.self) return;
  if (document.getElementById("cohert-browser-bridge-indicator")) return;

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
    if (document.body) {
      document.body.appendChild(indicator);
    }
  };

  if (document.readyState === "loading") {
    document.addEventListener("DOMContentLoaded", append, { once: true });
  } else {
    append();
  }
})();
