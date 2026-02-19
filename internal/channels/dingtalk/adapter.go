package dingtalk

import (
	"context"
	"fmt"
	"log"

	"goclaw/internal/channels"
	"goclaw/internal/config"
)

type Adapter struct {
	cfg      config.DingTalkConfig
	messages chan channels.Message
}

func New(cfg *config.Config) (*Adapter, error) {
	dc := cfg.Channels.DingTalk
	if !dc.Enabled {
		return nil, fmt.Errorf("dingtalk not enabled")
	}
	// Real implementation would need DingTalk SDK or HTTP client setup
	return &Adapter{
		cfg:      dc,
		messages: make(chan channels.Message, 100),
	}, nil
}

func (d *Adapter) Name() string { return "dingtalk" }

func (d *Adapter) Start(ctx context.Context) error {
	log.Println("dingtalk adapter started (stub)")
	// In a real implementation, this would start an HTTP server for callbacks
	// or poll for messages.
	return nil
}

func (d *Adapter) Stop() error { return nil }

func (d *Adapter) Messages() <-chan channels.Message { return d.messages }

func (d *Adapter) Send(ctx context.Context, chatID, text string) error {
	// Real implementation would call DingTalk API
	log.Printf("[DingTalk Stub] Send to %s: %s", chatID, text)
	return nil
}

func (d *Adapter) Reply(ctx context.Context, messageID, text string) error {
	log.Printf("[DingTalk Stub] Reply to %s: %s", messageID, text)
	return nil
}
