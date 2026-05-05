package cmd

import (
	"context"

	"wr/internal/pushover"
	"wr/internal/scheduler"
)

// pushoverBridge adapts a *pushover.Client to the scheduler.PushoverSender
// interface by converting scheduler.PushoverConfig to pushover.Config.
type pushoverBridge struct {
	client *pushover.Client
}

func (b *pushoverBridge) Send(ctx context.Context, cfg scheduler.PushoverConfig, message, title string, priority int) error {
	return b.client.Send(ctx, pushover.Config{APIToken: cfg.APIToken, UserKey: cfg.UserKey}, message, title, priority)
}
