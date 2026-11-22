# probex

Go 主机探针与远程管理工具。客户端主动连接服务端，支持设备信息上报、远程命令和浏览器交互式终端。

## 程序与平台

| 程序 | 平台 |
|---|---|
| `probexd` 服务端 | Linux amd64 |
| `probex` 客户端 | macOS arm64、Linux amd64、Linux arm64、Linux mipsle softfloat、Windows amd64 |

服务端内置 WebUI 和 SQLite 存储。客户端不依赖服务端页面或数据库；Linux 产物采用静态链接。MIPS 设备使用小端软浮点版本。系统仍需满足所用 Go 版本的最低内核要求。

## 启动服务端

无参数即可启动，默认监听 `127.0.0.1:17892`，SQLite 数据库为**程序所在目录**下的 `probex.db`，与启动时的工作目录无关：

```sh
./probexd-linux-amd64
# 显式配置监听地址、数据库及准入策略
./probexd-linux-amd64 --listen 0.0.0.0:17892 \
  --db /path/to/probex.db --auto-approve false
```

也支持 `listen 0.0.0.0:17892` 子命令。未配置 `--admin-token` 或 `PROBEX_ADMIN_TOKEN` 时，首次创建程序目录中的 `probexd.token`（权限 0600），后续启动复用。启动日志显示文件路径，可读取该文件中的 token 登录 WebUI；程序目录需可写。显式参数和环境变量优先于文件，显式 token 不写入文件。

默认允许客户端自主申请并自动批准；`--auto-approve false` 改为人工审核，`--allow-registration false` 关闭新的自主申请。这两个参数也支持 `--参数=false` 写法。已有待审核申请保持待审核，切换自动批准不会追溯批准旧申请。

开启 HTTPS/WSS 必须显式同时提供证书与私钥；即使程序目录中已存在证书，也不会自动开启 TLS：

```sh
./probexd-linux-amd64 --listen 0.0.0.0:17892 \
  --tls-cert /path/to/probexd.cert --tls-key /path/to/probexd.key
```

浏览器访问对应的 HTTP/HTTPS 地址，使用管理 token 登录。点击“添加节点”，只需填写名称，即可预创建待加入节点并生成加入密钥。管理 token 用于 WebUI 与管理 CLI，数据库保存节点、准入状态及执行记录。

## 生成与导出证书

`create-tls` 使用 ECDSA P-256 生成私有 CA、服务器证书链和服务器私钥，无需安装 OpenSSL。默认在程序目录生成 `probexd.cert`、`probexd.key`（0600）和 `probex.crt`；默认 SAN 为 `localhost,127.0.0.1,::1`。对外使用时必须通过 `--hosts` 填写客户端连接的实际域名或 IP，不包含协议、端口：

```sh
./probexd-linux-amd64 create-tls --hosts localhost,127.0.0.1,219.151.180.79,probex.example.com
# 也可显式指定三个输出路径，父目录须已存在
./probexd-linux-amd64 create-tls --hosts probex.example.com \
  --tls-cert /path/to/probexd.cert --tls-key /path/to/probexd.key --ca /path/to/probex.crt
```

服务器证书有效期一年，CA 十年；CA 私钥只用于这次签发，不保存。后续重新生成证书会创建新的 CA，需同步更新客户端信任。命令不会覆盖已有文件，需使用新的输出路径或自行保管、移走旧文件。生成后仍需显式指定 TLS 启动参数。

`create-ca` 校验证书与私钥匹配，从已有证书链导出末端 CA；自签证书导出自身。默认读取程序目录中的 `probexd.cert`、`probexd.key`，输出 `probex.crt`，支持相同的路径参数：

```sh
./probexd-linux-amd64 create-ca
./probexd-linux-amd64 create-ca \
  --tls-cert /path/to/server-chain.cert --tls-key /path/to/server.key --ca /path/to/ca.crt
```

已有链必须能够通过签名、有效期和服务器用途校验。只有一张由 CA 签发的服务器叶证书时，无法通过该证书及服务器私钥还原原 CA，命令会明确报错；需取得签发方提供的 CA 链。CA 文件只含公开证书，可交给客户端的 `--ca`。

## 连接客户端

复制 WebUI 生成的加入密钥，在目标设备运行：

```sh
./probex-linux-arm64 connect \
  --server wss://服务器:17892 \
  --key 'px1_加入密钥' \
  --state /path/to/probex-node.json
```

也可将密钥保存到权限 0600 的文件，通过 `--key-file /path/to/join.key` 读取，避免密钥进入进程参数和 shell 历史。加入密钥只能绑定一个节点，默认 24 小时有效，服务端可用 `--join-ttl 2h` 调整。未使用的密钥可在节点详情中重新生成，旧密钥随即失效。

首次连接前会保存独立节点身份；绑定成功后，后续启动只需相同的服务端地址与身份文件：

```sh
./probex-linux-arm64 connect \
  --server wss://服务器:17892 \
  --state /path/to/probex-node.json
```

`--server` 必须显式提供，协议、地址和目标端口由用户指定；WSS 不写端口时使用 443，WS 使用 80，不自动采用加入串里的后台监听端口。经过 Nginx 时可使用 `--server wss://probex.example.com`。

服务端自身启用 TLS 时，加入串包含启动证书文件中的公开证书链，客户端保留链与主机名/IP SAN 验证。证书文件若只包含叶证书，则信任固定到该证书，换证后需更新客户端信任；建议使用包含 CA 的证书链。证书和 CA 私钥不会进入加入串。

服务端自身未启用 TLS 时，加入串不带 CA；Nginx 对外提供 HTTPS 时，客户端使用系统根证书库。自建 CA 或缺根证书的路由器可以额外指定 `--ca /path/to/ca.pem`，优先于加入串内的信任材料。

同时提供 `--tls-cert` 和 `--tls-key` 时启用 HTTPS/WSS；都不提供时监听 HTTP/WS，只提供一个会报错。客户端和管理 CLI 按 URL 中的协议选择传输，不使用额外的 TLS 或明文开关。明文 WS/HTTP 不加密凭据、命令和输出，应只用于可信网络或反向代理的内部链路。Nginx 在同机终止 TLS 时，可将请求转发至 `127.0.0.1:17892` 并设置 `X-Forwarded-Proto https`；服务端仅信任回环地址传来的该请求头，并为登录 Cookie 设置 Secure。客户端到 Nginx 使用 WSS。

身份文件应放在可写、持久保存的目录，程序以前台方式运行。OpenWrt/Padavan 可用 `/tmp` 临时测试，但它占内存且重启会丢失。客户端无需公网地址或入站端口，前提是允许连接服务端。

## 主动申请加入

服务端默认允许自主加入并自动批准，客户端可以不持加入密钥，使用名称提交申请：

```sh
./probex-linux-arm64 connect \
  --server wss://服务器:17892 --name node1 \
  --ca /path/to/ca.pem --state /path/to/probex-node.json
```

公网可信证书不需要 `--ca`；自签证书或私有 CA 需要预先将公开证书安全地交给客户端。客户端在首次请求前保存独立随机密钥，服务端为新申请分配自增节点 ID，并由客户端保存。默认批准后直接连接；服务端设置 `--auto-approve false` 时，客户端等待管理员在 WebUI 或 CLI 中允许加入后才连接终端和命令通道。拒绝或删除会使客户端停止；不会自动更换身份重复申请。`--allow-registration false` 不影响已有申请的查询、批准后的连接或管理员预先生成的加入密钥。

节点名称仅由有效的待加入和已批准节点占用。拒绝加入、密钥过期或删除会释放名称；拒绝或过期记录在删除前保留；删除节点会移除其全部数据库记录，旧身份和旧密钥仍然无效。重新申请可使用 `connect --server ... --name node1 --state ... --rejoin`，显式生成新身份；也可以由管理员新建同名节点后使用新的 `--key`，程序会替换身份文件中的旧身份。删除身份文件不会恢复已撤销的密钥。不要对仍占用名称的有效节点使用 `--rejoin`。

加入失败时，客户端区分连接/DNS/超时、未信任 CA、CA 验证失败、证书地址或有效期不符、注册关闭、名称冲突以及密钥错误/过期/已使用、拒绝或删除等原因，并给出对应处理方法。网络中断沿用退避重连；证书和明确的准入错误停止，不自动降级明文或更换身份。

当前连接协议为 v4，仅支持配套的服务端与客户端。开发版不兼容旧数据库和旧身份格式；启动时遇到旧数据库会明确报错，请保留旧文件并通过 `--db` 指定新文件。旧客户端身份请使用新的加入密钥，或使用 `--rejoin` 申请新身份。

## WebUI

登录使用 HttpOnly 会话 Cookie，HTTP 与 HTTPS 分别保存会话，避免同一地址切换协议时发生 Cookie 冲突；HTTPS 会话保留 Secure 属性。更换版本后若原 HTTP 会话失效，重新登录即可。

- 节点列表：名称、状态、系统与最后上报时间。状态包括在线、离线、待加入、待审核、拒绝加入、密钥过期；待加入是管理员预创建的节点，待审核是客户端主动提交的申请。
- 节点详情：节点信息、设备信息、资源指标、采集错误及执行历史；CLI命令的状态、输出和退出码可在历史中查看。
- Terminal：基于 xterm.js，Unix 使用 PTY，Windows 使用 ConPTY，优先启动系统 Windows PowerShell（`powershell.exe -NoLogo -NoProfile`），不加载用户 profile；找不到 PowerShell 时使用 `cmd.exe`。保持 shell 状态，支持 `cd`、Ctrl+C、窗口缩放及已有交互程序。Ctrl/Cmd+F 打开搜索按钮下方的浮动搜索框（宽350px，窄屏自适应），搜索终端缓冲区；Enter/Shift+Enter 切换匹配，Escape 关闭搜索；HTTP(S) 输出链接可点击打开。

每个节点可同时运行一个单次命令和一个终端会话；单次命令之间、终端会话之间分别限制为一个。单次命令通过CLI/API执行，Web仅提供交互终端及历史查看。终端与命令使用客户端进程账户权限。关闭浏览器终端、退出登录或连接中断会结束终端；不自动恢复 shell 会话。

单次命令默认超时 60 秒，可设置 1–3600 秒。stdout/stderr 合计保存上限为 256 KiB，超出后标注截断。交互终端只实时转发，不保存终端执行记录或输出到数据库。执行历史只记录 CLI/API 单次命令；节点信息面板的“清除记录”按钮确认后删除该节点已结束的命令记录与输出，跳过正在执行和等待执行的命令，不影响其后续结果或终端。命令历史不自动按时间清理。

网络中断时已下发命令可能有未收到结果的副作用，记录标为“结果未知”，不会自动重新执行。Windows 单次命令使用 `cmd.exe /C`；Unix 使用 `/bin/sh -c`，命令和脚本需要适配目标系统。

## 管理 CLI

六个管理命令的默认地址都是 `http://127.0.0.1:17892`。未显式指定 `--server` 时，先通过无凭据的健康检查确定协议；HTTP 连接失败或明确收到“HTTP 发给 HTTPS 服务”的响应后尝试 HTTPS，再执行管理操作，不会重放添加、删除或命令执行。业务错误不会触发协议切换。

回环地址（`127.0.0.0/8`、`::1`、`localhost`）上的管理 CLI 忽略 `--ca` 和服务器证书验证，直连本机且不使用代理或跟随重定向。远程管理及所有客户端继续校验 TLS。显式指定的 `--server` 严格遵循其协议，不自动切换；非默认端口也需显式提供。

```sh
./probexd-linux-amd64 list
./probexd-linux-amd64 add --name node1
./probexd-linux-amd64 approve --client 1
./probexd-linux-amd64 reject --client 2
./probexd-linux-amd64 exec --client 1 --command 'uname -a'
./probexd-linux-amd64 remove --client 1
```

远程管理示例：

```sh
export PROBEX_ADMIN_TOKEN='<管理 token>'
./probexd-linux-amd64 list --server https://服务器:17892
./probexd-linux-amd64 add --server https://服务器:17892 --name node1
./probexd-linux-amd64 approve --server https://服务器:17892 --client '节点 ID'
./probexd-linux-amd64 reject --server https://服务器:17892 --client '节点 ID'
./probexd-linux-amd64 exec --server https://服务器:17892 \
  --client node1 --command 'uname -a' --timeout 60
./probexd-linux-amd64 remove --server https://服务器:17892 --client node1
```

节点 ID 由服务端数据库自增分配，删除后不重复使用；认证依赖独立密钥。`list` 显示名称、状态、加入时间和节点 ID；尚未批准或绑定的节点加入时间为“—”。`add` 输出加入密钥。管理命令优先使用 `--admin-token` 或 `PROBEX_ADMIN_TOKEN`；本机回环地址下未提供 token 时，读取程序目录的 `probexd.token`。远程管理必须显式提供 token，不会自动发送本地文件中的 token。远程 HTTPS 支持 `--ca` 指定 CA。`--client` 可以是 ID 或唯一名称；多个历史节点同名时必须使用 ID。执行命令时等待结果并输出 stdout/stderr；1–255 的远端退出码传递给调用方，其他失败返回 1，完整退出码保存在记录中。

删除节点会撤销身份、关闭连接、结束活动任务并释放名称，同时从数据库删除该节点的元数据、设备信息、加入凭据和全部命令记录与输出。旧身份不能重连，新设备需要新的加入密钥或重新申请并获批准。

## 从源码构建

使用 Go 1.26.11 或兼容的已修复工具链，以及 Node.js 22 或更新版本：

```sh
npm --prefix web ci
bash scripts/build.sh
```

`web/dist/` 是 Vite 的前端构建产物；`web/embed.go` 将其嵌入服务端，Go 编译前须先执行 `npm --prefix web run build`（完整流程见 `scripts/build.sh`）。根目录 `dist/` 包含六个平台产物、使用说明、第三方许可证与 SHA256SUMS。WebUI 使用 Svelte、Bits UI、Lucide、Tailwind CSS 和 TypeScript，构建后嵌入服务端；运行时不需要 Node.js 或外部 CDN。

前端使用 Prettier 和 Svelte 插件统一格式：`npm --prefix web run format` 写入格式，`npm --prefix web run format:check` 检查格式。xterm.js 与 fit、search、web-links 插件通过 npm 锁定版本并由 Vite 打包。

WebUI 支持自动/明亮/暗黑主题与自动/中文/英文语言。点击节点名称打开或切换节点标签；左侧为终端，右侧为设备信息与执行记录。信息栏按节点信息、设备信息和执行记录分区；尚未获取设备采集结果时隐藏设备信息卡片。信息栏默认360px，支持拖动调整，窄屏上下排列。切换标签保留shell和已选执行记录；关闭标签结束对应会话。CLI命令与Web终端可并行，关闭或重载终端不影响正在执行的CLI命令。离线或待加入节点也能查看信息；上线后可点击终端重新加载建立会话。删除节点、退出登录和结束、重载或关闭活动终端前会显示页面内确认框，取消不执行操作。首页操作列、侧栏节点右键菜单及信息栏顶部齿轮提供重命名和删除；主动待审核节点提供允许加入、拒绝加入和删除；待绑定或密钥过期的节点可重新生成密钥，已绑定节点不显示此操作。过期名称已被其他节点占用时，重新生成会明确拒绝。重命名保留节点ID、客户端凭据和历史，不中断连接，客户端继续使用原身份文件重连。左右侧栏宽度及主题、语言选择保存在当前浏览器。

第三方软件许可证见 [THIRD_PARTY_NOTICES](THIRD_PARTY_NOTICES)。本项目自身尚未声明开源许可证。
