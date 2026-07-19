# Cohert Browser Bridge

`Cohert Browser Bridge` 是 Cohert 的 Chrome 浏览器桥接插件。

它参考 GA 的 `assets/tmwd_cdp_bridge`，但第一版只保留必要能力：

- 连接 Cohert 本地 WebSocket 服务。
- 上报当前 Chrome 的 http/https 标签页。
- 支持读取页面标题、URL 和正文文本。
- 支持受控执行 JavaScript。
- 提供 popup 查看连接状态和标签页列表。

第一版刻意不包含 GA 扩展里的高权限能力：

- 不读取 cookies。
- 不移除 CSP 响应头。
- 不管理其他 Chrome 扩展。
- 不修改 content settings。

## 安装方式

1. 打开 Chrome。
2. 进入 `chrome://extensions`。
3. 打开 Developer mode。
4. 选择 Load unpacked。
5. 选择本目录：

```text
assert/cohert_browser_bridge
```

## 学习和开发

如果你不熟悉 JavaScript 或 Chrome 插件，先看：

```text
DEVELOPMENT_TUTORIAL.md
```

这份教程按从零开发的角度解释 `manifest.json`、`background.js`、`content.js`、`popup` 和后续 Go 侧接入方式。

如果只想逐行理解 Chrome 插件入口配置，看：

```text
MANIFEST_EXPLAINED.md
```

## 默认连接

插件会连接：

```text
ws://127.0.0.1:18777/browser
```

后续 Cohert 的 Go 侧 browser bridge server 应监听这个地址。

## WebSocket 协议

插件连接成功后发送：

```json
{
  "type": "ext_ready",
  "name": "Cohert Browser Bridge",
  "version": "0.1.0",
  "tabs": []
}
```

标签页变化时发送：

```json
{
  "type": "tabs_update",
  "tabs": []
}
```

Go 侧发送命令：

```json
{
  "id": "request-id",
  "command": "tabs"
}
```

```json
{
  "id": "request-id",
  "command": "scan",
  "tab_id": "123",
  "max_chars": 12000
}
```

```json
{
  "id": "request-id",
  "command": "execute_js",
  "tab_id": "123",
  "script": "return document.title",
  "max_return_chars": 8000
}
```

插件返回：

```json
{
  "type": "result",
  "id": "request-id",
  "result": {}
}
```

或：

```json
{
  "type": "error",
  "id": "request-id",
  "error": {
    "message": "error message",
    "stack": ""
  }
}
```

## 注意

`execute_js` 当前使用 `AsyncFunction` 执行脚本。需要返回值时请显式写 `return`：

```javascript
return document.title
```

这和 GA 的 `web_execute_js` 经验一致：带 `await` 的脚本最好显式 `return`，避免结果为空。
