# 六位码文件传输

类型：Normative（传输产品与运行契约）

状态：正式产品模块

这是一个不经控制服务器转发文件正文的独立六位码传输模块。它不是 Kindle 专用产品：

- 现代浏览器可选 WebRTC P2P、Cloudflare R2 或独立服务器 A；
- Kindle 自带浏览器使用 R2/A 离线下载；
- 服务器 C 只保存会话和信令，不提供文件上传路由。

单个取件码对应一个文件，文件上限 200MB。离线文件从第一次
`upload-started` 起保留两小时；期间允许重复 GET、HEAD 和 Range。

## 模块边界

`internal/transfer` 独占取件码、会话生命周期、WebRTC 信令、离线上传授权、到期与撤销。
调用方只负责选择部署角色和提供配置，不读取或改写传输内部状态。

- WebRTC 是要求发送方在线的传输路径，不与对象存储共用生命周期接口。
- R2 和 Server A 是离线数据面；两者由同一控制流程授权、验证和撤销。
- Kindle 不进入 WebRTC 路径，只解析已经 ready 的 R2 或 Server A 文件。
- 传输模块不依赖 Web 书库的 `library`、`task`、`settings` 或 `store` 业务状态。

## 程序组成

同一份源码构建出一个二进制，通过子命令区分部署角色：

```sh
go build -o kindle-toolbox .
```

服务器 C：

```sh
./kindle-toolbox transfer control \
  -addr 127.0.0.1:8790 \
  -db /var/lib/transfer-control/control.db \
  -public-url https://transfer.example.com \
  -storage-url https://storage.example.com \
  -storage-secret '至少 32 字节且与 A 相同的随机密钥' \
  -stun stun:stun.example.com:3478
```

服务器 A：

```sh
./kindle-toolbox transfer storage \
  -addr 127.0.0.1:8791 \
  -dir /var/lib/transfer-storage \
  -allowed-origin https://transfer.example.com \
  -secret '至少 32 字节且与 C 相同的随机密钥' \
  -max-downloads 3
```

不要把 C 与 A 配成相同主机名。程序在同时提供 `-public-url` 和
`-storage-url` 时会拒绝这种配置。生产环境由 Nginx 终止 TLS，示例见
[`deploy/transfer/`](deploy/transfer/)。

R2 额外配置：

```sh
./kindle-toolbox transfer control \
  ... \
  -r2-endpoint https://ACCOUNT_ID.r2.cloudflarestorage.com \
  -r2-bucket transfer-files \
  -r2-access-key-id "$R2_ACCESS_KEY_ID" \
  -r2-secret-access-key "$R2_SECRET_ACCESS_KEY"
```

C 只生成预签名 PUT/GET。HEAD 和 DELETE 使用 C 持有的 R2 凭证直接执行。
ETag 只用于对应上传结果，不作为 SHA-256。

## 页面

- `/sender`：选择文件以及 Online、R2、Server A 模式。
- `/` 或 `/receiver`：输入六位码；先解析离线文件，找不到时才尝试 WebRTC。
- `/diagnostics`：兼容性入口（当前也可用收发页内置诊断）。
- `/ws-probe`：兼容 RFC 6455、Hixie-76/75 的 WebSocket 回显探针。

Online 默认使用可靠、有序 DataChannel 和 32KiB 分块，根据
`pc.sctp.maxMessageSize` 自动收紧。发送端使用 `bufferedAmount` 背压；
两端通过 Web Worker 增量计算 SHA-256，完成时比较大小、块数和哈希。

接收端支持 File System Access API 时可以提前选择直写文件；其他浏览器使用
Blob，因而需要足够内存。Kindle 不进入 Online 模式。

只接受 STUN URL，程序会拒绝 TURN URL。STUN 未配置时仍可测试局域网 host
候选直连。

## R2 CORS 与真实能力验收

把 [`deploy/transfer/r2-cors.json`](deploy/transfer/r2-cors.json) 中的域名替换为 C 的真实
HTTPS 域名，再应用到 R2 Bucket。

使用生产 R2 凭证执行：

```sh
./kindle-toolbox transfer r2-probe \
  -endpoint https://ACCOUNT_ID.r2.cloudflarestorage.com \
  -bucket transfer-files \
  -access-key-id "$R2_ACCESS_KEY_ID" \
  -secret-access-key "$R2_SECRET_ACCESS_KEY" \
  -origin https://transfer.example.com
```

探针会验证：

- 浏览器 PUT CORS 预检；
- 预签名 PUT；
- C 凭证 HEAD；
- SHA-256 业务元数据；
- GET Range；
- C 凭证 DELETE；
- `x-amz-checksum-sha256` 是否被当前 R2/S3 行为接受；
- 错误 checksum 是否得到拒绝。

正常运行只把 `x-amz-meta-sha256` 作为稳定基线。探针通过之前不会在浏览器
上传授权里启用 `x-amz-checksum-sha256`。探针全部通过后，在 C 的启动参数中
加入 `-r2-checksum-sha256` 即可显式启用；该校验头会同时加入预签名内容和
浏览器 PUT 请求。

## 测试

```sh
GOCACHE=/tmp/kindle-go-cache go test ./internal/transfer ./internal/transfer/cmd
```

覆盖：

- SQLite 会话和六位码唯一约束；
- `upload-started` 幂等且不能延长；
- CREATED 十分钟过期；
- C 的 256KiB 请求体限制及无上传路由；
- R2 fake adapter 的 PUT/HEAD/GET/DELETE 控制流；
- A 的 CORS、大小限制、SHA-256、一次性 PUT、原子提交；
- A 的 GET、HEAD、Range、重复下载、三并发和撤销；
- WebSocket 现代及旧版握手。

真实 WebRTC 候选、浏览器保存行为、Kindle 下载以及真实 R2 必须按
[`transfer-real-device-testing.md`](transfer-real-device-testing.md) 验收。
