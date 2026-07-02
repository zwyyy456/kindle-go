package book

import (
	"fmt"
	"io"
	"strings"
)

func PrintPreview(w io.Writer, b Book) {
	fmt.Fprintf(w, "charset: %s\n", b.Stats.Text.Charset)
	fmt.Fprintf(w, "lines: original=%d dropped=%d blank=%d merged=%d\n",
		b.Stats.Text.OriginalLines,
		b.Stats.Text.DroppedLines,
		b.Stats.Text.BlankLines,
		b.Stats.Text.MergedLines,
	)
	fmt.Fprintf(w, "headings: h1=%d h2=%d split_level=%d sections=%d paragraphs=%d\n",
		b.Stats.H1Count,
		b.Stats.H2Count,
		b.SplitLevelUsed,
		b.Stats.SectionCount,
		b.Stats.ParagraphCount,
	)
	fmt.Fprintln(w, "toc:")
	for _, h := range b.Headings {
		indent := ""
		if h.Level == 2 {
			indent = "  "
		}
		fmt.Fprintf(w, "%s- %s\n", indent, strings.TrimSpace(h.Title))
	}
	if len(b.Headings) == 0 {
		fmt.Fprintln(w, "- 正文")
	}
}

func PrintSummary(w io.Writer, b Book, output string) {
	fmt.Fprintf(w, "wrote %s\n", output)
	fmt.Fprintf(w, "headings: h1=%d h2=%d split_level=%d sections=%d paragraphs=%d\n",
		b.Stats.H1Count,
		b.Stats.H2Count,
		b.SplitLevelUsed,
		b.Stats.SectionCount,
		b.Stats.ParagraphCount,
	)
}
