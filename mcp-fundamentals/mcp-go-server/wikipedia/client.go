package wikipedia

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	apiURL    = "https://en.wikipedia.org/w/api.php"
	userAgent = "WikipediaMCPServer/1.0 (Go; MCP)"
)

var httpClient = &http.Client{Timeout: 15 * time.Second}

// PageInfo holds the title, summary extract, and page ID of a Wikipedia page.
type PageInfo struct {
	Title   string
	Extract string
	PageID  int
}

// Section represents a single section heading in a Wikipedia article.
type Section struct {
	Index  string
	Title  string
	Level  string
	Number string
}

// Search queries the MediaWiki search API and returns matching page titles.
func Search(ctx context.Context, query string) ([]string, error) {
	params := url.Values{
		"action":   {"query"},
		"list":     {"search"},
		"srsearch": {query},
		"format":   {"json"},
	}

	body, err := doGet(ctx, params)
	if err != nil {
		return nil, err
	}

	var result struct {
		Query struct {
			Search []struct {
				Title string `json:"title"`
			} `json:"search"`
		} `json:"query"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing search response: %w", err)
	}

	titles := make([]string, len(result.Query.Search))
	for i, s := range result.Query.Search {
		titles[i] = s.Title
	}
	return titles, nil
}

// GetPageSummary fetches the introductory extract for a given page title.
func GetPageSummary(ctx context.Context, title string) (*PageInfo, error) {
	params := url.Values{
		"action":       {"query"},
		"titles":       {title},
		"prop":         {"extracts"},
		"exintro":      {"true"},
		"explaintext":  {"true"},
		"redirects":    {"1"},
		"format":       {"json"},
	}

	body, err := doGet(ctx, params)
	if err != nil {
		return nil, err
	}

	var result struct {
		Query struct {
			Pages map[string]struct {
				PageID  int    `json:"pageid"`
				Title   string `json:"title"`
				Extract string `json:"extract"`
				Missing string `json:"missing"`
			} `json:"pages"`
		} `json:"query"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing page summary response: %w", err)
	}

	for id, page := range result.Query.Pages {
		if id == "-1" || page.Missing != "" {
			return nil, fmt.Errorf("page not found: %s", title)
		}
		return &PageInfo{
			Title:   page.Title,
			Extract: page.Extract,
			PageID:  page.PageID,
		}, nil
	}

	return nil, fmt.Errorf("no pages returned for: %s", title)
}

// GetSections returns the list of section headings for a given page title.
func GetSections(ctx context.Context, title string) ([]Section, error) {
	params := url.Values{
		"action":    {"parse"},
		"page":      {title},
		"prop":      {"sections"},
		"redirects": {"1"},
		"format":    {"json"},
	}

	body, err := doGet(ctx, params)
	if err != nil {
		return nil, err
	}

	var result struct {
		Parse struct {
			Sections []struct {
				Index  string `json:"index"`
				Line   string `json:"line"`
				Level  string `json:"level"`
				Number string `json:"number"`
			} `json:"sections"`
		} `json:"parse"`
		Error struct {
			Code string `json:"code"`
			Info string `json:"info"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("parsing sections response: %w", err)
	}

	if result.Error.Code != "" {
		return nil, fmt.Errorf("%s: %s", result.Error.Code, result.Error.Info)
	}

	sections := make([]Section, len(result.Parse.Sections))
	for i, s := range result.Parse.Sections {
		sections[i] = Section{
			Index:  s.Index,
			Title:  s.Line,
			Level:  s.Level,
			Number: s.Number,
		}
	}
	return sections, nil
}

// GetSectionContent fetches the wikitext content of a specific section by title,
// then strips basic wikitext markup for readability.
func GetSectionContent(ctx context.Context, pageTitle, sectionTitle string) (string, error) {
	sections, err := GetSections(ctx, pageTitle)
	if err != nil {
		return "", err
	}

	sectionIndex := ""
	for _, s := range sections {
		if strings.EqualFold(s.Title, sectionTitle) {
			sectionIndex = s.Index
			break
		}
	}
	if sectionIndex == "" {
		return "", fmt.Errorf("section '%s' not found in article '%s'", sectionTitle, pageTitle)
	}

	params := url.Values{
		"action":  {"parse"},
		"page":    {pageTitle},
		"section": {sectionIndex},
		"prop":    {"wikitext"},
		"format":  {"json"},
	}

	body, err := doGet(ctx, params)
	if err != nil {
		return "", err
	}

	var result struct {
		Parse struct {
			Wikitext struct {
				Text string `json:"*"`
			} `json:"wikitext"`
		} `json:"parse"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("parsing section content response: %w", err)
	}

	return stripWikitext(result.Parse.Wikitext.Text), nil
}

// doGet performs an HTTP GET to the MediaWiki API with the given query parameters.
func doGet(ctx context.Context, params url.Values) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL+"?"+params.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("User-Agent", userAgent)

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from Wikipedia API", resp.StatusCode)
	}

	var body []byte
	buf := make([]byte, 4096)
	for {
		n, err := resp.Body.Read(buf)
		if n > 0 {
			body = append(body, buf[:n]...)
		}
		if err != nil {
			break
		}
	}
	return body, nil
}

var (
	// [[link|display text]] -> display text; [[link]] -> link
	reLinkPipe = regexp.MustCompile(`\[\[[^|\]]+\|([^\]]+)\]\]`)
	reLink     = regexp.MustCompile(`\[\[([^\]]+)\]\]`)
	// '''bold''' -> bold; ''italic'' -> italic
	reBold   = regexp.MustCompile(`'{3}(.+?)'{3}`)
	reItalic = regexp.MustCompile(`'{2}(.+?)'{2}`)
	// == heading == removal
	reHeading = regexp.MustCompile(`(?m)^=+\s*(.+?)\s*=+$`)
	// {{...}} template removal
	reTemplate = regexp.MustCompile(`\{\{[^}]*\}\}`)
	// <ref>...</ref> and <ref ... /> removal
	reRef = regexp.MustCompile(`<ref[^>]*>.*?</ref>|<ref[^/]*/\s*>`)
	// Remaining HTML tags
	reHTML = regexp.MustCompile(`<[^>]+>`)
)

// stripWikitext removes common wikitext markup for plain-text readability.
func stripWikitext(text string) string {
	text = reRef.ReplaceAllString(text, "")
	text = reTemplate.ReplaceAllString(text, "")
	text = reBold.ReplaceAllString(text, "$1")
	text = reItalic.ReplaceAllString(text, "$1")
	text = reLinkPipe.ReplaceAllString(text, "$1")
	text = reLink.ReplaceAllString(text, "$1")
	text = reHeading.ReplaceAllString(text, "$1")
	text = reHTML.ReplaceAllString(text, "")
	return strings.TrimSpace(text)
}
