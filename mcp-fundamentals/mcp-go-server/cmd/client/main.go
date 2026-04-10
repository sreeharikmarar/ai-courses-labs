package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/openai/openai-go/v3"
)

func main() {
	if os.Getenv("OPENAI_API_KEY") == "" {
		log.Fatal("OPENAI_API_KEY environment variable is required")
	}

	ctx := context.Background()

	// Connect to MCP server via stdio
	mcpClient, err := client.NewStdioMCPClient("./mcp-wikipedia-server", nil)
	if err != nil {
		log.Fatalf("Failed to start MCP server: %v", err)
	}
	defer mcpClient.Close()

	// Initialize MCP session
	initResult, err := mcpClient.Initialize(ctx, mcp.InitializeRequest{
		Params: mcp.InitializeParams{
			ProtocolVersion: mcp.LATEST_PROTOCOL_VERSION,
			ClientInfo: mcp.Implementation{
				Name:    "mcp-go-client",
				Version: "1.0.0",
			},
		},
	})
	if err != nil {
		log.Fatalf("Failed to initialize MCP: %v", err)
	}
	fmt.Printf("Connected to MCP server: %s %s\n", initResult.ServerInfo.Name, initResult.ServerInfo.Version)

	// Discover tools
	toolsResult, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		log.Fatalf("Failed to list tools: %v", err)
	}
	fmt.Printf("Discovered %d tools\n", len(toolsResult.Tools))

	// Convert MCP tools to OpenAI function tools
	openaiTools := convertMCPToolsToOpenAI(toolsResult.Tools)

	// Create OpenAI client (reads OPENAI_API_KEY from env)
	aiClient := openai.NewClient()

	// Conversation history with system message
	messages := []openai.ChatCompletionMessageParamUnion{
		openai.DeveloperMessage("You are a helpful assistant that uses tools to explore Wikipedia."),
	}

	// Interactive REPL
	scanner := bufio.NewScanner(os.Stdin)
	printHelp()

	for {
		fmt.Print("\nYou: ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		switch {
		case input == "exit" || input == "quit" || input == "q":
			fmt.Println("Goodbye!")
			return
		case input == "/prompts":
			listPrompts(ctx, mcpClient)
		case strings.HasPrefix(input, "/prompt "):
			handlePrompt(ctx, mcpClient, &aiClient, openaiTools, &messages, input)
		case input == "/resources":
			listResources(ctx, mcpClient)
		case strings.HasPrefix(input, "/resource "):
			handleResource(ctx, mcpClient, input)
		case input == "/tools":
			listTools(ctx, mcpClient)
		case input == "/help":
			printHelp()
		default:
			messages = append(messages, openai.UserMessage(input))
			runAgentLoop(ctx, &aiClient, mcpClient, openaiTools, &messages)
		}
	}
}

func printHelp() {
	fmt.Println("\nWikipedia MCP agent is ready.")
	fmt.Println("Ask anything about Wikipedia — the AI will search, browse, and summarize for you.")
	fmt.Println("")
	fmt.Println("Example queries:")
	fmt.Println("  \"Tell me about the Apollo 11 mission\"")
	fmt.Println("  \"What sections does the Wikipedia article on Rust have?\"")
	fmt.Println("  \"Show me the History section of the Python article\"")
	fmt.Println("  \"Summarize the Wikipedia article on Kubernetes\"")
	fmt.Println("")
	fmt.Println("Commands:")
	fmt.Println("  /tools                  - list available MCP tools")
	fmt.Println("  /help                   - show this help message")
	fmt.Println("  /prompts                - list available prompts")
	fmt.Println("  /prompt <name> \"args\"  - run a specific prompt")
	fmt.Println("  /resources              - list available resources")
	fmt.Println("  /resource <name>        - read a specific resource")
	fmt.Println("  exit/quit/q             - exit")
}

// listTools prints all available MCP tools and their descriptions.
func listTools(ctx context.Context, mcpClient *client.Client) {
	result, err := mcpClient.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		fmt.Printf("Failed to list tools: %v\n", err)
		return
	}

	if len(result.Tools) == 0 {
		fmt.Println("No tools found on the server.")
		return
	}

	fmt.Println("\nAvailable Tools:")
	for _, t := range result.Tools {
		fmt.Printf("  %-25s - %s\n", t.Name, t.Description)
	}
	fmt.Println("\nThe AI agent picks the right tool automatically based on your query.")
}

// convertMCPToolsToOpenAI converts MCP tool definitions to OpenAI function tool parameters.
func convertMCPToolsToOpenAI(tools []mcp.Tool) []openai.ChatCompletionToolUnionParam {
	result := make([]openai.ChatCompletionToolUnionParam, 0, len(tools))
	for _, tool := range tools {
		// Convert InputSchema to map[string]any via JSON round-trip
		schemaJSON, err := json.Marshal(tool.InputSchema)
		if err != nil {
			log.Printf("Warning: failed to marshal schema for tool %s: %v", tool.Name, err)
			continue
		}
		var params openai.FunctionParameters
		if err := json.Unmarshal(schemaJSON, &params); err != nil {
			log.Printf("Warning: failed to convert schema for tool %s: %v", tool.Name, err)
			continue
		}

		result = append(result, openai.ChatCompletionFunctionTool(openai.FunctionDefinitionParam{
			Name:        tool.Name,
			Description: openai.String(tool.Description),
			Parameters:  params,
		}))
	}
	return result
}

// runAgentLoop sends messages to OpenAI and handles tool calls in a loop until
// the model produces a final text response (no more tool calls).
func runAgentLoop(ctx context.Context, aiClient *openai.Client, mcpClient *client.Client, tools []openai.ChatCompletionToolUnionParam, messages *[]openai.ChatCompletionMessageParamUnion) {
	for {
		completion, err := aiClient.Chat.Completions.New(ctx, openai.ChatCompletionNewParams{
			Model:    openai.ChatModelGPT4o,
			Messages: *messages,
			Tools:    tools,
		})
		if err != nil {
			fmt.Printf("Error: OpenAI API call failed: %v\n", err)
			return
		}

		if len(completion.Choices) == 0 {
			fmt.Println("Error: no response from OpenAI")
			return
		}

		choice := completion.Choices[0]

		// Add assistant message (including any tool calls) to history
		*messages = append(*messages, choice.Message.ToParam())

		// No tool calls — print the final response and return
		if len(choice.Message.ToolCalls) == 0 {
			fmt.Printf("\nAI: %s\n", choice.Message.Content)
			return
		}

		// Execute each tool call via MCP
		for _, toolCall := range choice.Message.ToolCalls {
			fmt.Printf("[Calling tool: %s]\n", toolCall.Function.Name)

			// Parse the JSON arguments from OpenAI
			var args map[string]any
			if err := json.Unmarshal([]byte(toolCall.Function.Arguments), &args); err != nil {
				errMsg := fmt.Sprintf("Failed to parse arguments: %v", err)
				*messages = append(*messages, openai.ToolMessage(errMsg, toolCall.ID))
				continue
			}

			// Call the tool on the MCP server
			result, err := mcpClient.CallTool(ctx, mcp.CallToolRequest{
				Params: mcp.CallToolParams{
					Name:      toolCall.Function.Name,
					Arguments: args,
				},
			})
			if err != nil {
				errMsg := fmt.Sprintf("Tool call failed: %v", err)
				*messages = append(*messages, openai.ToolMessage(errMsg, toolCall.ID))
				continue
			}

			// Extract text content from the MCP result
			var resultText string
			for _, content := range result.Content {
				if tc, ok := content.(mcp.TextContent); ok {
					resultText += tc.Text
				}
			}
			if result.IsError {
				resultText = "Error: " + resultText
			}

			*messages = append(*messages, openai.ToolMessage(resultText, toolCall.ID))
		}
		// Loop back — send tool results to OpenAI for the next turn
	}
}

// listPrompts prints all available MCP prompts and their arguments.
func listPrompts(ctx context.Context, mcpClient *client.Client) {
	result, err := mcpClient.ListPrompts(ctx, mcp.ListPromptsRequest{})
	if err != nil {
		fmt.Printf("Failed to list prompts: %v\n", err)
		return
	}

	if len(result.Prompts) == 0 {
		fmt.Println("No prompts found on the server.")
		return
	}

	fmt.Println("\nAvailable Prompts:")
	for _, p := range result.Prompts {
		fmt.Printf("\nPrompt: %s\n", p.Name)
		if len(p.Arguments) > 0 {
			for _, arg := range p.Arguments {
				req := ""
				if arg.Required {
					req = " (required)"
				}
				fmt.Printf("  - %s%s\n", arg.Name, req)
			}
		} else {
			fmt.Println("  - No arguments required.")
		}
	}
	fmt.Println("\nUse: /prompt <name> \"arg1\" \"arg2\" ...")
}

// handlePrompt fetches an MCP prompt and sends it through the OpenAI agent loop.
func handlePrompt(ctx context.Context, mcpClient *client.Client, aiClient *openai.Client, tools []openai.ChatCompletionToolUnionParam, messages *[]openai.ChatCompletionMessageParamUnion, input string) {
	parts := parseArgs(strings.TrimPrefix(input, "/prompt "))
	if len(parts) == 0 {
		fmt.Println("Usage: /prompt <name> \"arg1\" \"arg2\" ...")
		return
	}

	promptName := parts[0]
	args := parts[1:]

	// Find the prompt definition
	result, err := mcpClient.ListPrompts(ctx, mcp.ListPromptsRequest{})
	if err != nil {
		fmt.Printf("Failed to list prompts: %v\n", err)
		return
	}

	var matched *mcp.Prompt
	for i, p := range result.Prompts {
		if p.Name == promptName {
			matched = &result.Prompts[i]
			break
		}
	}
	if matched == nil {
		fmt.Printf("Prompt '%s' not found.\n", promptName)
		return
	}

	// Validate argument count
	if len(args) != len(matched.Arguments) {
		names := make([]string, len(matched.Arguments))
		for i, a := range matched.Arguments {
			names[i] = a.Name
		}
		fmt.Printf("Expected %d arguments: %s\n", len(matched.Arguments), strings.Join(names, ", "))
		return
	}

	// Build argument map
	argMap := make(map[string]string)
	for i, a := range matched.Arguments {
		argMap[a.Name] = args[i]
	}

	// Fetch the prompt from the server
	promptResult, err := mcpClient.GetPrompt(ctx, mcp.GetPromptRequest{
		Params: mcp.GetPromptParams{
			Name:      promptName,
			Arguments: argMap,
		},
	})
	if err != nil {
		fmt.Printf("Failed to get prompt: %v\n", err)
		return
	}

	// Extract prompt text from messages
	var promptText string
	for _, msg := range promptResult.Messages {
		if tc, ok := msg.Content.(mcp.TextContent); ok {
			promptText += tc.Text
		}
	}

	// Send the prompt text through the agent loop
	*messages = append(*messages, openai.UserMessage(promptText))
	fmt.Println("\n=== Prompt Result ===")
	runAgentLoop(ctx, aiClient, mcpClient, tools, messages)
}

// listResources prints all available MCP resources.
func listResources(ctx context.Context, mcpClient *client.Client) {
	result, err := mcpClient.ListResources(ctx, mcp.ListResourcesRequest{})
	if err != nil {
		fmt.Printf("Failed to list resources: %v\n", err)
		return
	}

	if len(result.Resources) == 0 {
		fmt.Println("No resources found on the server.")
		return
	}

	fmt.Println("\nAvailable Resources:")
	for i, r := range result.Resources {
		fmt.Printf("[%d] %s\n", i+1, r.Name)
	}
	fmt.Println("\nUse: /resource <name-or-index> to view content.")
}

// handleResource reads and prints an MCP resource by name or index.
func handleResource(ctx context.Context, mcpClient *client.Client, input string) {
	resourceID := strings.TrimSpace(strings.TrimPrefix(input, "/resource "))
	if resourceID == "" {
		fmt.Println("Usage: /resource <name-or-index>")
		return
	}

	// Get available resources
	result, err := mcpClient.ListResources(ctx, mcp.ListResourcesRequest{})
	if err != nil {
		fmt.Printf("Failed to list resources: %v\n", err)
		return
	}

	// Build index-to-resource map
	indexMap := make(map[string]int)
	for i := range result.Resources {
		indexMap[fmt.Sprintf("%d", i+1)] = i
	}

	// Resolve by numeric index or by name
	var matched *mcp.Resource
	if idx, ok := indexMap[resourceID]; ok {
		matched = &result.Resources[idx]
	} else {
		for i, r := range result.Resources {
			if r.Name == resourceID {
				matched = &result.Resources[i]
				break
			}
		}
	}

	if matched == nil {
		fmt.Printf("Resource '%s' not found.\n", resourceID)
		return
	}

	// Read the resource content
	readResult, err := mcpClient.ReadResource(ctx, mcp.ReadResourceRequest{
		Params: mcp.ReadResourceParams{
			URI: matched.URI,
		},
	})
	if err != nil {
		fmt.Printf("Failed to read resource: %v\n", err)
		return
	}

	for _, content := range readResult.Contents {
		if tc, ok := content.(mcp.TextResourceContents); ok {
			fmt.Println("\n=== Resource Content ===")
			fmt.Println(tc.Text)
		}
	}
}

// parseArgs splits a string into arguments, respecting double-quoted strings.
func parseArgs(s string) []string {
	var args []string
	var current strings.Builder
	inQuote := false

	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '"':
			inQuote = !inQuote
		case ch == ' ' && !inQuote:
			if current.Len() > 0 {
				args = append(args, current.String())
				current.Reset()
			}
		default:
			current.WriteByte(ch)
		}
	}
	if current.Len() > 0 {
		args = append(args, current.String())
	}

	return args
}
