package channels

import "context"

// Message is a normalized message from any channel.
type Message struct {
	ID       string
	ChatID   string
	FromUser string
	Content  string
	Channel  string // "feishu" | "telegram" | "dingtalk" | "wecom"
	ChatType string // "p2p" | "group"
	// Optional
	ThreadID  string
	ReplyToID string
}

// Channel is the unified interface all channel implementations must satisfy.
type Channel interface {
	// Name returns the channel identifier (e.g. "feishu", "telegram").
	Name() string
	// Start connects to the messaging platform and begins receiving.
	Start(ctx context.Context) error
	// Stop disconnects gracefully.
	Stop() error
	// Messages returns a channel of incoming messages.
	Messages() <-chan Message
	// Send sends a text message to the given chat.
	Send(ctx context.Context, chatID, text string) error
}

// ReactingChannel is implemented by channels that support message reactions.
type ReactingChannel interface {
	AddReaction(ctx context.Context, messageID, emojiType string) (string, error)
	RemoveReaction(ctx context.Context, messageID, reactionID string) error
}
