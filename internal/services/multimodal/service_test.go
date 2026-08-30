package multimodal

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/schema"

	modeladapter "github.com/phlin/go-agent/internal/adapters/model"
	"github.com/phlin/go-agent/internal/config"
	mediadomain "github.com/phlin/go-agent/internal/domain/media"
)

func TestUnderstandUsesUserInputMultiContent(t *testing.T) {
	mock := modeladapter.NewMockChatModel(schema.AssistantMessage(`{"summary":"熊猫头表情包","scene_tags":["meme"],"confidence":0.8}`, nil))
	service := New(modeladapter.StaticFactory{VisionModel: mock}, config.MultimodalConfig{})

	descriptors, err := service.Understand(context.Background(), []mediadomain.MultimodalAttachment{{
		AttachmentID: "a1",
		Kind:         mediadomain.MediaSticker,
		URL:          "https://example.com/a1.webp",
		MIME:         "image/webp",
	}})
	if err != nil {
		t.Fatalf("understand: %v", err)
	}
	if len(descriptors) != 1 || descriptors[0].Summary != "熊猫头表情包" {
		t.Fatalf("unexpected descriptors: %#v", descriptors)
	}

	inputs := mock.Inputs()
	if len(inputs) == 0 || len(inputs[0]) < 2 || len(inputs[0][1].UserInputMultiContent) == 0 {
		t.Fatalf("expected multimodal input")
	}
}
