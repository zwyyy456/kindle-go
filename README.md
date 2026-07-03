# Kindle Toolbox

`kindle2flashdict` is becoming a local Kindle toolbox. The first CLI layer includes:

- `vocab export`: reads Kindle Vocabulary Builder records, asks FlashDict for split sense candidates, uses `codex exec` to pick the sense that matches each usage sentence, and writes a FlashDict flashcard JSON file.
- `txt2epub`: converts Simplified Chinese TXT files to EPUB, with optional AZW3 output through Calibre's `ebook-convert`.

WebUI is planned for a later phase; the current implementation keeps everything in one Go CLI binary.

## Project Layout

- `main.go`: top-level CLI dispatch only.
- `internal/vocab`: Kindle Vocabulary Builder to FlashDict export workflow.
- `internal/vocab/cmd`: `vocab` command-line flags and compatibility entrypoints.
- `internal/txt2epub`: TXT cleaning, chapter parsing, EPUB writing, and optional Calibre conversion.
- `internal/txt2epub/cmd`: `txt2epub` command-line flags.

## Vocabulary Export

Current scope:

- macOS only.
- Requires FlashDict to be running. The CLI reads FlashDict's lookup bridge discovery file and connects to its local Unix socket.
- Requires a logged-in `codex` CLI.
- Skips Kindle records without `usage`.
- Deduplicates by normalized `term + usage`.
- Writes low-confidence or failed items to `review.jsonl`.

## Usage

```sh
cp kindle2flashdict.example.toml kindle2flashdict.toml
go run . vocab export -config kindle2flashdict.toml
```

The old single-purpose invocation is still accepted for compatibility:

```sh
go run . -config kindle2flashdict.toml
```

FlashDict import output defaults to `flashdict-cards.json`.

By default the FlashDict lookup bridge discovery file is read from:

```text
~/Library/Containers/tech.hyperseek.flashdict/Data/Library/Application Support/FlashDict/lookup-bridge.json
```

For debugging, `sense_source.socket_path` can be set in the config to bypass discovery.

## TXT to EPUB

Preview table-of-contents matching:

```sh
go run . txt2epub --preview book.txt
```

Write an EPUB:

```sh
go run . txt2epub book.txt -o book.epub --title "书名" --author "作者"
```

Generate AZW3 through Calibre:

```sh
go run . txt2epub book.txt --format azw3
```

AZW3 output requires Calibre's `ebook-convert` to be installed. The tool does not implement AZW3/KF8 directly.
