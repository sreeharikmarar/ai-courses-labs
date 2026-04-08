package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"mcp-go-server/wikipedia"
)

func main() {
	s := server.NewMCPServer("WikipediaSearch", "1.0.0",
		server.WithToolCapabilities(false),
		server.WithPromptCapabilities(false),
		server.WithResourceCapabilities(false, false),
	)

	// --- Tools ---

	fetchInfoTool := mcp.NewTool("fetch_wikipedia_info",
		mcp.WithDescription("Search Wikipedia for a topic and return title, summary, and URL of the best match."),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("The search query to look up on Wikipedia"),
		),
	)
	s.AddTool(fetchInfoTool, handleFetchWikipediaInfo)

	listSectionsTool := mcp.NewTool("list_wikipedia_sections",
		mcp.WithDescription("Return a list of section titles from the Wikipedia page of a given topic."),
		mcp.WithString("topic",
			mcp.Required(),
			mcp.Description("The Wikipedia article topic"),
		),
	)
	s.AddTool(listSectionsTool, handleListWikipediaSections)

	getSectionContentTool := mcp.NewTool("get_section_content",
		mcp.WithDescription("Return the content of a specific section in a Wikipedia article."),
		mcp.WithString("topic",
			mcp.Required(),
			mcp.Description("The Wikipedia article topic"),
		),
		mcp.WithString("section_title",
			mcp.Required(),
			mcp.Description("The title of the section to retrieve"),
		),
	)
	s.AddTool(getSectionContentTool, handleGetSectionContent)

	summarizeArticleTool := mcp.NewTool("summarize_article",
		mcp.WithDescription("Fetch a complete Wikipedia article summary in a single call: returns title, URL, introduction, and content of all top-level sections. Use this instead of calling fetch_wikipedia_info + list_wikipedia_sections + get_section_content separately."),
		mcp.WithString("topic",
			mcp.Required(),
			mcp.Description("The topic to look up on Wikipedia"),
		),
	)
	s.AddTool(summarizeArticleTool, handleSummarizeArticle)

	// --- Prompt ---

	highlightPrompt := mcp.NewPrompt("highlight_sections_prompt",
		mcp.WithPromptDescription("Identifies the most important sections from a Wikipedia article on the given topic."),
		mcp.WithArgument("topic",
			mcp.ArgumentDescription("The Wikipedia article topic"),
			mcp.RequiredArgument(),
		),
	)
	s.AddPrompt(highlightPrompt, handleHighlightSectionsPrompt)

	// --- Resource ---

	suggestedTitlesResource := mcp.NewResource(
		"file://suggested_titles",
		"Suggested Titles",
		mcp.WithResourceDescription("Read and return suggested Wikipedia topics from a local file."),
		mcp.WithMIMEType("text/plain"),
	)
	s.AddResource(suggestedTitlesResource, handleSuggestedTitles)

	// --- Serve ---

	transport := os.Getenv("MCP_TRANSPORT")
	switch transport {
	case "http":
		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}
		addr := ":" + port
		log.Printf("Starting StreamableHTTP server on %s/mcp", addr)
		httpServer := server.NewStreamableHTTPServer(s)
		if err := httpServer.Start(addr); err != nil {
			log.Fatalf("server error: %v", err)
		}
	case "sse":
		port := os.Getenv("PORT")
		if port == "" {
			port = "8080"
		}
		addr := ":" + port
		log.Printf("Starting SSE server on %s", addr)
		sseServer := server.NewSSEServer(s)
		if err := sseServer.Start(addr); err != nil {
			log.Fatalf("server error: %v", err)
		}
	default:
		if err := server.ServeStdio(s); err != nil {
			log.Fatalf("server error: %v", err)
		}
	}
}

// handleFetchWikipediaInfo searches Wikipedia and returns the best match's title, summary, and URL.
func handleFetchWikipediaInfo(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	query, err := request.RequireString("query")
	if err != nil {
		return errorResult("Missing required parameter: query"), nil
	}

	results, err := wikipedia.Search(ctx, query)
	if err != nil {
		return errorResult(fmt.Sprintf("Search failed: %v", err)), nil
	}
	if len(results) == 0 {
		return toolResultJSON(map[string]string{"error": "No results found for your query."}), nil
	}

	bestMatch := results[0]
	page, err := wikipedia.GetPageSummary(ctx, bestMatch)
	if err != nil {
		return toolResultJSON(map[string]string{"error": "No Wikipedia page could be loaded for this query."}), nil
	}

	pageURL := "https://en.wikipedia.org/wiki/" + url.PathEscape(page.Title)

	return toolResultJSON(map[string]string{
		"title":   page.Title,
		"summary": page.Extract,
		"url":     pageURL,
	}), nil
}

// handleListWikipediaSections returns all section headings for a Wikipedia article.
func handleListWikipediaSections(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	topic, err := request.RequireString("topic")
	if err != nil {
		return errorResult("Missing required parameter: topic"), nil
	}

	sections, err := wikipedia.GetSections(ctx, topic)
	if err != nil {
		return toolResultJSON(map[string]string{"error": err.Error()}), nil
	}

	titles := make([]string, len(sections))
	for i, s := range sections {
		titles[i] = s.Title
	}

	return toolResultJSON(map[string][]string{"sections": titles}), nil
}

// handleGetSectionContent returns the content of a specific section in a Wikipedia article.
func handleGetSectionContent(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	topic, err := request.RequireString("topic")
	if err != nil {
		return errorResult("Missing required parameter: topic"), nil
	}
	sectionTitle, err := request.RequireString("section_title")
	if err != nil {
		return errorResult("Missing required parameter: section_title"), nil
	}

	content, err := wikipedia.GetSectionContent(ctx, topic, sectionTitle)
	if err != nil {
		return toolResultJSON(map[string]string{
			"error": fmt.Sprintf("Section '%s' not found in article '%s'.", sectionTitle, topic),
		}), nil
	}

	return toolResultJSON(map[string]string{"content": content}), nil
}

// handleSummarizeArticle fetches a complete article overview in a single call.
func handleSummarizeArticle(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	topic, err := request.RequireString("topic")
	if err != nil {
		return errorResult("Missing required parameter: topic"), nil
	}

	results, err := wikipedia.Search(ctx, topic)
	if err != nil {
		return errorResult(fmt.Sprintf("Search failed: %v", err)), nil
	}
	if len(results) == 0 {
		return toolResultJSON(map[string]string{"error": "No results found for your query."}), nil
	}

	bestMatch := results[0]
	page, err := wikipedia.GetPageSummary(ctx, bestMatch)
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to get page summary: %v", err)), nil
	}

	pageURL := "https://en.wikipedia.org/wiki/" + url.PathEscape(page.Title)

	sections, err := wikipedia.GetSections(ctx, page.Title)
	if err != nil {
		return errorResult(fmt.Sprintf("Failed to get sections: %v", err)), nil
	}

	// Fetch content for top-level sections (level "2"), skip boilerplate
	skipSections := map[string]bool{
		"See also": true, "References": true, "External links": true,
		"Further reading": true, "Notes": true, "Bibliography": true,
	}

	type sectionContent struct {
		Title   string `json:"title"`
		Content string `json:"content"`
	}

	var articleSections []sectionContent
	for _, s := range sections {
		if s.Level != "2" || skipSections[s.Title] {
			continue
		}
		content, err := wikipedia.GetSectionContent(ctx, page.Title, s.Title)
		if err != nil {
			continue
		}
		// Truncate very long sections to keep response manageable
		if len(content) > 2000 {
			content = content[:2000] + "\n[truncated]"
		}
		articleSections = append(articleSections, sectionContent{
			Title:   s.Title,
			Content: content,
		})
	}

	sectionTitles := make([]string, len(articleSections))
	for i, s := range articleSections {
		sectionTitles[i] = s.Title
	}

	result := map[string]any{
		"title":        page.Title,
		"url":          pageURL,
		"introduction": page.Extract,
		"sections":     strings.Join(sectionTitles, ", "),
		"content":      articleSections,
	}

	return toolResultJSON(result), nil
}

// handleHighlightSectionsPrompt returns a prompt asking the LLM to pick the most important sections.
func handleHighlightSectionsPrompt(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
	topic := request.Params.Arguments["topic"]

	promptText := fmt.Sprintf(`
    The user is exploring the Wikipedia article on "%s".

    Given the list of section titles from the article, choose the 3–5 most important or interesting sections
    that are likely to help someone learn about the topic.

    Return a bullet list of these section titles, along with 1-line explanations of why each one matters.
    `, topic)

	return &mcp.GetPromptResult{
		Description: "Identifies the most important sections from a Wikipedia article on the given topic.",
		Messages: []mcp.PromptMessage{
			{
				Role: mcp.RoleUser,
				Content: mcp.TextContent{
					Type: "text",
					Text: promptText,
				},
			},
		},
	}, nil
}

// handleSuggestedTitles reads suggested_titles.txt and returns its content.
func handleSuggestedTitles(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	exe, err := os.Executable()
	if err != nil {
		return []mcp.ResourceContents{
			mcp.TextResourceContents{
				URI:      "file://suggested_titles",
				MIMEType: "text/plain",
				Text:     "Error determining executable path",
			},
		}, nil
	}
	dir := filepath.Dir(exe)
	filePath := filepath.Join(dir, "suggested_titles.txt")

	data, err := os.ReadFile(filePath)
	if err != nil {
		// Try relative to working directory as fallback
		data, err = os.ReadFile("suggested_titles.txt")
		if err != nil {
			return []mcp.ResourceContents{
				mcp.TextResourceContents{
					URI:      "file://suggested_titles",
					MIMEType: "text/plain",
					Text:     "File not found",
				},
			}, nil
		}
	}

	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      "file://suggested_titles",
			MIMEType: "text/plain",
			Text:     string(data),
		},
	}, nil
}

// toolResultJSON marshals v to JSON and returns it as a CallToolResult text content.
func toolResultJSON(v any) *mcp.CallToolResult {
	data, _ := json.Marshal(v)
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{
				Type: "text",
				Text: string(data),
			},
		},
	}
}

// errorResult returns a CallToolResult flagged as an error.
func errorResult(msg string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{
				Type: "text",
				Text: msg,
			},
		},
		IsError: true,
	}
}
