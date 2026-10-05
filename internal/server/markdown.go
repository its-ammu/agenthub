package server

import (
	"html"
	"html/template"
	"regexp"
	"strconv"
	"strings"
)

// renderMarkdown turns an agent's post into safe HTML. It supports the subset
// agents actually write: paragraphs (single newlines are kept as line breaks),
// fenced code, inline code, bold, italic, links, bare URLs, headings, quotes
// and flat lists. All input is HTML-escaped first and only the tags generated
// here can appear in the output; links are limited to http and https.
func renderMarkdown(src string) template.HTML {
	src = strings.ReplaceAll(strings.ReplaceAll(src, "\r\n", "\n"), "\r", "\n")
	lines := strings.Split(src, "\n")

	var out strings.Builder
	var para []string
	flushPara := func() {
		if len(para) == 0 {
			return
		}
		parts := make([]string, len(para))
		for i, l := range para {
			parts[i] = inlineMarkdown(l)
		}
		out.WriteString("<p>" + strings.Join(parts, "<br>") + "</p>")
		para = nil
	}

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// fenced code block
		if strings.HasPrefix(trimmed, "```") {
			flushPara()
			var code []string
			i++
			for ; i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```"); i++ {
				code = append(code, lines[i])
			}
			out.WriteString("<pre><code>" + html.EscapeString(strings.Join(code, "\n")) + "</code></pre>")
			continue
		}
		if trimmed == "" {
			flushPara()
			continue
		}
		if m := headingRe.FindStringSubmatch(trimmed); m != nil {
			flushPara()
			out.WriteString(`<p class="md-h">` + inlineMarkdown(m[1]) + "</p>")
			continue
		}
		if strings.HasPrefix(trimmed, ">") {
			flushPara()
			var q []string
			for ; i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), ">"); i++ {
				q = append(q, inlineMarkdown(strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(lines[i]), ">"))))
			}
			i--
			out.WriteString("<blockquote>" + strings.Join(q, "<br>") + "</blockquote>")
			continue
		}
		if ulRe.MatchString(line) || olRe.MatchString(line) {
			flushPara()
			ordered := olRe.MatchString(line)
			re := ulRe
			tag := "ul"
			if ordered {
				re, tag = olRe, "ol"
			}
			out.WriteString("<" + tag + ">")
			for ; i < len(lines) && re.MatchString(lines[i]); i++ {
				out.WriteString("<li>" + inlineMarkdown(re.FindStringSubmatch(lines[i])[1]) + "</li>")
			}
			i--
			out.WriteString("</" + tag + ">")
			continue
		}
		para = append(para, line)
	}
	flushPara()
	return template.HTML(out.String())
}

var (
	headingRe = regexp.MustCompile(`^#{1,6}\s+(.+)$`)
	ulRe      = regexp.MustCompile(`^\s*[-*+]\s+(.+)$`)
	olRe      = regexp.MustCompile(`^\s*\d+[.)]\s+(.+)$`)

	codeSpanRe = regexp.MustCompile("`([^`\n]+)`")
	mdLinkRe   = regexp.MustCompile(`\[([^\]\n]+)\]\((https?://[^)\s]+)\)`)
	bareURLRe  = regexp.MustCompile(`(^|[\s(])(https?://[^\s<]*[^\s<).,;:!?'"])`)
	boldRe     = regexp.MustCompile(`\*\*([^*\n]+?)\*\*`)
	italicStar = regexp.MustCompile(`(^|[\s(])\*([^*\s][^*\n]*?)\*($|[\s).,;:!?])`)
	italicUnd  = regexp.MustCompile(`(^|[\s(])_([^_\s][^_\n]*?)_($|[\s).,;:!?])`)
)

// inlineMarkdown formats one line of text. Code spans and links are swapped for
// placeholders while the other rules run, so their contents are left alone.
func inlineMarkdown(s string) string {
	s = html.EscapeString(s)

	var held []string
	hold := func(h string) string {
		held = append(held, h)
		return "\x00" + strconv.Itoa(len(held)-1) + "\x00"
	}

	s = codeSpanRe.ReplaceAllStringFunc(s, func(m string) string {
		return hold("<code>" + codeSpanRe.FindStringSubmatch(m)[1] + "</code>")
	})
	s = mdLinkRe.ReplaceAllStringFunc(s, func(m string) string {
		g := mdLinkRe.FindStringSubmatch(m)
		return hold(`<a href="` + g[2] + `" target="_blank" rel="noopener noreferrer nofollow">` + g[1] + "</a>")
	})
	s = bareURLRe.ReplaceAllStringFunc(s, func(m string) string {
		g := bareURLRe.FindStringSubmatch(m)
		return g[1] + hold(`<a href="`+g[2]+`" target="_blank" rel="noopener noreferrer nofollow">`+g[2]+"</a>")
	})
	s = boldRe.ReplaceAllString(s, "<strong>$1</strong>")
	s = italicStar.ReplaceAllString(s, "$1<em>$2</em>$3")
	s = italicUnd.ReplaceAllString(s, "$1<em>$2</em>$3")

	for i := len(held) - 1; i >= 0; i-- {
		s = strings.ReplaceAll(s, "\x00"+strconv.Itoa(i)+"\x00", held[i])
	}
	return s
}
