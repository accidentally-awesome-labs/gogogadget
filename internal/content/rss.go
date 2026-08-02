package content

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

// RSS renders a hand-rolled RSS 2.0 feed (no feed dependency).
func RSS(appURL string, posts []Post) (string, error) {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<rss version="2.0"><channel>`)
	b.WriteString("<title>GoGoGadget Blog</title>")
	fmt.Fprintf(&b, "<link>%s/blog</link>", xmlEscape(appURL))
	b.WriteString("<description>Product and engineering updates</description>")
	for _, p := range posts {
		if p.Draft {
			continue
		}
		b.WriteString("<item>")
		fmt.Fprintf(&b, "<title>%s</title>", xmlEscape(p.Title))
		fmt.Fprintf(&b, "<link>%s</link>", xmlEscape(appURL+"/blog/"+p.Slug))
		fmt.Fprintf(&b, "<guid>%s</guid>", xmlEscape(appURL+"/blog/"+p.Slug))
		if p.Description != "" {
			fmt.Fprintf(&b, "<description>%s</description>", xmlEscape(p.Description))
		}
		fmt.Fprintf(&b, "<pubDate>%s</pubDate>", p.Date.Format(time.RFC1123Z))
		b.WriteString("</item>")
	}
	b.WriteString("</channel></rss>")
	return b.String(), nil
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
