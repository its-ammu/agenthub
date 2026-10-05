package server

import (
	"regexp"
	"strings"
	"testing"
)

func TestMarkdownBasics(t *testing.T) {
	cases := map[string]string{
		"hello":                          "<p>hello</p>",
		"line one\nline two":             "<p>line one<br>line two</p>",
		"para one\n\npara two":           "<p>para one</p><p>para two</p>",
		"use `ah post` now":              "<p>use <code>ah post</code> now</p>",
		"this is **important**":          "<p>this is <strong>important</strong></p>",
		"an *emphasised* word":           "<p>an <em>emphasised</em> word</p>",
		"a _quiet_ word":                 "<p>a <em>quiet</em> word</p>",
		"snake_case_name stays":          "<p>snake_case_name stays</p>",
		"- one\n- two":                   "<ul><li>one</li><li>two</li></ul>",
		"1. first\n2. second":            "<ol><li>first</li><li>second</li></ol>",
		"> quoted":                       "<blockquote>quoted</blockquote>",
		"## Heading":                     `<p class="md-h">Heading</p>`,
		"```\nfoo <b>\nbar\n```":         "<pre><code>foo &lt;b&gt;\nbar</code></pre>",
		"**bold `code **not** bold` x**": "<p><strong>bold <code>code **not** bold</code> x</strong></p>",
	}
	for in, want := range cases {
		if got := string(renderMarkdown(in)); got != want {
			t.Errorf("renderMarkdown(%q)\n got  %s\n want %s", in, got, want)
		}
	}
}

func TestMarkdownLinks(t *testing.T) {
	got := string(renderMarkdown("see [the docs](https://example.com/a?x=1&y=2) or https://example.com/b."))
	for _, want := range []string{
		`<a href="https://example.com/a?x=1&amp;y=2" target="_blank" rel="noopener noreferrer nofollow">the docs</a>`,
		`<a href="https://example.com/b" target="_blank" rel="noopener noreferrer nofollow">https://example.com/b</a>.`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	// A link inside a code span is not turned into an anchor.
	if got := string(renderMarkdown("`https://example.com`")); strings.Contains(got, "<a ") {
		t.Errorf("link inside code span was linked: %s", got)
	}
}

func TestMarkdownIsSafe(t *testing.T) {
	attacks := []string{
		`<script>alert(1)</script>`,
		`<img src=x onerror=alert(1)>`,
		`[x](javascript:alert(1))`,
		`[x](data:text/html;base64,AAAA)`,
		`[x](https://a.com" onmouseover="alert(1))`,
		`https://a.com/"onmouseover="alert(1)`,
		"```\n</pre><script>alert(1)</script>\n```",
		`**<b onclick=x>**`,
		"`<script>`",
	}
	for _, in := range attacks {
		got := string(renderMarkdown(in))
		// Whatever is produced, no raw angle bracket may come from the input.
		stripped := regexpTags.ReplaceAllString(got, "")
		if strings.ContainsAny(stripped, "<>") {
			t.Errorf("%q leaked a raw angle bracket:\n%s", in, got)
		}
		// Every anchor must be exactly: an http(s) href with no quote inside it,
		// plus the fixed target and rel attributes.
		for _, a := range regexp.MustCompile(`<a [^>]*>`).FindAllString(got, -1) {
			if !strictAnchor.MatchString(a) {
				t.Errorf("%q produced a malformed anchor %s in:\n%s", in, a, got)
			}
		}
		if strings.Contains(got, "onerror") && !strings.Contains(got, "&lt;") {
			t.Errorf("%q kept a live event handler:\n%s", in, got)
		}
	}
}

func TestMarkdownEmpty(t *testing.T) {
	if got := renderMarkdown(""); got != "" {
		t.Errorf("empty input gave %q", got)
	}
	if got := string(renderMarkdown("\r\nwindows\r\nlines\r\n")); got != "<p>windows<br>lines</p>" {
		t.Errorf("CRLF handling: %s", got)
	}
}

// regexpTags matches the tags renderMarkdown is allowed to emit.
var regexpTags = regexp.MustCompile(`</?(p|br|strong|em|code|pre|ul|ol|li|blockquote|a)( [^<>]*)?>`)

var strictAnchor = regexp.MustCompile(`^<a href="https?://[^"<>]*" target="_blank" rel="noopener noreferrer nofollow">$`)
