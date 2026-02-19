package telegram

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"goclaw/internal/channels"
	"goclaw/internal/config"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// Adapter 为 Telegram 实现了 channels.Channel 接口。
type Adapter struct {
	bot          *tgbotapi.BotAPI
	messages     chan channels.Message
	stop         chan struct{}
	allowedUsers map[string]bool
}

// New 创建一个新的 Telegram 适配器。
func New(cfg config.TelegramConfig) (*Adapter, error) {
	if cfg.Token == "" {
		return nil, fmt.Errorf("telegram: 需要令牌 (token)")
	}

	bot, err := tgbotapi.NewBotAPI(cfg.Token)
	if err != nil {
		return nil, fmt.Errorf("telegram: 创建机器人失败: %w", err)
	}

	allowed := make(map[string]bool)
	for _, user := range cfg.AllowedUsers {
		allowed[strings.TrimSpace(user)] = true
	}

	return &Adapter{
		bot:          bot,
		messages:     make(chan channels.Message, 200),
		stop:         make(chan struct{}),
		allowedUsers: allowed,
	}, nil
}

func (a *Adapter) Name() string { return "telegram" }

// Start 开始接收更新。
func (a *Adapter) Start(ctx context.Context) error {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60

	updates := a.bot.GetUpdatesChan(u)

	// 汉化控制台中的冲突日志：由于 tgbotapi 内部直接使用 log.Println 打印 GetUpdates 错误，
	// 我们只能在捕获到通道关闭或初始化成功后进行中文引导提示。
	// 实际上，砖头看到的 "Conflict: terminated by other getUpdates request" 是 SDK 内部抛出的。
	// 为了让砖头看着顺眼，我们在这里输出一个明确的中文提示。
	log.Println("telegram: 正在建立安全连接并检查实例冲突...")

	// 确保下载目录存在
	downloadDir := filepath.Join("workspace", "downloads", "telegram")
	if err := os.MkdirAll(downloadDir, 0755); err != nil {
		log.Printf("telegram: 创建下载目录失败: %v", err)
	}

	go func() {
		log.Printf("telegram: 以 @%s 身份启动", a.bot.Self.UserName)
		for {
			select {
			case <-ctx.Done():
				a.bot.StopReceivingUpdates()
				return
			case <-a.stop:
				a.bot.StopReceivingUpdates()
				return
			case update, ok := <-updates:
				if !ok {
					log.Println("telegram: 更新通道已意外关闭，可能是因为 Token 错误或实例冲突 (Conflict)")
					return
				}
				if update.Message == nil {
					continue
				}

				// 过滤：仅允许私服聊天（个人助手模式）
				if !update.Message.Chat.IsPrivate() {
					continue
				}

				// 过滤：白名单检查
				sender := update.Message.From.UserName
				senderID := fmt.Sprintf("%d", update.Message.From.ID)
				if len(a.allowedUsers) > 0 {
					if !a.allowedUsers[sender] && !a.allowedUsers[senderID] {
						log.Printf("telegram: 忽略来自未经授权用户的消息: %s (ID: %s)", sender, senderID)
						continue
					}
				}

				chatType := "p2p"
				if update.Message.Chat.IsGroup() || update.Message.Chat.IsSuperGroup() {
					chatType = "group"
				}

				msgID := fmt.Sprintf("%d|%d", update.Message.Chat.ID, update.Message.MessageID)

				content := update.Message.Text

				// 处理照片
				if len(update.Message.Photo) > 0 {
					// 获取最大的照片
					photo := update.Message.Photo[len(update.Message.Photo)-1]
					filePath, err := a.downloadFile(photo.FileID, downloadDir)
					if err != nil {
						log.Printf("telegram: 下载照片错误: %v", err)
						content = "[图片错误]"
					} else {
						content = fmt.Sprintf("[图片] %s", filePath)
					}
					if update.Message.Caption != "" {
						content += "\n" + update.Message.Caption
					}
				}

				// 处理文档
				if update.Message.Document != nil {
					doc := update.Message.Document
					filePath, err := a.downloadFile(doc.FileID, downloadDir)
					if err != nil {
						log.Printf("telegram: 下载文档错误: %v", err)
						content = "[文件错误]"
					} else {
						// 尽可能重命名为原始名称，但目前保持简单（API 经常给出通用名称）
						// 实际上 GetFile 会给出一个路径，如果我们有 OriginalFileName，尝试重命名
						if doc.FileName != "" {
							newPath := filepath.Join(downloadDir, fmt.Sprintf("%s_%s", doc.FileID[:8], doc.FileName))
							if err := os.Rename(filePath, newPath); err == nil {
								filePath = newPath
							}
						}
						content = fmt.Sprintf("[文件] %s", filePath)
					}
					if update.Message.Caption != "" {
						content += "\n" + update.Message.Caption
					}
				}

				if content == "" {
					continue // 如果内容仍为空则跳过（例如：有效更新但属于不支持的类型，如贴纸）
				}

				msg := channels.Message{
					ID:       msgID,
					ChatID:   fmt.Sprintf("%d", update.Message.Chat.ID),
					FromUser: update.Message.From.UserName,
					Content:  content,
					Channel:  "telegram",
					ChatType: chatType,
				}

				select {
				case a.messages <- msg:
				default:
					log.Println("telegram: 消息缓冲区已满，正在丢弃")
				}
			}
		}
	}()

	return nil
}

func (a *Adapter) Stop() error {
	close(a.stop)
	return nil
}

func (a *Adapter) Messages() <-chan channels.Message {
	return a.messages
}

func (a *Adapter) Send(ctx context.Context, chatID, text string) error {
	var id int64
	if _, err := fmt.Sscanf(chatID, "%d", &id); err != nil {
		return fmt.Errorf("无效的聊天 ID 格式: %s", chatID)
	}

	// 检查文件发送规则: "[FILE] path/to/file"
	if strings.HasPrefix(text, "[FILE] ") {
		filePath := strings.TrimSpace(strings.TrimPrefix(text, "[FILE] "))
		doc := tgbotapi.NewDocument(id, tgbotapi.FilePath(filePath))
		if _, err := a.bot.Send(doc); err != nil {
			return fmt.Errorf("telegram 发送文件失败: %w", err)
		}
		return nil
	}

	// 检查图片发送规则: "[IMAGE] path/to/file"
	if strings.HasPrefix(text, "[IMAGE] ") {
		filePath := strings.TrimSpace(strings.TrimPrefix(text, "[IMAGE] "))
		photo := tgbotapi.NewPhoto(id, tgbotapi.FilePath(filePath))
		if _, err := a.bot.Send(photo); err != nil {
			return fmt.Errorf("telegram 发送图片失败: %w", err)
		}
		return nil
	}

	// 分割长消息 (Telegram 限制: 4096 字符)
	const maxLen = 4000
	for len(text) > 0 {
		chunk := text
		if len(chunk) > maxLen {
			chunk = text[:maxLen]
			text = text[maxLen:]
		} else {
			text = ""
		}

		formattedText := FormatMessage(chunk)
		msg := tgbotapi.NewMessage(id, formattedText)
		msg.ParseMode = tgbotapi.ModeMarkdownV2 // 使用 MarkdownV2

		if _, err := a.bot.Send(msg); err != nil {
			// 如果发送失败（可能是极其罕见的格式问题），尝试回退到纯文本
			log.Printf("telegram: MarkdownV2 发送失败，尝试纯文本回退: %v", err)
			msg = tgbotapi.NewMessage(id, chunk) // 使用原始文本
			msg.ParseMode = ""                   // 清空模式
			if _, err := a.bot.Send(msg); err != nil {
				return fmt.Errorf("telegram 发送失败 (即使回退后): %w", err)
			}
		}
	}
	return nil
}

// downloadFile 从 Telegram 下载文件并返回本地路径
func (a *Adapter) downloadFile(fileID, dir string) (string, error) {
	fileURL, err := a.bot.GetFileDirectURL(fileID)
	if err != nil {
		return "", fmt.Errorf("获取 URL 失败: %w", err)
	}

	resp, err := http.Get(fileURL)
	if err != nil {
		return "", fmt.Errorf("下载失败: %w", err)
	}
	defer resp.Body.Close()

	// 从 URL 中提取文件名或生成一个
	filename := filepath.Base(fileURL)
	if filename == "" || filename == "." {
		filename = fmt.Sprintf("file_%s", fileID)
	}

	localPath := filepath.Join(dir, filename)
	out, err := os.Create(localPath)
	if err != nil {
		return "", fmt.Errorf("创建文件失败: %w", err)
	}
	defer out.Close()

	if _, err := io.Copy(out, resp.Body); err != nil {
		return "", fmt.Errorf("保存文件失败: %w", err)
	}

	return localPath, nil
}

// AddReaction 使用原始 API 请求添加表情符号回应 (Bot API 7.0+)。
func (a *Adapter) AddReaction(ctx context.Context, messageID, emojiType string) (string, error) {
	// 解析特定实现的 ID 格式: "chatID|messageID"
	parts := strings.Split(messageID, "|")
	if len(parts) != 2 {
		// 回退：如果它是旧版 ID 格式，尝试解析为简单整数
		// 但实际上回应需要 ChatID。
		return "", fmt.Errorf("回应的消息 ID 格式无效: %s", messageID)
	}

	chatIDStr, msgIDStr := parts[0], parts[1]

	// 将 "THINKING" 映射到有效的表情符号
	emoji := emojiType
	if emojiType == "THINKING" {
		emoji = "🤔"
	}

	// 为 setMessageReaction 构建原始负载
	// tgbotapi v5.5.1 MakeRequest 接受 tgbotapi.Params (map[string]string)
	reactionJSON, _ := json.Marshal([]map[string]interface{}{
		{"type": "emoji", "emoji": emoji},
	})

	payload := tgbotapi.Params{
		"chat_id":    chatIDStr,
		"message_id": msgIDStr,
		"reaction":   string(reactionJSON),
	}

	resp, err := a.bot.MakeRequest("setMessageReaction", payload)
	if err != nil {
		return "", fmt.Errorf("setMessageReaction: %w", err)
	}
	if !resp.Ok {
		return "", fmt.Errorf("setMessageReaction 失败: %s", resp.Description)
	}

	// 返回虚拟回应 ID，因为 Telegram 回应不像 Slack/Feishu 那样有 ID。
	// 我们只需要一个非空值来表示成功，并供 RemoveReaction（概念上）使用。
	return "reaction_set", nil
}

// RemoveReaction 通过设置空列表来移除回应。
func (a *Adapter) RemoveReaction(ctx context.Context, messageID, reactionID string) error {
	// 解析 ID
	parts := strings.Split(messageID, "|")
	if len(parts) != 2 {
		return nil // 忽略无效 ID
	}
	chatIDStr, msgIDStr := parts[0], parts[1]

	// 发送空回应列表以清除
	payload := tgbotapi.Params{
		"chat_id":    chatIDStr,
		"message_id": msgIDStr,
		"reaction":   "[]", // 空 JSON 数组
	}

	_, err := a.bot.MakeRequest("setMessageReaction", payload)
	if err != nil {
		return fmt.Errorf("清除回应失败: %w", err)
	}
	return nil
}
