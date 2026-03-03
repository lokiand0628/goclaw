package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"

	"goclaw/internal/channels"
	"goclaw/internal/config"

	lark "github.com/larksuite/oapi-sdk-go/v3"
	larkcore "github.com/larksuite/oapi-sdk-go/v3/core"
	"github.com/larksuite/oapi-sdk-go/v3/event/dispatcher"
	larkim "github.com/larksuite/oapi-sdk-go/v3/service/im/v1"
	larkws "github.com/larksuite/oapi-sdk-go/v3/ws"
)

// Adapter wraps the Lark SDK to implement channels.Channel.
type Adapter struct {
	client   *lark.Client
	wsClient *larkws.Client
	cfg      config.FeishuConfig
	messages chan channels.Message
}

func New(cfg *config.Config) (*Adapter, error) {
	fc := cfg.Channels.Feishu
	if fc.AppID == "" || fc.AppSecret == "" {
		return nil, fmt.Errorf("feishu: appId and appSecret required")
	}

	opts := []lark.ClientOptionFunc{
		lark.WithLogLevel(larkcore.LogLevelWarn),
	}
	if strings.TrimSpace(fc.OpenBaseURL) != "" {
		opts = append(opts, lark.WithOpenBaseUrl(strings.TrimSpace(fc.OpenBaseURL)))
	} else if fc.Domain == "lark" {
		opts = append(opts, lark.WithOpenBaseUrl("https://open.larksuite.com"))
	}

	client := lark.NewClient(fc.AppID, fc.AppSecret, opts...)
	return &Adapter{
		client:   client,
		cfg:      fc,
		messages: make(chan channels.Message, 200),
	}, nil
}

func (f *Adapter) Name() string { return "feishu" }

func (f *Adapter) Start(ctx context.Context) error {
	eventDispatcher := dispatcher.NewEventDispatcher("", "").
		OnP2MessageReceiveV1(func(ctx context.Context, event *larkim.P2MessageReceiveV1) error {
			if event.Event == nil || event.Event.Message == nil {
				return nil
			}
			msg := f.parseEvent(event)
			if msg.Content == "" {
				return nil // skip empty messages
			}
			select {
			case f.messages <- msg:
			default:
				log.Println("feishu: 消息缓冲区已满，正在丢弃")
			}
			return nil
		})

	wsOpts := []larkws.ClientOption{
		larkws.WithLogLevel(larkcore.LogLevelWarn),
		larkws.WithEventHandler(eventDispatcher),
	}
	if f.cfg.Domain == "lark" {
		wsOpts = append(wsOpts, larkws.WithDomain("https://open.larksuite.com"))
	}

	f.wsClient = larkws.NewClient(f.cfg.AppID, f.cfg.AppSecret, wsOpts...)
	go func() {
		if err := f.wsClient.Start(ctx); err != nil && ctx.Err() == nil {
			log.Printf("feishu: WebSocket 连接错误: %v", err)
		}
	}()
	return nil
}

func (f *Adapter) Stop() error { return nil }

func (f *Adapter) Messages() <-chan channels.Message { return f.messages }

// Send sends a text message to the given chatID.
// chatID format: "chat_id:oc_xxx" (group/p2p) or "open_id:ou_xxx" (direct user)
// For compatibility, bare "oc_xxx" is treated as chat_id, "ou_xxx" as open_id.
func (f *Adapter) Send(ctx context.Context, chatID, text string) error {
	receiveIDType, receiveID := parseFeishuChatID(chatID)

	// Build Post message content (rich text, supports markdown-ish formatting)
	contentJSON := buildPostContent(text)

	req := larkim.NewCreateMessageReqBuilder().
		ReceiveIdType(receiveIDType).
		Body(larkim.NewCreateMessageReqBodyBuilder().
			ReceiveId(receiveID).
			MsgType(larkim.MsgTypePost).
			Content(contentJSON).
			Build()).
		Build()

	resp, err := f.client.Im.Message.Create(ctx, req)
	if err != nil {
		return fmt.Errorf("feishu send: %w", err)
	}
	if resp.Code != 0 {
		return fmt.Errorf("feishu send: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

// Reply sends a message in reply to a specific message thread.
func (f *Adapter) Reply(ctx context.Context, messageID, text string) error {
	contentJSON := buildPostContent(text)

	req := larkim.NewReplyMessageReqBuilder().
		MessageId(messageID).
		Body(larkim.NewReplyMessageReqBodyBuilder().
			MsgType(larkim.MsgTypePost).
			Content(contentJSON).
			Build()).
		Build()

	resp, err := f.client.Im.Message.Reply(ctx, req)
	if err != nil {
		return fmt.Errorf("feishu reply: %w", err)
	}
	if resp.Code != 0 {
		return fmt.Errorf("feishu reply: code=%d msg=%s", resp.Code, resp.Msg)
	}
	return nil
}

// parseFeishuChatID determines the ReceiveIdType from the chatID format.
// Feishu IDs:
//   - oc_xxx  → chat_id  (group or p2p chat)
//   - ou_xxx  → open_id  (individual user)
//   - "chat_id:oc_xxx" → explicit override
func parseFeishuChatID(chatID string) (string, string) {
	if strings.HasPrefix(chatID, "chat_id:") {
		return larkim.ReceiveIdTypeChatId, strings.TrimPrefix(chatID, "chat_id:")
	}
	if strings.HasPrefix(chatID, "open_id:") {
		return larkim.ReceiveIdTypeOpenId, strings.TrimPrefix(chatID, "open_id:")
	}
	// Auto-detect by prefix
	if strings.HasPrefix(chatID, "oc_") {
		return larkim.ReceiveIdTypeChatId, chatID
	}
	if strings.HasPrefix(chatID, "ou_") {
		return larkim.ReceiveIdTypeOpenId, chatID
	}
	// Default: treat as chat_id
	return larkim.ReceiveIdTypeChatId, chatID
}

// buildPostContent converts plain text to Feishu Post message JSON.
// Splits by newline to preserve formatting.
func buildPostContent(text string) string {
	lines := strings.Split(text, "\n")
	var blocks [][]map[string]string
	for _, line := range lines {
		if line == "" {
			blocks = append(blocks, []map[string]string{}) // empty line
		} else {
			blocks = append(blocks, []map[string]string{
				{"tag": "text", "text": line},
			})
		}
	}

	content, _ := json.Marshal(map[string]interface{}{
		"zh_cn": map[string]interface{}{
			"content": blocks,
		},
	})
	return string(content)
}

func (f *Adapter) parseEvent(event *larkim.P2MessageReceiveV1) channels.Message {
	e := event.Event
	msg := channels.Message{Channel: "feishu"}

	if e.Message != nil {
		if e.Message.MessageId != nil {
			msg.ID = *e.Message.MessageId
		}
		// Use chat_id as the reply target (oc_xxx prefix)
		if e.Message.ChatId != nil {
			msg.ChatID = *e.Message.ChatId
		}
		if e.Message.ChatType != nil {
			msg.ChatType = *e.Message.ChatType
		}
		if e.Message.Content != nil && e.Message.MessageType != nil {
			msg.Content = parseFeishuText(*e.Message.Content, *e.Message.MessageType)
		}
		// Store parent_id for thread replies
		if e.Message.ParentId != nil {
			msg.ThreadID = *e.Message.ParentId
		}
		// ReplyToID = this message's ID (for threading)
		if e.Message.MessageId != nil {
			msg.ReplyToID = *e.Message.MessageId
		}
	}
	if e.Sender != nil && e.Sender.SenderId != nil && e.Sender.SenderId.OpenId != nil {
		msg.FromUser = *e.Sender.SenderId.OpenId
	}
	return msg
}

func parseFeishuText(content, msgType string) string {
	switch msgType {
	case "text":
		var t struct {
			Text string `json:"text"`
		}
		if json.Unmarshal([]byte(content), &t) == nil {
			return strings.TrimSpace(t.Text)
		}
	case "post":
		// Extract plain text from post blocks
		var post struct {
			ZhCn struct {
				Content [][]struct {
					Tag  string `json:"tag"`
					Text string `json:"text"`
				} `json:"content"`
			} `json:"zh_cn"`
		}
		if json.Unmarshal([]byte(content), &post) == nil {
			var sb strings.Builder
			for _, line := range post.ZhCn.Content {
				for _, block := range line {
					if block.Tag == "text" {
						sb.WriteString(block.Text)
					}
				}
				sb.WriteString("\n")
			}
			return strings.TrimSpace(sb.String())
		}
	case "image":
		var img struct {
			ImageKey string `json:"image_key"`
		}
		if json.Unmarshal([]byte(content), &img) == nil {
			return fmt.Sprintf("[Image: %s]", img.ImageKey)
		}
	}
	return content
}

// AddReaction adds an emoji reaction to a message.
// Returns the reactionID which can be used to remove it later.
func (f *Adapter) AddReaction(ctx context.Context, messageID, emojiType string) (string, error) {
	req := larkim.NewCreateMessageReactionReqBuilder().
		MessageId(messageID).
		Body(larkim.NewCreateMessageReactionReqBodyBuilder().
			ReactionType(&larkim.Emoji{
				EmojiType: &emojiType,
			}).
			Build()).
		Build()

	resp, err := f.client.Im.MessageReaction.Create(ctx, req)
	if err != nil {
		return "", fmt.Errorf("add reaction: %w", err)
	}
	if resp.Code != 0 {
		return "", fmt.Errorf("add reaction: code=%d %s", resp.Code, resp.Msg)
	}
	if resp.Data != nil && resp.Data.ReactionId != nil {
		return *resp.Data.ReactionId, nil
	}
	return "", nil
}

// RemoveReaction deletes an emoji reaction from a message.
func (f *Adapter) RemoveReaction(ctx context.Context, messageID, reactionID string) error {
	req := larkim.NewDeleteMessageReactionReqBuilder().
		MessageId(messageID).
		ReactionId(reactionID).
		Build()

	resp, err := f.client.Im.MessageReaction.Delete(ctx, req)
	if err != nil {
		return fmt.Errorf("remove reaction: %w", err)
	}
	if resp.Code != 0 {
		return fmt.Errorf("remove reaction: code=%d %s", resp.Code, resp.Msg)
	}
	return nil
}
