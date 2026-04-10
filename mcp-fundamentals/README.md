# MCP Fundamentals

Hands-on labs for the [MCP Fundamentals for Building AI Agents](https://www.educative.io/courses/model-context-protocol) course on Educative.

This directory is also a knowledge base for engineers building their first MCP servers and deploying them to production infrastructure.

---

## What is MCP?

**Model Context Protocol (MCP)** is an open standard that lets any AI application call external tools, read data, and use prompt templates through a unified protocol. Think of it as **"USB-C for AI"**: one standard interface that connects any AI model to any capability.

### The problem MCP solves

Without MCP, every AI integration is bespoke:

```
AI App A <-- custom code --> Your Database
AI App B <-- different code --> Slack API  
AI App C <-- more custom code --> GitHub
```

With MCP, you build the integration once:

```
Any MCP client <-- MCP --> MCP Server (wraps your DB)
Any MCP client <-- MCP --> MCP Server (wraps Slack)
Any MCP client <-- MCP --> MCP Server (wraps GitHub)
```

The client speaks MCP. Your server speaks MCP. Everything in between is standard JSON-RPC. Build the server once, and it works with Claude, Cursor, Windsurf, VS Code + Copilot, or any other MCP-compatible client.

### MCP architecture

```
┌─────────────┐     ┌──────────────┐     ┌──────────────────┐
│   MCP Host  │     │  MCP Client  │     │   MCP Server     │
│             │────►│ (built into  │────►│ (your code)      │
│ Claude,     │     │  the host)   │     │                  │
│ Cursor,     │     │              │     │ Tools            │
│ VS Code,    │     │ Discovers    │     │ Prompts          │
│ Custom App  │     │ and calls    │     │ Resources        │
└─────────────┘     └──────────────┘     └──────────────────┘
```

- **Host**: The AI application. Examples: Claude Desktop, Claude Code, Cursor, Windsurf, VS Code + Copilot, or your own custom app
- **Client**: Built into the host — handles the MCP protocol, discovers servers, makes tool calls
- **Server**: Your code — exposes tools, prompts, and resources over MCP

### The three primitives

| Primitive | What it is | Who triggers it | Analogy |
|-----------|-----------|-----------------|---------|
| **Tool** | A function the LLM can call | The LLM decides | REST API endpoint |
| **Prompt** | A reusable message template | The user selects | Stored procedure |
| **Resource** | Data the LLM can read | The LLM or user | File system |

**Tools** are the most common primitive. When an LLM sees a tool schema, it can decide to call it based on the user's question. For example, given a `fetch_wikipedia_info(query)` tool, the LLM will call it when asked "What is Kubernetes?"

**Prompts** are user-triggered templates. Think of them as pre-built workflows — the user explicitly picks one, and it generates a structured message for the LLM.

**Resources** are static or semi-static data. The LLM can read them for context, like config files or reference data.

---

## How an MCP client talks to an MCP server

This is the generic flow — it works the same regardless of which client (Claude, Cursor, custom) is connecting to your server.

### 1. Connection and handshake

When the host application starts, the built-in MCP client connects to each configured server and runs a capability discovery handshake:

```
MCP Client                               MCP Server
    |                                         |
    |---- initialize ----------------------->|  Protocol version + client info
    |<--- server info + capabilities --------|  Server name, version, what it supports
    |                                         |
    |---- tools/list ----------------------->|  What tools do you have?
    |<--- tool schemas ----------------------|  JSON Schema for each tool
    |                                         |
    |---- prompts/list --------------------->|  What prompts do you have?
    |<--- prompt list -----------------------|  Prompt names + arguments
    |                                         |
    |---- resources/list ------------------->|  What resources do you have?
    |<--- resource list ---------------------|  Resource URIs + descriptions
```

After this handshake, the host injects the tool schemas into the LLM's context. The LLM now "sees" the available tools as part of its system prompt — their names, descriptions, and parameter schemas.

### 2. LLM decides which tool to call

When the user asks a question, the LLM reads through the available tool schemas and decides which tool(s) to call. **The tool description is the LLM's documentation** — it directly determines whether the LLM picks the right tool.

This is the same mechanism whether the LLM is Claude, GPT-4, or any other model — the host presents tool schemas, the LLM outputs a tool call, the host executes it via MCP.

### 3. Tool execution

```
MCP Client                               MCP Server
    |                                         |
    |---- tools/call ----------------------->|  {"name": "summarize_article",
    |                                         |   "arguments": {"topic": "K8s"}}
    |                                         |
    |                                         |  Server executes the handler function
    |                                         |
    |<--- tool result -----------------------|  {title, url, intro, sections...}
```

### 4. Response synthesis

The LLM receives the tool result in its context window, synthesizes a response for the user. If one tool call isn't enough, it may call additional tools before responding.

### What's on the wire

MCP uses [JSON-RPC 2.0](https://www.jsonrpc.org/specification). Every message is a JSON object:

```json
{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"summarize_article","arguments":{"topic":"Kubernetes"}}}
```

This protocol is the same regardless of transport. The two transport options are:

| Transport | How it works | Use case |
|-----------|-------------|----------|
| **stdio** | Client spawns server as child process, talks over OS pipes (stdin/stdout) | Local: desktop apps, CLI tools, IDE extensions |
| **StreamableHTTP** | Standard HTTP POST to `/mcp` endpoint | Remote: containers, K8s, cloud platforms |

---

## Tips for building MCP servers

- **Write clear tool descriptions** — the LLM reads them to decide which tool to call. Be specific about what the tool returns and when to use it.
- **Offer composite tools for common workflows** — a single `summarize_article` call beats six sequential calls. Keep fine-grained tools for edge cases.
- **Keep tool count small** (4-5 is ideal) — every schema goes into the LLM's context window.
- **Use consistent parameter names** across tools — inconsistency causes the LLM to guess wrong.
- **Truncate large responses** — the LLM needs enough to synthesize from, not a raw data dump.

---

## Labs

### [WikipediaSearch MCP Server (Go)](mcp-go-server/)

A complete MCP server implementation covering tools, prompts, resources, multiple transports, containerization, and Kubernetes deployment.

See the [mcp-go-server README](mcp-go-server/) for full build instructions, architecture deep-dive, and client configuration examples.
