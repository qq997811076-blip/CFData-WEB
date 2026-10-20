# CFData-Web

CFData-Web 是一个基于 Go 的 Cloudflare IP 测试与筛选工具，提供本地 Web 与 CLI 两种使用方式，支持官方 IP 段扫描、详细延迟测试、测速、结果筛选、导出和 GitHub 上传。

[在线演示站](https://cfdata-demo.cce.de5.net/) 仅使用浏览器内虚拟数据，用于预览界面与交互；真实使用请下载正式版本。

![image](img/demo.png)

### 更新说明

项目功能已趋于完善，基本达到了作者预期的效果。后续更新将以维护和新功能为主，版本迭代频率会有所降低。如果你在使用过程中有新的需求或建议，欢迎提交 Issue，作者会在评估后酌情纳入后续版本。

近期主要更新：

- CLI 交互菜单：`-cli` 进入菜单（按配置启动 / 自定义参数启动 / 修改配置参数 / 定时任务 / 帮助），`-cli qs` 按已保存配置快速启动，`-cli -参数…` 直接执行；启动时输出 Banner 与「调试等级」
- 参数设置向导：分模块逐项设置，提示格式与可选值，可保存为配置文件
- 定时任务：仅 Linux systemd，按天/周/月执行，开机自启，失败自动回滚清理，unit 与日志在程序目录
- 配置文件自动迁移：键名无变化时静默按新模板迁移；存在旧键名时交互提示（仅迁移同名值），原文件备份为 `.bak`，非交互环境自动跳过
- 启动输出统一为简洁的「当前配置」单块，版本行下方显示调试等级

## 功能

- 官方优选：扫描 Cloudflare IPv4/IPv6，按数据中心继续详细延迟测试。
- 测速：支持单点测速、批量测速和测速阈值筛选。
- 导出：支持 CSV/TXT、自定义字段、IP 类型筛选、合格结果筛选。
- 上传：支持将导出结果上传到 GitHub。
- CLI：交互菜单、参数设置向导、定时任务、配置自动迁移。
- APK：支持 Android WebView 壳运行内置后端。

## 快速开始

从 [Releases](https://github.com/PoemMisty/CFData-WEB/releases/latest) 下载对应平台程序后运行。

默认启动 Web 模式：

```text
服务启动于 http://localhost:13335
当前测速网址: auto
```

浏览器打开终端中的地址即可使用。

CLI 模式（进入交互菜单）：

```bash
./cfdata-linux-amd64 -cli
```

菜单提供：按配置启动（菜单 1，等价 `-cli qs`）、自定义参数启动（向导）、修改配置参数、定时任务、帮助。

首次使用 CLI 配置文件时会生成模板并退出，编辑配置后重新运行即可；也可通过菜单「修改配置参数」逐项设置。升级后启动时若配置键名有变化会提示迁移：同名值保留，原文件备份为 `.bak`，非交互环境自动跳过。

简单示例：

```bash
# 交互菜单（不带其他参数时默认进入菜单）
./cfdata-linux-amd64 -cli

# 按已保存配置快速启动（qs 大小写不敏感）
./cfdata-linux-amd64 -cli qs

# 官方模式直接执行：扫描 IPv4，测试 443 端口，测速地址自动选择
./cfdata-linux-amd64 -cli -mode official -offiptype 4 -offport 443 -offurl auto

```

## Web 使用

界面顶部「扫描方式」选择器支持 TCPing（默认）和 HTTPing。不同扫描模式的延迟数据不可互相比较，仅同模式内的对比才有意义。

### 扫描方式说明

- **TCPing**：测量 TCP 握手延迟，基准值。
- **HTTPing**：测量 HTTP TTFB（Time To First Byte），延迟比 TCPing 高属正常现象。延迟阈值和渲染颜色已按倍率自动缩放，倍率仅为延迟等级参考值，非精确换算：
  - 无 TLS（HTTP 端口）：×1.3
  - 有 TLS（HTTPS 端口）：×4.0

### 官方优选

1. 选择 IPv4 或 IPv6。
2. 设置测试端口、扫描并发、延迟阈值。
3. 点击“开始扫描与测试”。
4. 扫描完成后选择数据中心继续详细测试。
5. 在详细测试结果中可单点测速或批量测速。

## 测速地址

默认测速地址为 `auto`，表示由后端自动选择内置测速源。

Web 下拉项：

- 自动选择
- Cloudflare
- CM提供
- 移动专属
- 手动输入

CLI 可通过 `-offurl` 指定：

```bash
./cfdata-linux-amd64 -cli -offurl auto
./cfdata-linux-amd64 -cli -offurl speed.cloudflare.com/__down?bytes=99999999
./cfdata-linux-amd64 -cli -offurl https://example.com/file.bin
```

说明：测速只读取响应字节流计算速度，不会把测速文件保存到本地。

## 常用参数

```text
-cli              启用 CLI 模式
-mode             official
-scanmode         扫描方式：tcping（默认，TCP 握手延迟）或 httping（HTTP TTFB，延迟比 tcping 高属正常，不同模式数据不可对比）
-offthreads       官方扫描并发数
-offport          官方测试/测速端口
-offdelay         官方延迟阈值，单位毫秒
-offurl           官方测速下载地址，默认 auto
-dns              自定义 DNS 服务器
-debug            调试日志等级：false、error、all
-skipgeo          跳过地区/代理环境验证（启动时不再提示代理警告）
-out              输出文件名，默认 cfdata-results
```

完整参数可运行：

```bash
./cfdata-linux-amd64 -h
```

## 本地缓存

Web 右上角设置菜单提供“恢复全部默认配置”，会清理本地缓存文件，例如 `ips-v4.txt`、`ips-v6.txt`、`locations.json`、ASN 数据库等。任务运行中不会直接清理，避免影响测试。

## 免责声明

本程序仅限用于学习与研究目的。请在下载后24小时内自行删除。使用本程序时，应自行遵守所在地区的法律法规。作者不对使用本程序所产生的任何后果承担责任。下载或使用本程序即视为已阅读、理解并同意上述声明。

## 致谢

- TG 频道：[CF中转IP](https://t.me/CF_NAT)
- GitHub：[Kwisma/iptest](https://github.com/Kwisma/iptest)

## License

Copyright (C) 2026 PoemMisty

This project is licensed under the GNU General Public License v3.0 or later.
See the LICENSE file for details.
