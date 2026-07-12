# AZW3 兼容性待办

## Calibre 将非文本记录识别为未知图片

状态：待排查，不阻塞当前 TXT → AZW3 阅读和转换。

### 现象

使用 calibre 9.11.0 将原生 writer 生成的 standalone KF8/AZW3 反向转换为 EPUB 时，转换能够成功，但日志会出现：

```text
Trimming 'images/00001.unknown' from manifest
...
Trimming 'images/00011.unknown' from manifest
```

Calibre 最终会删除这些未使用的 `.unknown` 项并正常输出 EPUB。目前没有证据表明正文、目录、扉页或章节跳转因此损坏。

### 已排除

- 与文字扉页无关：`--no-cover` 输出也会出现相同警告。
- 与 `cover` guide 误用不是同一个问题：文字页改为 `title-page` 后，带扉页的 AZW3 已能完整反向转换。
- 文件能被 Calibre 识别为 `standalone` KF8，并能重建全部 spine 文档。

### 复现

```sh
go run . txt2epub --config kindle-go.toml --format azw3 \
  -o /tmp/kindle-go-test.azw3 input.txt

ebook-convert \
  /tmp/kindle-go-test.azw3 \
  /tmp/kindle-go-roundtrip.epub
```

失败信号不是命令退出失败，而是日志中出现 `images/*.unknown`。当前命令预期仍以成功状态结束并生成 EPUB。

### 建议排查方向

1. 将 `.unknown` 编号对应回 PalmDB record，确认它们是 KF8 的 INDX、FDST、FLIS、FCIS 或结束记录中的哪些项。
2. 核对 MOBI8 header 中与首个图片 record、首个非文本 record 和各索引 record 相关的字段。
3. 对比 Calibre 自己生成的无图片 AZW3，检查 record 顺序、header sentinel 值及尾记录差异。
4. 使用 Calibre debug pipeline 或输入插件提取结果，确认其将这些 record 加入 image manifest 的判断依据。
5. 为确认的根因增加一个最小二进制回归测试，避免只依赖 Calibre 日志。

### 完成标准

- Calibre 反向转换不再产生 `images/*.unknown` 清理日志；或能够证明这些日志是 Calibre 对合法 KF8 控制记录的无害误判，并记录依据。
- 带文字扉页和 `--no-cover` 两种输出均继续通过 AZW3 → EPUB 反向转换。
- `go test ./...` 保持通过。

### 相关代码

- `internal/azw3/header.go`
- `internal/azw3/records.go`
- `internal/azw3/index.go`
- `internal/azw3/pdb.go`
- `internal/azw3/index_inspector_test.go`
- `internal/azw3/navigation_index_test.go`
