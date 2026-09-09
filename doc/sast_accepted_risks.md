# SAST 保留/误报检测点判定记录

## 概述

本文件记录 Gophish-Smishing 项目在 SAST（静态应用安全测试）整改过程中，经人工复核后判定为**「保留（功能性依赖）」**或**「误报」**的检测点，共计 **22 项**。

这些检测点之所以不在本轮整改中通过代码修改或文件删除消除，原因有二：其一，删除或改写将直接破坏产品既有功能；其二，经核实为扫描器的误报，实际不构成安全风险。

本文件的用途是为安全审计提供可追溯的判定依据：审计人员可据此核对每一项被"接受"的检测点，理解其被保留或判定为误报的具体理由与复核结论。相关完整整改方案见 [`.qoder/specs/SAST_剩余问题修复_task-76d.md`](../.qoder/specs/SAST_剩余问题修复_task-76d.md)。

## 判定分类说明

本轮保留的 22 项检测点分为两类含义：

- **功能性依赖保留**：检测点所指向的代码是产品正常功能不可或缺的实现（如 Word 粘贴清理、附件上传组件），或位于测试代码中且输入为受控常量（非用户输入）。删除即破坏功能或测试，且实际不存在可利用的攻击面，因此予以保留。
- **误报**：扫描器基于模式匹配报告了疑似风险，但经复核实际输出/输入路径不构成漏洞。典型如：输出经 JSON 序列化且 `Content-Type` 为 `application/json`（非 HTML 上下文，不触发 XSS）；或路径来源于启动期 CLI 参数、服务端生成值（非请求输入，不构成路径穿越）。此类判定为扫描器误报。

## 逐项判定表

| 检测点（文件:行/组件） | 数量 | 类别 | 保留理由 | 复核结论 |
|---|---|---|---|---|
| `plugins/pastefromword/filter/default.js` | 3 | XSS | Word 粘贴清理是模板编辑核心功能；处理对象为管理员自己粘贴的内容，innerHTML 操作是插件固有实现 | 功能性依赖保留 |
| `vendor/jquery.iframe-transport.js` | 1 | XSS | templates 页附件上传必需组件（jQuery File Upload 官方实现） | 功能性依赖保留 |
| `controllers/phish_test.go` | 10 | SSRF | 测试代码，URL 为 httptest/localhost 常量，非用户输入 | 功能性依赖保留（测试代码） |
| `controllers/route_test.go` | 2 | SSRF | 测试代码，URL 为 httptest/localhost 常量，非用户输入 | 功能性依赖保留（测试代码） |
| `models/attachment_test.go` | 1 | 路径 | 测试代码，路径为 testdata 固定值 | 功能性依赖保留（测试代码） |
| `controllers/api/response.go:21` | 1 | XSS | 误报：输出经 `json.MarshalIndent` 序列化，`Content-Type` 为 `application/json` | 误报 |
| `middleware/middleware.go:196` | 1 | XSS | 误报：`JSONError` 亦为 JSON 序列化输出（`Content-Type` 为 `application/json`） | 误报 |
| `config/config.go:51` | 1 | 路径 | 启动期 CLI 参数（运维提供），非请求输入 | 误报 |
| `util/util.go:180` | 1 | 路径 | 服务端生成的证书路径，非用户输入 | 误报 |
| `SMS script/send_sms.py:253` | 1 | 路径 | CLI 参数（操作员提供），非网络输入 | 误报 |

**合计核对**：3 + 1 + 10 + 2 + 1 + 1 + 1 + 1 + 1 + 1 = **22 项**，与整改方案 C 节一致。

## 已修复项索引

以下为本轮已处理的 A 类（真实代码漏洞修复）与 B 类（CKEditor vendor 物理清理）检测点，简要列出以便审计对照。实现细节不在此展开，详见 [`.qoder/specs/SAST_剩余问题修复_task-76d.md`](../.qoder/specs/SAST_剩余问题修复_task-76d.md) 对应章节。

### A 类：真实代码漏洞修复（4 处）

| 编号 | 处理内容 |
|---|---|
| A1 | `models/webhook.go` — webhook URL scheme 白名单（仅允许 http/https） |
| A2 | `SMS script/app.py` — method/sub_id 参数白名单校验 + 关闭 debug 调试模式 |
| A3 | `logger/logger.go` — 日志文件权限由 `0644` 收紧为 `0600` |
| A4 | `controllers/route.go` — 移除 TLS 弱套件（`TLS_RSA_WITH_AES_*`），保留 ECDHE 套件 |

### B 类：CKEditor vendor 物理清理（38 项）

| 编号 | 处理内容 |
|---|---|
| B1 | 删除 8 个 RTL 语言包文件（si/ku/km/fa），消除 27 项 Trojan Source |
| B2 | 删除 `wsc` 拼写检查插件目录并同步 `config.removePlugins`，消除 9 项 XSS |
| B3 | `plugins/link/dialogs/link.js` 原位剥离双向控制字符（U+200E 等），消除 2 项 Trojan Source |
