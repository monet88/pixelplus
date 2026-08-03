package httptransport

import (
	"net/http"

	"github.com/monet88/pixelplus/apps/gateway/internal/application"
	"github.com/monet88/pixelplus/apps/gateway/internal/domain"
)

// chatCompletionWire is the canonical, Provider-independent non-streaming
// completion projection. It carries the assistant message, a single
// finish_reason per choice (OpenAI-compatible), usage, and safe x_pixelplus
// metadata (including the PixelPlus finish_class classification). It NEVER leaks
// raw Provider payloads, Provider end-markers, credential material, or foreign
// ids (chat lifecycle §4.2, OpenAI-compatible contract §3.4/§3.6).
type chatCompletionWire struct {
	ID         string          `json:"id"`
	Object     string          `json:"object"`
	Created    int64           `json:"created"`
	Model      string          `json:"model"`
	Choices    []choiceWire    `json:"choices"`
	Usage      usageWire       `json:"usage"`
	Xpixelplus *xpixelplusWire `json:"x_pixelplus"`
}

type choiceWire struct {
	Index        int         `json:"index"`
	Message      messageWire `json:"message"`
	FinishReason string      `json:"finish_reason"`
}

type messageWire struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type usageWire struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// xpixelplusWire is the safe provider-independent metadata block (below the
// openai-compatible core fields). It carries only same-Tenant safe ids plus the
// PixelPlus finish_class classification in its documented metadata location
// (ChatSafeMetadata), distinct from the OpenAI-compatible choices[].finish_reason.
type xpixelplusWire struct {
	RequestID         string `json:"request_id"`
	ExecutionID       string `json:"execution_id"`
	ProviderAccountID string `json:"provider_account_id"`
	FinishClass       string `json:"finish_class"`
}

// toChatCompletionWire projects the canonical completion. It never admits the
// user/system prompt back; only the assistant choice message is exposed. The
// created timestamp is emitted as Unix seconds (integer), not an RFC3339 string,
// per the published ChatCompletionResponse schema.
func toChatCompletionWire(completion domain.ChatCompletion) chatCompletionWire {
	var created int64
	if !completion.Created.IsZero() {
		created = completion.Created.Time().Unix()
	}
	wire := chatCompletionWire{
		ID:      string(completion.ID),
		Object:  completion.Object,
		Created: created,
		Model:   completion.Model,
		Choices: []choiceWire{},
		Usage: usageWire{
			PromptTokens:     completion.Usage.PromptTokens,
			CompletionTokens: completion.Usage.CompletionTokens,
			TotalTokens:      completion.Usage.PromptTokens + completion.Usage.CompletionTokens,
		},
		Xpixelplus: &xpixelplusWire{
			RequestID:         string(completion.RequestID),
			ExecutionID:       string(completion.ExecutionID),
			ProviderAccountID: string(completion.ProviderAccountID),
		},
	}
	for _, choice := range completion.Choices {
		wire.Choices = append(wire.Choices, choiceWire{
			Index: choice.Index,
			Message: messageWire{
				Role:    string(choice.Message.Role),
				Content: choice.Message.Content,
			},
			FinishReason: string(choice.FinishClass),
		})
	}
	if len(completion.Choices) > 0 {
		wire.Xpixelplus.FinishClass = string(completion.Choices[0].FinishClass)
	}
	return wire
}

// writeChatCompletion emits the canonical success completion.
func writeChatCompletion(writer http.ResponseWriter, statusCode int, result application.ChatResult) {
	writeJSON(writer, statusCode, toChatCompletionWire(result.Completion))
}
