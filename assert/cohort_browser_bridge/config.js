// 插件的集中配置文件。
// background.js 通过 importScripts("config.js") 加载本文件。
// 挂到 self 上，是为了让 MV3 service worker 里的其他脚本能稳定读取。
self.COHORT_BRIDGE_CONFIG = {
  // Cohort Go 侧后续要监听的本地 WebSocket 地址。
  wsUrl: "ws://127.0.0.1:18777/browser",

  // 未连接时，background.js 会通过这个 alarm 周期性尝试重连。
  probeAlarmName: "cohort-browser-probe",

  // 已连接时，background.js 会通过这个 alarm 周期性发送 ping。
  keepaliveAlarmName: "cohort-browser-keepalive",

  // Chrome alarm 的单位是分钟。0.083 分钟约等于 5 秒。
  probeDelayMinutes: 0.083,

  // 0.4 分钟约等于 24 秒，用于降低 MV3 service worker 被挂起后的断连概率。
  keepaliveDelayMinutes: 0.4,

  // browser_scan 默认最多返回的页面正文字符数。
  maxScanChars: 12000,

  // execute_js 默认最多返回的 JS 执行结果字符数。
  maxJsReturnChars: 8000
};
