# txt2epub

`txt2epub` converts a Simplified Chinese TXT file into a clean EPUB with a generated table of contents. It can optionally call Calibre's `ebook-convert` to produce AZW3.

The first version is intentionally single-file focused. The internal pipeline is split so batch conversion can be added later without changing parsing and writing behavior.

## Features

- Reads UTF-8 and GB18030 TXT files.
- Supports level 1 and level 2 table-of-contents regexes.
- Provides default Chinese chapter regexes, overridable from config or CLI.
- Supports TOML and YAML configuration files.
- Supports user-defined line drop regexes and replace regexes.
- Optionally merges hard-wrapped lines back into paragraphs.
- Trims repeated blank lines.
- Generates a simple text cover by default.
- Writes EPUB3 `nav.xhtml` and legacy `toc.ncx`.
- Splits XHTML by level 2 headings when present, otherwise by level 1 headings.
- Uses conservative CSS and does not embed or force fonts.

## Install

```bash
go install github.com/zwyyy/txt2epub/cmd/txt2epub@latest
```

For local development:

```bash
go build ./cmd/txt2epub
```

## Usage

```bash
txt2epub book.txt
```

By default this writes `book.epub`.

Preview table-of-contents matching without writing an EPUB:

```bash
txt2epub --preview book.txt
```

Specify metadata and output:

```bash
txt2epub book.txt -o out.epub --title "书名" --author "作者"
```

Override heading regexes:

```bash
txt2epub book.txt \
  --h1-regex '^\s*(第.+卷).*$' \
  --h2-regex '^\s*(第.+章).*$'
```

Add custom filters:

```bash
txt2epub book.txt \
  --drop-regex '^\s*请收藏本站.*$' \
  --drop-regex '^\s*最新网址.*$' \
  --replace-regex '　+= '
```

Disable hard-wrapped line merging:

```bash
txt2epub book.txt --no-merge-lines
```

Generate AZW3 through Calibre:

```bash
txt2epub book.txt --format azw3
```

AZW3 output requires Calibre's `ebook-convert` to be installed. This project does not implement the AZW3/KF8 format itself.

## Configuration

If no `--config` is passed, `txt2epub` automatically checks the current directory for:

1. `txt2epub.toml`
2. `txt2epub.yaml`
3. `txt2epub.yml`

Explicit config paths are also supported:

```bash
txt2epub --config ./my-book.toml book.txt
```

Configuration priority:

```text
CLI flags > config file > built-in defaults
```

TOML example:

```toml
title = "书名"
author = "作者"
language = "zh-CN"

h1_regex = '^\s*(第[一二三四五六七八九十百千万零〇两\d]+[卷部集篇]).*$'
h2_regex = '^\s*(第[一二三四五六七八九十百千万零〇两\d]+[章节回]).*$'

merge_lines = true
trim_blank_lines = true
split_level = 2
cover = true

drop_regex = [
  '^\s*请收藏本站.*$',
  '^\s*最新网址.*$',
]

[[replace]]
pattern = '　+'
with = ' '

[style]
line_height = 1.7
paragraph_indent = "2em"
paragraph_spacing = "0"
text_align = "justify"

[calibre]
path = "ebook-convert"
output_profile = "kindle_pw3"
extra_args = ["--minimum-line-height", "140"]
```

YAML is supported as a compatibility format; see `examples/txt2epub.yaml`.

## Hard-Wrapped Lines

Some TXT files contain visual line wrapping:

```text
这是一个很长的段落，因为原网站按固定宽度
自动换行，所以一句话被拆成了两行。
```

With `merge_lines = true`, this becomes one paragraph. Blank lines and heading lines are treated as boundaries. This is a heuristic, so disable it with `--no-merge-lines` if the source text uses single newlines as meaningful paragraph breaks.

## Default Heading Regexes

Level 1:

```text
^\s*(第[一二三四五六七八九十百千万零〇两\d]+[卷部集篇]).*$
```

Level 2:

```text
^\s*(第[一二三四五六七八九十百千万零〇两\d]+[章节回]).*$
```

These defaults are intentionally conservative. For web-downloaded TXT files, use `--preview` to verify matches before generating the final EPUB.
