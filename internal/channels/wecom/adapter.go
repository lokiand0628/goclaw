package wecom

import (
	"context"
	"fmt"
	"log"

	"goclaw/internal/channels"
	"goclaw/internal/config"
)

type Adapter struct {
	cfg      config.WeComConfig
	messages chan channels.Message
}

func New(cfg *config.Config) (*Adapter, error) {
	wc := cfg.Channels.WeCom
	if !wc.Enabled {
		return nil, fmt.Errorf("wecom not enabled")
	}
	// Real implementation would import WeCom SDK
	return &Adapter{
		cfg:      wc,
		messages: make(chan channels.Message, 100),
	}, nil
}

func (w *Adapter) Name() string { return "wecom" }

func (w *Adapter) Start(ctx context.Context) error {
	log.Println("wecom adapter started (stub)")
	return nil
}

func (w *Adapter) Stop() error { return nil }

func (w *Adapter) Messages() <-chan channels.Message { return w.messages }

func (w *Adapter) Send(ctx context.Context, chatID, text string) error {
	log.Printf("[WeCom Stub] Send to %s: %s", chatID, text)
	return nil
}

func (w *Adapter) Reply(ctx context.Context, messageID, text string) error {
	log.Printf("[WeCom Stub] Reply to %s: %s", messageID, text)
	return nil
}
