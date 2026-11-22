# WebUI Mock

```sh
npm --prefix web run dev:mock -- --port 5173 --strictPort
```

登录 Token 为 `mock`。Mock 在 Vite 内提供 HTTP API 和模拟 WebSocket 终端，无需 Go 服务端或客户端。普通 `npm --prefix web run dev` 继续使用真实后端代理；生产构建不启用 Mock 插件或控制面板。

默认提供27个节点：16在线、6离线、2待加入、1待审核、1拒绝加入、1密钥过期，涵盖 OpenWrt、Padavan、macOS、Linux amd64/arm64 和 Windows。样本包含中文/长名称、陈旧上报、高CPU/低内存、采集失败与缺失指标。每个已登记节点有28条历史记录，支持分页；`windows-build-queued` 和 `windows-desktop-busy` 用于观察排队、执行中及节点忙的样式。

左下角 **MOCK** 面板可以重置完整数据、全部在线、全部离线、切换空列表，或模拟待加入节点上线。支持创建、重命名、重新生成加入串、审核允许/拒绝、删除、登录和退出；拒绝、过期和删除释放名称。节点ID为十进制自增序号；右侧独立展示节点信息、已采集的设备信息和历史，无采集时隐藏设备卡片。数据保存在开发服务内存中，刷新浏览器保留；重启 Vite 或重置会恢复样本。加入串仅供界面预览，不可用于真实设备登记。

Web 仅保留交互终端和执行历史。Mock 命令 API 支持任意文本的模拟成功结果，以下命令用于展示不同状态：

| 命令            | 场景                       |
| --------------- | -------------------------- |
| `mock:help`     | 可用场景说明               |
| `mock:success`  | 成功                       |
| `mock:error`    | 标准错误、失败、退出码7    |
| `mock:stderr`   | 成功并附带警告             |
| `mock:timeout`  | 超时                       |
| `mock:unknown`  | 失联、结果未知             |
| `mock:truncate` | 多行日志与截断提示         |
| `mock:empty`    | 空输出                     |
| `mock:unicode`  | 中文、Unicode、JSON文本    |
| `mock:slow`     | 12秒任务，观察排队和执行中 |

模拟终端支持 `help`、`cd`、`pwd`、`ls`/`dir`、`echo`、`export`/`set`、`env`、`whoami`、`hostname`、`uname`/`ver`、`date`、`cat`、`df`、`free`、`stty size`、`top`、`ping`、`sleep`、`clear`/`cls`、`exit`，以及退格、历史上下键和 Ctrl+C。切换标签保留独立目录和环境变量；重载重建会话，关闭释放占用并生成历史。

所有命令和终端均为文本模拟，不调用系统 shell。终端使用现有 xterm.js 与 [ws](https://github.com/websockets/ws) 的 WebSocket server，真实前端组件保持原样。
