# 🤖 goclaw (Clawdbot)

**goclaw** 是 AI 智能体平台 [OpenClaw](https://github.com/idootop/openclaw) 的高性能 Go 语言原生实现版本。它旨在提供一个**轻量级**、**响应迅速**且**零依赖**的 Agent 核心引擎，专为云原生环境与高并发场景打造。

## 🆚 架构对比：Goclaw vs OpenClaw

我们基于不同的技术栈构建了两个版本的核心引擎，它们各有千秋，适用于不同的场景：

| 维度 | goclaw (Go) | OpenClaw (Node.js) |
| :--- | :--- | :--- |
| **核心定位** | **高性能运行时** (Runtime) | **全栈解决方案** (Full Stack) |
| **并发性能** | 🚀 **极高** (Goroutine 原生并发) | ⚠️ 一般 (单线程 Event Loop) |
| **资源占用** | 🟢 **极低** (< 20MB 内存) | 🟡 中等 (> 100MB 内存) |
| **部署难度** | ✅ **简单** (单二进制文件，无依赖) | ❌ 复杂 (需 Node/Python 环境) |
| **二次开发** | ⚠️ 门槛较高 (需掌握 Go 语言) | ✅ **门槛低** (JavaScript/TS 普及度高) |
| **生态插件** | 🔧 核心插件内置，扩展需重新编译 | 🧩 插件生态丰富，动态加载更容易 |
| **适用场景** | **高并发微服务、边缘计算、私有化部署** | 快速原型开发、全栈应用整合 |

**总结**：如果您追求极致的性能、稳定的后端服务或方便的容器化部署，**goclaw** 是更好的选择；如果您需要快速修改业务逻辑或利用丰富的前端生态，原版 **OpenClaw** 可能更适合您。

## 🔥 核心特性

### 🧠 强大的认知内核
*   **通用协议支持**: 完美兼容 **OpenAI** 与 **Anthropic** 两大主流协议标准。
    *   无论是接入 GPT 系列、Claude 系列，还是国产的 DeepSeek、通义千问、MiniMax，只需配置对应的 API Key 与 BaseURL 即可直接使用。
*   **思维链 (CoT) 深度适配**: 针对支持 "Thinking" 过程的模型，原生支持其思维链的流式解析与展示，确保复杂任务的逻辑连贯性。
*   **系统管理指令**: 用户可通过 `/status`、`/model` 等指令实时查看或切换系统状态，完全掌控 Agent 运行环境。

### 💬 全渠道原生接入
*   **即时通讯**: 内置 Telegram、飞书 (Feishu)、钉钉 (DingTalk)、企业微信 (WeCom) 适配器。
*   **消息归一化**: 无论来自哪个渠道的消息，都会被转换为统一的内部事件结构。

### 🛡️ 企业级安全围栏
*   **沙盒执行环境**: 内置的 Shell与文件操作工具均受安全策略管控，支持严格的路径白名单。
*   **记忆持久化**: 采用 SQLite 本地数据库进行会话存储，数据完全私有化。

## 🧭 设计现状与优化方向

当前版本已实现「单二进制部署 + 多渠道接入 + 多 Provider 兼容 + 工具调用 + 本地持久化」的主线能力，适合个人与小团队生产使用。

仍在持续优化的方向：
*   **可靠性**：持续补齐 Provider 兼容回归测试（SSE 事件格式、URL 归一化、协议推断）。
*   **可观测性**：增强结构化日志与故障定位信息。
*   **架构解耦**：进一步拆分启动编排，降低维护复杂度。
*   **并发吞吐**：在保持 SQLite 可靠性的前提下优化读写路径。

## 🛠️ 快速上手

### 1. 准备配置
复制配置文件模板：
```bash
cp .env.example .env
```
根据注释填入你的 LLM 供应商 API Key。

### 2. 编译运行
**macOS / Linux**:
```bash
# 编译生成可执行文件
go build -o clawdbot ./cmd/clawdbot/

# 启动服务
./clawdbot start
```

**Docker 方式**:
```bash
docker-compose up -d
```

### 2.1 质量检查（建议）
```bash
make check
```

### 3. 使用指令
在连接的聊天窗口中（如 Telegram）：
*   `/status` - 查看当前系统状态与模型信息
*   `/clear` - 重置当前对话记忆

## 🔌 Provider 配置建议（实践版）

以下组合已在项目内进行兼容适配，推荐优先使用：
*   **Qwen 3.5 Plus**：`BASE_URL=https://dashscope.aliyuncs.com/compatible-mode/v1` + `TYPE=openai`
*   **Kimi Coding (k2p5)**：`BASE_URL=https://api.kimi.com/coding` + `TYPE=anthropic`
*   **MiniMax**：`BASE_URL=https://api.minimaxi.com/anthropic` + `TYPE=anthropic`
*   **Bailian (Qwen)**：`BASE_URL=https://coding.dashscope.aliyuncs.com/apps/anthropic/v1/messages` + `TYPE=anthropic`

如果你误填了常见 URL（例如把 OpenAI 兼容地址写成 `.../messages`），系统会做基础归一化处理，但仍建议按 `.env.example` 保持标准写法。

---
*Powered by Golang & OpenClaw Architecture*
