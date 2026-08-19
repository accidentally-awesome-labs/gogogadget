package content

import (
	"sort"
	"strings"
)

// SearchResult is one ranked docs hit. score is unexported: ranking is a
// search concern, not render data.
type SearchResult struct {
	Slug    string
	Title   string
	Section string
	Snippet string
	score   int
}

const (
	maxResults    = 20
	titleWeight   = 50
	descWeight    = 10
	bodyWeight    = 2
	bodyCountCap  = 10 // per-term body hits counted, so one keyword-spam page can't dominate
	snippetBefore = 60
	snippetAfter  = 160
)

// Search ranks docs pages against a whitespace-split query. AND semantics:
// every term must hit somewhere (title, description, or body) — the same
// contract as the projects websearch. Title hits dominate, then description,
// then capped body frequency; ties keep weight order (stable sort). The
// snippet centers on the first body occurrence of the longest term, stripped
// of markdown noise.
func (d *Docs) Search(query string) []SearchResult {
	terms := splitTerms(query)
	if len(terms) == 0 || len(d.Pages) == 0 {
		return nil
	}
	var results []SearchResult
	for _, p := range d.Pages {
		score, ok := scorePage(p, terms)
		if !ok {
			continue
		}
		results = append(results, SearchResult{
			Slug:    p.Slug,
			Title:   p.Title,
			Section: p.Section,
			Snippet: snippet(p.raw, terms),
			score:   score,
		})
	}
	sort.SliceStable(results, func(i, j int) bool { return results[i].score > results[j].score })
	if len(results) > maxResults {
		results = results[:maxResults]
	}
	return results
}

func splitTerms(query string) []string {
	fields := strings.Fields(strings.ToLower(query))
	terms := make([]string, 0, len(fields))
	for _, f := range fields {
		f = strings.Trim(f, "`*_#>|[]()")
		if f != "" {
			terms = append(terms, f)
		}
	}
	return terms
}

// scorePage reports whether EVERY term hits (AND) and the page's weight.
func scorePage(p DocPage, terms []string) (int, bool) {
	title := strings.ToLower(p.Title)
	desc := strings.ToLower(p.Description)
	body := strings.ToLower(p.raw)
	total := 0
	for _, t := range terms {
		score := 0
		if strings.Contains(title, t) {
			score += titleWeight
		}
		if strings.Contains(desc, t) {
			score += descWeight
		}
		score += min(strings.Count(body, t), bodyCountCap) * bodyWeight
		if score == 0 {
			return 0, false // one missing term disqualifies the page
		}
		total += score
	}
	return total, true
}

// snippet picks a display window around the first occurrence of the longest
// term and strips the markdown syntax that would read as noise.
func snippet(raw string, terms []string) string {
	longest := ""
	for _, t := range terms {
		if len(t) > len(longest) {
			longest = t
		}
	}
	lower := strings.ToLower(raw)
	start := 0
	if i := strings.Index(lower, longest); i > snippetBefore {
		start = i - snippetBefore
	}
	end := start + snippetBefore + snippetAfter
	if end > len(raw) {
		end = len(raw)
	}
	window := raw[start:end]
	window = strings.NewReplacer("```", "", "**", "", "__", "", "###", "", "##", "", "#", "", "[", "", "]", "").Replace(window)
	window = strings.Join(strings.Fields(window), " ")
	if start > 0 {
		window = "…" + window
	}
	if end < len(raw) {
		window += "…"
	}
	return window
}
