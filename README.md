# Kindle Toolbox

`kindle2flashdict` is becoming a local Kindle toolbox. The first CLI layer includes:

- `vocab export`: reads Kindle Vocabulary Builder records, asks FlashDict for split sense candidates, uses `codex exec` to pick the sense that matches each usage sentence, and writes a FlashDict flashcard JSON file.
- `txt2epub`: converts Simplified Chinese TXT files to EPUB/native AZW3 and reflowable text EPUB files to native AZW3.
- `serve`: runs a LAN Web UI for uploads/conversion and a Kindle-friendly download page.

The implementation keeps everything in one Go CLI binary.

The confirmed Web UI v1 product direction and functional requirements are documented in
[`docs/product-design.md`](docs/product-design.md). The sections below describe the functionality
that is currently implemented.

AI proofreading calls a locally installed and logged-in `codex` CLI. The app starts isolated
`codex exec` processes with structured JSON output for first review, independent verification,
and EPUB image review; it does not call a separate model API or store an API key. Codex
authentication is reused, while user/project instructions and hooks are ignored for these calls so
they cannot change the proofreading protocol. `python3` runs the bundled deterministic TXT/EPUB
workflow scripts. The Settings page reports whether both executables are available and holds the
global model, batch-size, and concurrency defaults.

The Web UI v1 implementation order and milestone acceptance checks are documented in
[`docs/development-plan.md`](docs/development-plan.md).

## Project Layout

- `main.go`: top-level CLI dispatch only.
- `internal/config`: versioned toolbox configuration grouped by responsibility.
- `internal/converter`: format validation and conversion orchestration shared by CLI and Web.
- `internal/vocab`: Kindle Vocabulary Builder to FlashDict export workflow.
- `internal/vocab/cmd`: `vocab` command-line flags and compatibility entrypoints.
- `internal/txt2epub`: TXT cleaning and chapter parsing.
- `internal/epub`: shared EPUB reader and writer.
- `internal/azw3`: native AZW3/KF8 writer.
- `internal/proofread`: bundled workflow process runner and structured Codex CLI adapter.
- `internal/settings`: persisted Web UI defaults and dependency diagnostics.
- `internal/txt2epub/cmd`: `txt2epub` command-line flags.
- `internal/server`: local upload library, conversion Web UI, and Kindle download page.
- `internal/server/cmd`: `serve` command-line flags.

## Testing

Run the default Go and proofreading workflow checks with:

```sh
tools/test.sh
```

The default suite does not depend on Calibre. To additionally verify cover extraction with a
locally installed and compatible `ebook-meta`, run:

```sh
KINDLE_GO_CALIBRE_TEST=1 go test ./internal/azw3 -run TestCalibreExtractsNativeCover
```

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

Copy the grouped configuration file and adjust it for the book or server:

```sh
cp kindle-go.example.toml kindle-go.toml
```

The configuration format is versioned and grouped by responsibility:

- `[metadata]`: title, author, and default language.
- `[output]`: output format/path and generated TXT cover.
- `[txt]`: chapter detection, cleanup, and replacement rules.
- `[style]`: default reflowable text styling.
- `[server]`: Web UI addresses and library directory.

Unknown fields and the former top-level configuration fields are rejected.

Preview table-of-contents matching:

```sh
go run . txt2epub --preview book.txt
```

Write an EPUB:

```sh
go run . txt2epub -o book.epub --title "书名" --author "作者" book.txt
```

Generate AZW3 with the native writer:

```sh
go run . txt2epub --format azw3 book.txt
```

TXT and text-only EPUB conversion to AZW3 do not require Calibre.

Convert a reflowable text EPUB to native AZW3:

```sh
go run . txt2epub --format azw3 book.epub
```

The native EPUB reader currently covers metadata, OPF manifest/spine/guide, EPUB3 nav, EPUB2 NCX, text XHTML, cross-document internal links, bidirectional footnotes, referenced JPEG/PNG images, conservative external CSS (local imports, basic layout rules, and local image URLs), and EPUB2/EPUB3 image covers. SVG resources require an explicitly configured converter and otherwise fail safely. Complex CSS remains out of scope.

## LAN Web UI

Run a local upload/conversion server:

```sh
go run . serve --config kindle-go.toml
```

Values in `[server]` provide the defaults. `--web-addr`, `--kindle-addr`, and
`--library` explicitly override them.

Defaults:

- desktop Web UI: `:8787`
- Kindle download page: `:8788`
- library directory: `kindle-go-library`

The command prints local URLs such as:

```text
Web UI:
  http://127.0.0.1:8787/
  http://192.168.1.23:8787/

Kindle:
  http://127.0.0.1:8788/
  http://192.168.1.23:8788/
```

Use the desktop Web UI to import immutable TXT/EPUB source copies. TXT preview is synchronous;
EPUB import saves a structured AZW3 compatibility report. EPUB/AZW3 generation runs in the
persistent background task queue, and every successful output and its parameter snapshot is kept.
Failed or canceled tasks can be retried from the beginning.

The Settings page stores global TXT defaults, proofreading defaults, and the Kindle EPUB switch in
the library database. Per-book form changes affect only that preview or generation and are not
saved as book-level configuration.

The Kindle page is deliberately plain HTML. By default it lists the latest AZW3 for each book.
When “Show latest EPUB” is enabled globally, the latest EPUB is shown alongside the AZW3 for
KOReader and similar readers. Legacy MOBI/PDF originals remain downloadable after library
migration.
