package app

import (
	"context"
	"time"

	"github.com/cloudwego/eino/schema"
	modeladapter "github.com/phlin/go-agent/internal/adapters/model"
	"github.com/phlin/go-agent/internal/app/admin"
)

func probeProviders(ctx context.Context, factory *modeladapter.Factory, health *admin.CapabilityHealth, mainReady, vectorReady bool) error {
	now := time.Now()
	if mainReady {
		var err error
		chat, chatErr := factory.MainChatModel(ctx)
		if chatErr == nil {
			_, err = chat.Generate(ctx, []*schema.Message{schema.UserMessage("ping")})
		} else {
			err = chatErr
		}
		health.RecordMainModel(err == nil, now)
	}
	if vectorReady {
		var err error
		embed, embedErr := factory.EmbeddingModel(ctx)
		if embedErr == nil {
			_, err = embed.EmbedStrings(ctx, []string{"ping"})
		} else {
			err = embedErr
		}
		health.RecordVector(err == nil, now)
	}
	return nil
}
