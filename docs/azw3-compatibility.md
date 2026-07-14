# AZW3 兼容性说明

## Calibre 将非文本记录识别为未知图片

状态：已确认是 Calibre 对 standalone KF8 控制记录的无害清理提示，不是 Kindle 卡死根因。

### 现象

使用 calibre 9.11.0 将原生 writer 生成的 standalone KF8/AZW3 反向转换为 EPUB 时，转换能够成功，但日志会出现：

```text
Trimming 'images/00001.unknown' from manifest
...
Trimming 'images/00011.unknown' from manifest
```

Calibre 最终会删除这些未使用的 `.unknown` 项并正常输出 EPUB。将同一内容由 Calibre
重新生成 AZW3 后再次反向转换，仍会出现相同的 11 个 `.unknown`，因此它们不是本项目
独有的资源表错误，也不能解释 Kindle 原生 Mobi8SDK 卡死。

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

观察信号是日志中出现 `images/*.unknown`；该命令仍应成功并生成 EPUB。此提示现已确认为
Calibre 对合法 standalone KF8 控制记录的清理行为，不再作为兼容性失败判据。

### Kindle 原生兼容性排查结论

2026-07 的 Kindle dmcc 日志显示，问题文件在全文索引时长期停留于
`Mobi8SDKWordIterator.next()`；修复部分结构后，新的线程转储进一步显示它停在
`Mobi8SDKContentProvider.createworditerator()`，并持有 `Mobi8SDK.class` 全局锁，继而
阻塞其他书籍的 `ReaderSDKImpl.openBook()`。与可用 KF8 做字段级差分后确认了以下结构错误：

1. FCIS 多写了两个 `uint32(0)`，长度为 60；标准 KF8 布局为 52 字节。
2. 超过 256 项的 NCX 使用变长十六进制 key，导致 `FF -> 100` 不满足 INDX 字典序；
   标准输出使用按最大 key 固定宽度的十六进制字符串。
3. PalmDOC 压缩时不存在 HUFF/CDIC 记录，但 MOBI header 的 Huffman record offset 写成了
   `0xffffffff`；标准输出使用 offset/count `0/0`。
4. 正文和样式全部塞进单一 flow，FDST count 为 1；标准解析器会把 count 小于等于 1 的
   FDST 当成不存在。现在正文引用独立的 CSS flow，并写出正文、基础样式、页面样式和导航
   样式共 4 个连续 FDST flow。
5. 正文 record 为避开标签和 UTF-8 边界而频繁缩短，且完全没有 multibyte overlap 和
   indexing TBS。现在除末条外严格写入 4096 个未压缩字节，通过 overlap 补全跨界字符，
   并根据 NCX 几何为每条 record 生成 TBS；MOBI extra data flags 为 3。
6. PalmDB record UID 使用 `0,1,2...` 且 unique ID seed 为 0。现在使用 `0,2,4...`，seed
   指向下一个可用 UID，内部数据库名使用稳定 ASCII。
7. 普通 identifier 曾被直接写入 ASIN 字段。现在基于 identifier（缺失时使用书名）生成
   稳定的 UUID v5 形式 ASIN，写入匹配的 `kindle-go:<ASIN>` source，并按 Calibre 兼容形式
   声明为 `EBOK`；同时补齐资源计数、字体覆盖和主语言字段。
8. CHUNK CNCX selector 被写成 `S-<body-aid>`，既缺少标准 XPath，也把抽离自 section
   内部的正文错误定位到 body 的 sibling。现在根据 skeleton 的实际插入父节点写成
   `P-//*[@aid='<parent-aid>']`；单一 section 指向 section，整段 body 抽离则指向 body。

这些项目均已有不依赖 Calibre 的二进制回归测试。Calibre 只用于一次性差分取证，不是构建
或运行依赖。

### 验证标准

- FCIS 长度为 52，所有字段位于标准偏移。
- NCX key 固定宽度且严格递增，包括跨越 `0FF -> 100` 的大型目录。
- PalmDOC 的 Huffman record offset/count 为 `0/0`。
- FDST 包含 4 个连续 flow，正文显式引用基础样式和页面样式 flow。
- 除末条外，每个正文 record 解压后为 4096 字节；剥离 trailer 后可无损重建全文。
- 每条正文 record 都能解析 multibyte overlap 与 indexing TBS，`extra_data_flags=3`。
- TBS 解码结果与 NCX 的层级和文本区间一致，覆盖平铺和嵌套目录。
- PalmDB UID、seed 和 ASCII 内部名称满足容器约束。
- EXTH 包含稳定的 UUID 形式 ASIN、匹配的 ASCII source、`EBOK`、资源计数、语言和字体覆盖。
- 所有 CHUNK selector 使用完整 `P-//*[@aid='…']`，且 aid 存在于对应 skeleton。
- 带文字扉页和 `--no-cover` 的目录、guide、SKEL、chunk target 测试通过。
- `go test ./...` 保持通过；最终修复版已通过 Kindle 真机验证并能正常打开；此前的错误版本在
  兼容性修复后也已将失败隔离到 webreader，不再阻塞其他书籍。

### 相关代码

- `internal/azw3/header.go`
- `internal/azw3/records.go`
- `internal/azw3/index.go`
- `internal/azw3/tbs.go`
- `internal/azw3/pdb.go`
- `internal/azw3/index_inspector_test.go`
- `internal/azw3/navigation_index_test.go`
