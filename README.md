# kindle2flashdict

`kindle2flashdict` reads Kindle Vocabulary Builder records, asks FlashDict for split sense candidates, uses `codex exec` to pick the sense that matches each usage sentence, and writes a FlashDict flashcard JSON file.

First version scope:

- macOS only.
- Requires FlashDict and its bundled `flashdict-cli`.
- Requires a logged-in `codex` CLI.
- Skips Kindle records without `usage`.
- Deduplicates by normalized `term + usage`.
- Writes low-confidence or failed items to `review.jsonl`.

## Usage

```sh
cp kindle2flashdict.example.toml kindle2flashdict.toml
go run . -config kindle2flashdict.toml
```

FlashDict import output defaults to `flashdict-cards.json`.
