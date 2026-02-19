# 🤖 goclaw (Clawdbot)

**goclaw** 是 AI 智能体平台 [OpenClaw](https://github.com/idootop/openclaw) 的高性能 Go 语言实现版本。它旨在提供一个轻量级、响应迅速且易于部署的 Agent 核心引擎。

## 🚀 为什么选择 goclaw？

虽然 OpenClaw 提供了极其丰富的生态和 UI，但 **goclaw** 专注于以下核心优势：

1.  **单二进制部署**：基于 Go 语言特性，编译后仅需一个可执行文件即可运行，无需预装 Node.js 或管理复杂的 `npm/python` 依赖包。
2.  **极致性能**：原生 Go 协程并发处理多渠道消息，在高并发场景下内存占用更低，响应速度更快。
3.  **MiniMax M2.5 深度适配**：内置对 MiniMax 系列模型 **thinking blocks（思考链）** 的流式解析与持久化支持，确保长上下文对话的逻辑连贯性。
4.  **安全围栏**：静态注册的工具系统与内置的路径拦截机制，为执行 Shell 命令和文件操作提供了可靠的安全保障。

## 🔥 核心特性

- **多渠道连接**：内置对 Telegram、飞书（Feishu）、企业微信等主流通讯平台的原生支持。
- **智能体循环**：完整的 Agent 决策循环，支持工具调用（Function Calling）、多轮迭代规划。
- **结构化记忆**：
    - **数据库层**：使用 SQLite 对每一轮对话进行完整持久化。
    - **工作区层**：通过 `IDENTITY.md`、`SOUL.md`、`MEMORY.md` 等预定义的 Markdown 模板管理 Agent 的核心逻辑与长期共识。
- **流式 SSE 优化**：针对 Anthropic/Minimax 协议优化的 SSE 解析引擎，极大降低了首字输出延迟（TTFT）。

## 📂 项目结构

- `cmd/`: 系统入口与启动逻辑。
- `internal/agent/`: Agent 核心决策循环逻辑。
- `internal/ai/`: 与 LLM 提供商交互的协议实现层。
- `internal/channels/`: 与各平台通讯渠道的适配层。
- `internal/tools/`: 受保护的本地工具箱实现。
- `workspace/`: Agent 的独立运行空间，包含性格定义、近期日志与工具偏好。

## 🛠️ 快速上手

1. **配置环境**：
   将 `.env.example` 复制并重命名为 `.env`，填入相应的 API Key 与配置。
2. **编译运行**：
   ```bash
   go build -o clawdbot ./cmd/clawdbot/
   ./clawdbot start
   ```

## 🛡️ 安全性说明

由于 goclaw 具备执行系统命令（bash）的能力，在生产环境中，强烈建议将本服务运行在 **Docker** 容器或其他隔离沙盒中。

---
*本项目为 Go 开发者提供了一个轻量且强大的人工智能 Agent 开发底座。*
