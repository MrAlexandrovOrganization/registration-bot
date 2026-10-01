// Package tgfmt composes Telegram HTML. Wrappers accept escaped fragments, not raw text.
package tgfmt

import (
	"html"
	"strings"
)

type HTML string

func Escape(s string) HTML { return HTML(html.EscapeString(s)) }
func Join(parts ...HTML) HTML {
	var b strings.Builder
	for _, p := range parts {
		b.WriteString(string(p))
	}
	return HTML(b.String())
}
func Bold(h HTML) HTML             { return "<b>" + h + "</b>" }
func Code(h HTML) HTML             { return "<code>" + h + "</code>" }
func Blockquote(h HTML) HTML       { return "<blockquote>" + h + "</blockquote>" }
func Link(h HTML, url string) HTML { return HTML(`<a href="`+html.EscapeString(url)+`">`) + h + "</a>" }
