# WikipediaSearch MCP Server (Go)

A Go implementation of the WikipediaSearch MCP server that exposes Wikipedia search capabilities. Supports **stdio** (local) and **StreamableHTTP** (remote) transports.

This README doubles as a knowledge base — if you're building your first MCP server, read through the [How It All Works](#how-it-all-works) section to understand the architecture end-to-end.

## Prerequisites

- Go 1.25+
- `OPENAI_API_KEY` environment variable (required by the interactive client)
- Docker (for container builds)
- Kind + kubectl (for local K8s deployment)

## Project Structure

```
mcp-go-server/
  main.go                 # MCP server: tool/prompt/resource registration + transport setup
  wikipedia/client.go     # Wikipedia API wrapper (Search, GetPageSummary, GetSections, etc.)
  cmd/client/main.go      # Interactive MCP client with GPT-4o agentic loop
  suggested_titles.txt    # Sample resource data
  Dockerfile              # Multi-stage container build
  .dockerignore           # Build context filtering
  deploy/
    kind-setup.sh         # Kind cluster setup script
    kind-config.yaml      # Kind cluster configuration
    k8s/
      namespace.yaml      # mcp namespace
      deployment.yaml     # Server deployment
      service.yaml        # NodePort service
```

## Build

```bash
cd mcp-go-server
go build -o mcp-wikipedia-server .
go build -o mcp-client ./cmd/client
```

## Components

| Type     | Name                        | Description                                                        |
|----------|-----------------------------|--------------------------------------------------------------------|
| Tool     | `summarize_article`         | Fetch a complete article (intro + all sections) in a single call   |
| Tool     | `fetch_wikipedia_info`      | Search Wikipedia and return title, summary, URL                    |
| Tool     | `list_wikipedia_sections`   | List section headings of a Wikipedia article                       |
| Tool     | `get_section_content`       | Get content of a specific section                                  |
| Prompt   | `highlight_sections_prompt` | Pick the most important sections from an article                   |
| Resource | `file://suggested_titles`   | Suggested Wikipedia topics from a local file                       |

---

## How It All Works

### The MCP client-server lifecycle

This walkthrough applies to **any MCP client** (Claude, Cursor, Windsurf, VS Code + Copilot, or a custom app). The protocol is the same — only the configuration differs.

#### Phase 1: Connection and capability discovery

When the host application starts, its built-in MCP client connects to each configured server:

```
MCP Client                               MCP Server (WikipediaSearch)
    |                                         |
    |---- initialize ----------------------->|  "Protocol version? Client info?"
    |<--- server info + capabilities --------|  "I'm WikipediaSearch v1.0.0,
    |                                         |   I support tools, prompts, resources"
    |                                         |
    |---- tools/list ----------------------->|  "What tools do you have?"
    |<--- tool schemas ----------------------|  JSON Schema for all 4 tools
    |                                         |
    |---- prompts/list --------------------->|  "What prompts?"
    |<--- prompt list -----------------------|  highlight_sections_prompt
    |                                         |
    |---- resources/list ------------------->|  "What resources?"
    |<--- resource list ---------------------|  file://suggested_titles
```

After this handshake, the host injects the tool schemas into the LLM's context. Every tool becomes available with its name, description, and parameter schema:

```
Available tools from "wikipedia" server:

- summarize_article(topic: string)
  "Fetch a complete Wikipedia article summary in a single call: returns title,
   URL, introduction, and content of all top-level sections. Use this instead
   of calling fetch_wikipedia_info + list_wikipedia_sections +
   get_section_content separately."

- fetch_wikipedia_info(query: string)
  "Search Wikipedia for a topic and return title, summary, and URL"

- list_wikipedia_sections(topic: string)
  "Return a list of section titles from the Wikipedia page"

- get_section_content(topic: string, section_title: string)
  "Return the content of a specific section"
```

**Key insight**: The LLM doesn't "know" Wikipedia. It knows it has tools. The descriptions you write in your Go code are the LLM's documentation — they directly determine whether it picks the right tool.

#### Phase 2: User asks a question

```
User: "Summarize the Wikipedia article on Kubernetes"
        |
        v
LLM (reasoning):
  "The user wants a full article summary. I see summarize_article which says
   'Fetch a complete Wikipedia article summary in a single call' and
   'Use this instead of calling the other tools separately.'
   I'll call summarize_article with topic='Kubernetes'."
```

This reasoning is the same mechanism across models — Claude, GPT-4, or any model that supports tool use. The host presents tool schemas, the LLM outputs a structured tool call, and the host executes it over MCP.

#### Phase 3: Tool execution

```
MCP Client                               MCP Server
    |                                         |
    |---- tools/call ----------------------->|  {
    |     (JSON-RPC)                          |    "name": "summarize_article",
    |                                         |    "arguments": {"topic": "Kubernetes"}
    |                                         |  }
    |                                         |
    |                                         |  Server internally:
    |                                         |    1. wikipedia.Search("Kubernetes")
    |                                         |    2. wikipedia.GetPageSummary("Kubernetes")
    |                                         |    3. wikipedia.GetSections("Kubernetes")
    |                                         |    4. wikipedia.GetSectionContent() x N
    |                                         |
    |<--- tool result -----------------------|  JSON with title, url, intro, all sections
```

The server handler runs entirely server-side. The MCP client sees one request and one response — it has no idea the server made multiple Wikipedia API calls internally.

#### Phase 4: Response synthesis

The LLM receives the tool result in its context window, synthesizes a coherent response, and presents it to the user. If one tool call isn't enough, the LLM may call additional tools before responding. The MCP client manages this loop.

**Full exchange**: one user message -> one tool call -> one response. Without the composite `summarize_article` tool, it would have been one user message -> six tool calls -> one response.

### Tools vs Prompts vs Resources — when to use which

All three are ways to expose capabilities to an LLM, but they serve different purposes:

| | Tools | Prompts | Resources |
|---|---|---|---|
| **What** | Functions the LLM can call | Templated messages injected into conversation | Data the LLM can read |
| **Who triggers** | The LLM decides to call them | The user selects them | The LLM or user requests them |
| **Analogy** | REST API endpoints | Reusable prompt templates | File system / data store |
| **When** | LLM needs to *do* something or *fetch* dynamic data | User wants a pre-built workflow | LLM needs static/semi-static context |

In this server:

- **Tools** (`summarize_article`, `fetch_wikipedia_info`, etc.) — The LLM calls these to fetch live data from Wikipedia. The LLM decides when and how to call them based on the user's question.

- **Prompt** (`highlight_sections_prompt`) — A pre-written prompt template. When the user selects it (e.g., via `/prompt highlight_sections_prompt "Kubernetes"` in the interactive client), it injects a structured message asking the LLM to identify the most important sections. The user triggers this, not the LLM.

- **Resource** (`file://suggested_titles`) — A static text file with suggested topics. The LLM can read this to suggest articles, or the user can browse it. Think of it like a config file the LLM has access to.

### Fine-grained vs composite tools

This server demonstrates both approaches:

- **Fine-grained tools** (`fetch_wikipedia_info`, `list_wikipedia_sections`, `get_section_content`) — the LLM can call them individually for targeted lookups like "just get the History section"
- **Composite tools** (`summarize_article`) — does multiple operations server-side and returns everything in one call

Without the composite tool, summarizing an article takes 6 round trips (search + list sections + fetch each section). With `summarize_article`, it's one call. Provide composite tools for common workflows, keep fine-grained tools for edge cases.

### Tips

- **Write clear tool descriptions** — the LLM reads them to decide which tool to call. Be specific about what the tool returns and when to use it.
- **Use "Use this instead of..."** in composite tool descriptions — it hints to the LLM which tool to prefer.
- **Keep tool count small** (4-5 is ideal) — every schema goes into the LLM's context window.
- **Use consistent parameter names** across tools — this server uses `topic` in most tools but `query` in one, which caused the LLM to guess wrong parameters.
- **Truncate large responses** — `summarize_article` caps sections at 2000 chars. The LLM needs enough to synthesize from, not a raw data dump.

### The MCP protocol — what's on the wire

MCP uses [JSON-RPC 2.0](https://www.jsonrpc.org/specification) over a transport (stdio pipes or HTTP). Every message is a JSON object:

**Client request:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "method": "tools/call",
  "params": {
    "name": "summarize_article",
    "arguments": {"topic": "Kubernetes"}
  }
}
```

**Server response:**
```json
{
  "jsonrpc": "2.0",
  "id": 1,
  "result": {
    "content": [{
      "type": "text",
      "text": "{\"title\":\"Kubernetes\",\"url\":\"...\",\"introduction\":\"...\",\"sections\":\"...\",\"content\":[...]}"
    }]
  }
}
```

The `id` field correlates requests to responses. The `method` field tells the server what to do. This is identical whether the transport is stdio or HTTP.

### Transport comparison

| | Stdio | StreamableHTTP | SSE (legacy) |
|---|---|---|---|
| **How it connects** | Client spawns server as child process, communicates over OS pipes (stdin/stdout) | Client sends HTTP POST to `/mcp` endpoint | Client connects to `/sse` for events, sends to `/message` |
| **Network** | None — local process only | Standard HTTP, works across machines | HTTP, but requires two endpoints |
| **Use case** | Local: desktop apps, CLI tools, IDE extensions | Remote: containers, K8s, cloud platforms | Older MCP clients |
| **Security** | No network surface — as secure as the process | Standard HTTP security (TLS, auth headers) | Same as HTTP |
| **Config** | Point client at binary path | Point client at URL | Point client at URL |

In this server, transport selection is controlled by an environment variable:

```go
switch os.Getenv("MCP_TRANSPORT") {
case "http":
    server.NewStreamableHTTPServer(s).Start(":8080")  // Remote
case "sse":
    server.NewSSEServer(s).Start(":8080")             // Legacy remote
default:
    server.ServeStdio(s)                               // Local (default)
}
```

The same server binary works locally (stdio) and in a container (HTTP) — only the environment variable changes.

---

## Interactive Client

The project includes a Go MCP client that connects to the server via stdio, discovers available tools, and runs an agentic loop using OpenAI GPT-4o. When you type a question, the client sends it to GPT-4o which autonomously decides which MCP tools to call, processes the results, and returns a final answer.

### Run

```bash
OPENAI_API_KEY=<API_KEY> ./mcp-client
```

The client expects the `mcp-wikipedia-server` binary in the current directory.

### REPL Commands

| Command                    | Description                          |
|----------------------------|--------------------------------------|
| `/tools`                   | List available MCP tools             |
| `/help`                    | Show help with example queries       |
| `/prompts`                 | List available MCP prompts           |
| `/prompt <name> "args"`   | Run a specific prompt via the agent  |
| `/resources`               | List available MCP resources         |
| `/resource <name>`         | Read a specific resource             |
| `exit` / `quit` / `q`     | Exit the client                      |

Any other input is sent as a question to the GPT-4o agent loop.

### Demo

![Interactive MCP Client](./assets/demo.gif)

## Claude Code + StreamableHTTP

Connect Claude Code to the MCP server over HTTP:

```bash
# Start the server in HTTP mode
MCP_TRANSPORT=http ./mcp-wikipedia-server &

# Register with Claude Code
claude mcp add --transport http wikipedia http://localhost:8080/mcp

# Use Claude — it will discover and call the Wikipedia tools automatically
claude
```

![Claude Code + StreamableHTTP](./assets/demo-claude-cli.gif)

## Run (Server)

The server supports multiple transports, controlled by the `MCP_TRANSPORT` environment variable:

| `MCP_TRANSPORT` | Transport       | Use case                   |
|-----------------|-----------------|----------------------------|
| _(empty/unset)_ | stdio           | Local use with MCP clients |
| `http`          | StreamableHTTP  | Remote deployment          |
| `sse`           | SSE             | Legacy remote clients      |

The `PORT` environment variable sets the listen port for `http` and `sse` modes (default: `8080`).

### Stdio (default)

```bash
./mcp-wikipedia-server
```

### StreamableHTTP

```bash
MCP_TRANSPORT=http ./mcp-wikipedia-server
# Listening on :8080/mcp
```

## Docker

```bash
docker build -t mcp-wikipedia-server .
docker run -p 8080:8080 mcp-wikipedia-server
```

The Dockerfile defaults to `MCP_TRANSPORT=http` on port `8080`.

## Local Kubernetes (Kind)

A single script builds the image, creates a Kind cluster, loads the image, and deploys:

```bash
./deploy/kind-setup.sh
```

The server is available at `http://localhost:30080/mcp`.

To tear down:

```bash
./deploy/kind-setup.sh teardown
```

## Connecting MCP clients

This server works with any MCP-compliant client. Below are configuration examples for common hosts.

### Stdio (local binary)

| Host | Configuration |
|------|--------------|
| **Claude Desktop** | Add to `~/Library/Application Support/Claude/claude_desktop_config.json`: |

```json
{
  "mcpServers": {
    "wikipedia": {
      "command": "/absolute/path/to/mcp-wikipedia-server"
    }
  }
}
```

| Host | Configuration |
|------|--------------|
| **Claude Code** | `claude mcp add wikipedia /absolute/path/to/mcp-wikipedia-server` |
| **Cursor** | Add to Cursor Settings > MCP with the binary path |
| **Custom Go client** | `client.NewStdioMCPClient("/path/to/mcp-wikipedia-server", nil)` |

### StreamableHTTP (remote endpoint)

For Docker, Kind, or any remote deployment:

| Host | Configuration |
|------|--------------|
| **Claude Desktop** | Add to `claude_desktop_config.json`: |

```json
{
  "mcpServers": {
    "wikipedia": {
      "type": "streamable-http",
      "url": "http://localhost:30080/mcp"
    }
  }
}
```

| Host | Configuration |
|------|--------------|
| **Claude Code** | `claude mcp add --transport http wikipedia http://localhost:30080/mcp` |
| **Cursor** | Add URL in Cursor Settings > MCP |
| **Any HTTP client** | `POST http://localhost:30080/mcp` with JSON-RPC body |

### curl (manual testing)

```bash
curl -s -X POST http://localhost:30080/mcp \
  -H 'Content-Type: application/json' \
  -d '{
    "jsonrpc": "2.0",
    "id": 1,
    "method": "initialize",
    "params": {
      "protocolVersion": "2025-03-26",
      "capabilities": {},
      "clientInfo": {"name": "test", "version": "1.0"}
    }
  }'
```

## Test manually (stdio)

Each command sends an `initialize` handshake followed by a request:

**List tools:**

```bash
printf '{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}\n{"jsonrpc":"2.0","id":1,"method":"tools/list"}\n' | ./mcp-wikipedia-server
```

**Call a tool:**

```bash
printf '{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}\n{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"fetch_wikipedia_info","arguments":{"query":"golang"}}}\n' | ./mcp-wikipedia-server
```

**List prompts:**

```bash
printf '{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}\n{"jsonrpc":"2.0","id":3,"method":"prompts/list"}\n' | ./mcp-wikipedia-server
```

**Read a resource:**

```bash
printf '{"jsonrpc":"2.0","id":0,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"test","version":"1.0"}}}\n{"jsonrpc":"2.0","id":4,"method":"resources/read","params":{"uri":"file://suggested_titles"}}\n' | ./mcp-wikipedia-server
```
