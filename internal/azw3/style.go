package azw3

import (
	"fmt"
	"strings"

	"github.com/flashdict/kindle2flashdict/internal/ebook"
)

func css(style ebook.Style) string {
	return fmt.Sprintf(`body {
  line-height: %.2f;
  text-align: %s;
}
p {
  margin: %s 0;
  text-indent: %s;
}
h1, h2, h3, h4, h5, h6 {
  text-indent: 0;
  line-height: 1.35;
  page-break-before: always;
}
section:first-child > h1:first-child,
section:first-child > h2:first-child {
  page-break-before: auto;
}
.cover {
  text-align: center;
  padding-top: 25%%;
}
.cover h1 {
  font-size: 1.6em;
  page-break-before: auto;
}
.cover .author {
  margin-top: 2em;
  text-indent: 0;
}
`, style.LineHeight, safeCSSIdent(style.TextAlign, "justify"), safeCSSLength(style.ParagraphSpacing, "0"), safeCSSLength(style.ParagraphIndent, "2em"))
}

func safeCSSIdent(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	for _, r := range value {
		if !(r == '-' || r == '_' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return fallback
		}
	}
	return value
}

func safeCSSLength(value, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	for _, r := range value {
		if !(r == '.' || r == '%' || r == '-' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return fallback
		}
	}
	return value
}
