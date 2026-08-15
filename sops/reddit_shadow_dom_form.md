# Reddit Shadow DOM 表单自动化 SOP

## 触发场景

- 需要在 Reddit（新版 shreddit web components）发帖。
- Reddit `/submit` 提交页的标题、正文、flair、发帖按钮用普通 CSS selector 或 `browser_snapshot` 定位不到。
- 需要穿透 shadow root 才能操作表单元素。

## 背景

Reddit 新版提交页使用 web components（shreddit），关键元素分布如下：

- 标题输入框 `textarea[name=title]`：位于 `post-composer-title` 元素的 **shadow root** 内。
- 正文输入区：light DOM 的 `div[aria-label="帖子正文字段"]`。
- flair 按钮、发帖按钮：位于 shadow root 内。
- 发帖按钮宿主：`r-post-form-submit-button`，内部按钮 `inner-post-submit-button`。

普通 CSS selector 和 `browser_snapshot` 都看不到 shadow root 内元素。

## 流程

```text
1. browser_open 目标 subreddit 的 /submit 页面 → browser_wait_for_load
2. 标题：browser_click 点击 post-composer-title 宿主元素，使 shadow root 内 textarea 聚焦
   → browser_type 输入标题
3. 正文：browser_click 点击 div[aria-label="帖子正文字段"] → browser_type 输入正文
4. shadow root 内元素（flair/发帖按钮）普通 selector 不可达：
   用 browser_execute_js 递归穿透 shadow root 读取按钮 id 与坐标
5. 坐标不可靠时：browser_screenshot + browser_ocr 读取屏幕坐标后 browser_click 兜底
6. 发帖：点击发帖按钮（inner-post-submit-button 宿主 r-post-form-submit-button）
7. 验证：browser_wait_for_url 等待跳转回社区首页 → browser_scan 确认帖子标题出现
```

关键约束：

- 标题 + 正文都填完后，提交按钮才会从 disabled 变为可用。
- 提交按钮可用后才能点击发帖。

## 禁止事项

- 不要用普通 CSS selector 直接定位 shadow root 内元素（会定位失败）。
- 不要在标题/正文未填完时尝试点击发帖按钮（此时仍 disabled）。
- 不要凭印象硬猜 shadow root 结构；先 `browser_execute_js` 读取实际 DOM。

## 验收标准

- 发帖后 URL 跳转回社区首页或帖子页。
- `browser_scan` 能读到帖子标题。
