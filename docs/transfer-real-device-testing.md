# 实机验收

## 1. 局域网临时测试

在开发机启动 C：

```sh
GOCACHE=/tmp/kindle-go-cache go run . transfer control \
  -addr 0.0.0.0:8790 \
  -db /tmp/transfer-control.db
```

这种配置只能测试 WebRTC 局域网和兼容性页面。R2/A 需要各自的 HTTPS
数据面配置。

电脑浏览器打开终端输出的 `/sender`，另一台现代设备打开 `/`。选择 Online：

1. 发送至少一个小文件和一个接近 200MB 的文件；
2. 确认日志显示 32KiB 或更小分块；
3. 确认完成时大小、块数、SHA-256 一致；
4. 查看候选结果：同局域网通常为 `host / host`；
5. 接收端分别测试 Blob 和“直接保存到文件”；
6. 传输中断后应失败，不宣称支持断点续传。

跨 NAT 测试时配置一个 STUN URL，并把两台设备放到不同网络。候选应包含
`srflx`/`prflx`，不得出现 `relay`。

## 2. Server A

C 和 A 必须使用不同 HTTPS 域名。启动参数和 Nginx 配置见 README。

验收：

1. 现代浏览器从 C 创建 `Offline · Server A`；
2. 浏览器网络面板中 PUT 主机必须是 A，不是 C；
3. 上传完成后在另一浏览器输入六位码；
4. 下载主机必须是 A；
5. 下载中断后再次点击相同链接；
6. 使用支持 Range 的下载器恢复下载；
7. 两小时内重复下载；
8. 第四个并发 GET 应显示“线路繁忙”并带 `Retry-After`；
9. HEAD 不应挤占并发名额；
10. 撤销后原下载 URL 必须失效。

## 3. R2

先运行 `r2-probe`，所有基线项目通过后再测试页面：

1. PUT 和 GET 的主机必须是 R2；
2. C 的日志和磁盘不得出现文件正文；
3. ETag、大小与 `x-amz-meta-sha256` 应可在 HEAD 验证；
4. 中断后用相同 URL 做 Range 重试；
5. 撤销后对象应删除；
6. 根据 probe 结果单独记录 checksum SHA-256 是否可启用。

## 4. Kindle

Kindle 自带浏览器打开 C 的 HTTPS 首页：

1. 页面即使没有 Promise、Service Worker 和 WebRTC，也应能输入六位码；
2. 分别输入已经 READY 的 R2 和 A 六位码；
3. 页面应展示文件名、大小、SHA-256 和明确下载链接；
4. 点击后确认数据主机是 R2/A；
5. 中断后重新点击同一链接；
6. 到期或撤销后链接必须失败；
7. A 繁忙时页面应显示简短中文提示，而不是空白错误页。

不要把 Kindle 的 WebSocket 文本回显成功解释成 WebRTC 支持；能力探针已经证明
目标 Kindle 没有可调用的 PeerConnection/DataChannel API。
