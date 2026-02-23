# goclaw (Clawdbot)

`goclaw` 是基于 [OpenClaw]([https://github.com/openclaw](https://github.com/openclaw/openclaw)) 的 Go 重构版本，重点在于用更轻量、稳定、易维护的方式服务真实使用场景。

## 项目背景（爱好者视角）

- 我是非科班开发者，主要通过 WebCode + 多个 AI 编程工具协作完成这个项目。
- 重构的核心动机不是“技术炫技”，而是实用：部署更省心、运行更稳定、维护更可控。
- 所以本文档重点讲使用场景、使用效果和实际取舍，而不是科研式 benchmark 报告。

## 轻量对比方法（非科研版）

我们采用简单但可复用的方式对比 `goclaw` 和 `OpenClaw`：

1. 用同一批真实对话场景跑两个项目（普通问答、工具调用、多轮上下文）。
2. 观察日常使用体验（部署时间、故障概率、调试成本、响应体感）。
3. 记录关键事实（功能是否可用、是否稳定、是否容易排错）。
4. 得出“场景化建议”而不是绝对结论。

## 使用体验对比：goclaw vs OpenClaw

| 你关心的问题 | goclaw (Go) | OpenClaw | 结论建议 |
| :--- | :--- | :--- | :--- |
| 第一次部署麻不麻烦 | 单二进制 + `.env`，步骤更少 | 依赖栈更多，准备时间更长 | 想快速稳定上线选 `goclaw` |
| 日常运行是否省资源 | 内存占用和进程数量更克制 | 功能丰富但运行负担更高 | 小机器/长期运行更偏向 `goclaw` |
| 出问题后是否容易排查 | 运行路径更集中，日志链路更短 | 生态多样，排查时要跨更多层 | 想“少折腾”选 `goclaw` |
| 改功能是否方便 | 需要 Go 改动并重新编译 | JS/TS 改动快，原型迭代灵活 | 想快速试验新玩法选 `OpenClaw` |
| 插件和生态扩展 | 核心能力稳定，扩展偏工程化 | 社区和插件思路更活跃 | 追求生态丰富可优先 `OpenClaw` |

## 为什么要用 Go 重构

重构不是否定 OpenClaw，而是为另一类需求提供答案：

- 目标是长期运行：更看重稳定性、资源效率、可维护性。
- 目标是低门槛部署：一份二进制，在服务器和容器都好落地。
- 目标是可控演进：核心路径收敛，便于后续逐步加功能。

换句话说：`OpenClaw` 更像灵活的创新底座，`goclaw` 更像稳定的运行底座。

## 当前已具备能力

- 多渠道接入：Telegram、飞书、钉钉、企业微信。
- 多 Provider 兼容：OpenAI / Anthropic 协议。
- 对话记忆持久化：SQLite 本地存储。
- 常用管理能力：`/status`、`/clear` 等。
- 工具安全守护：对命令执行和路径访问有约束策略。

## 快速上手

### 1. 准备配置

```bash
cp .env.example .env
```

按注释填入 API Key 和模型配置。

### 2. 编译运行

```bash
go build -o clawdbot ./cmd/clawdbot/
./clawdbot start
```

或使用 Docker：

```bash
docker-compose up -d
```

### 3. 建议先做一次检查

```bash
make check
```

## 近期优化重点

- 可靠性：补齐 Provider 回归测试，增强 SSE 解析兼容性。
- 一致性：调度与存储路径收敛到数据库单一事实源。
- 可观测性：关键链路补齐结构化日志。
- 安全性：Bash 写操作和受保护路径联合检测。

## 更新记录（基于 Git）

- 最后更新时间：`2026-02-23 01:59:26 +0800`
- 当前提交：`01d08bf`
- 本次完成事项：
  - 修复备份流程中的 Git 身份配置问题。
  - 完成 P0-P2（可靠性/存储/可观测性/安全）加固。
  - 增加 Provider 回归测试并同步文档。
  - 修复 qwen/kimi 兼容和 SSE 解析鲁棒性问题。
- 最近提交：
  - `2026-02-23 01:59:26 +0800` `01d08bf` `Fix backup git identity config`
  - `2026-02-23 01:16:10 +0800` `4896224` `feat: complete p0-p2 hardening for reliability, storage, and observability`
  - `2026-02-23 01:09:39 +0800` `cf5129d` `test+docs: add provider regression checks and update README`
  - `2026-02-23 01:03:23 +0800` `d18b296` `fix(provider): restore qwen/kimi compatibility and robust SSE parsing`

后续每次发布前，建议用 `git log` 刷新这个区块，保持 README 与实际进度一致。
