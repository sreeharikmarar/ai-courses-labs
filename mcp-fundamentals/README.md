# MCP Fundamentals

Hands-on labs for the [MCP Fundamentals for Building AI Agents](https://www.educative.io/courses/model-context-protocol) course on Educative.

## Labs

### [WikipediaSearch MCP Server (Go)](mcp-go-server/)

A Go implementation of the WikipediaSearch MCP server that exposes Wikipedia search capabilities over stdio transport. Includes an interactive MCP client with a GPT-4o agentic loop.

**Components:**

| Type     | Name                       | Description                                      |
|----------|----------------------------|--------------------------------------------------|
| Tool     | `fetch_wikipedia_info`     | Search Wikipedia and return title, summary, URL   |
| Tool     | `list_wikipedia_sections`  | List section headings of a Wikipedia article      |
| Tool     | `get_section_content`      | Get content of a specific section                 |
| Prompt   | `highlight_sections_prompt`| Pick the most important sections from an article  |
| Resource | `file://suggested_titles`  | Suggested Wikipedia topics from a local file      |

**How it works:**

The server uses MCP's **stdio transport** — the client spawns the server as a child process and communicates over OS pipes (no network sockets). The interactive client connects to the server, discovers available tools, and runs an agentic loop where GPT-4o autonomously decides which MCP tools to call to answer user questions.

```
┌──────────────────────────────┐
│  mcp-client (parent)         │
│                              │
│  Writes JSON-RPC ────────────────┐
│  Reads  JSON-RPC ◄─────────────┐ │
└──────────────────────────────┘ │ │
                                 │ │
┌──────────────────────────────┐ │ │
│  mcp-wikipedia-server(child) │ │ │
│                              │ │ │
│  server.ServeStdio(s)        │ │ │
│    stdin  ◄──────────────────────┘
│    stdout ─────────────────────┘
└──────────────────────────────┘
```

**Prerequisites:** Go 1.25+, `OPENAI_API_KEY`

See the [mcp-go-server README](mcp-go-server/) for build instructions, usage, and manual testing commands.
