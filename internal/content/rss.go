package content

import (
	"encoding/xml"
	"fmt"
	"strings"
	"time"
)

// FeedItem is one entry in the RSS channel, already resolved to an absolute
// link. Any content type with Feed: true contributes items.
type FeedItem struct {
	Title       string
	Link        string // absolute
	Description string
	Date        time.Time
}

// RSS renders a hand-rolled RSS 2.0 feed (no feed dependency). The caller
// passes only items that are live: there is no draft filter here.
func RSS(appURL, channelTitle, channelDesc, channelLink string, items []FeedItem) (string, error) {
	var b strings.Builder
	b.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	b.WriteString(`<rss version="2.0"><channel>`)
	fmt.Fprintf(&b, "<title>%s</title>", xmlEscape(channelTitle))
	fmt.Fprintf(&b, "<link>%s</link>", xmlEscape(channelLink))
	fmt.Fprintf(&b, "<description>%s</description>", xmlEscape(channelDesc))
	for _, it := range items {
		b.WriteString("<item>")
		fmt.Fprintf(&b, "<title>%s</title>", xmlEscape(it.Title))
		fmt.Fprintf(&b, "<link>%s</link>", xmlEscape(it.Link))
		fmt.Fprintf(&b, "<guid>%s</guid>", xmlEscape(it.Link))
		if it.Description != "" {
			fmt.Fprintf(&b, "<description>%s</description>", xmlEscape(it.Description))
		}
		fmt.Fprintf(&b, "<pubDate>%s</pubDate>", it.Date.Format(time.RFC1123Z))
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
