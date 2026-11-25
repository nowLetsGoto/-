package rdc

import (
	"testing"

	"dandelion/server/internal/contract"
)

// 上游 200 但完全不带 usage 字段（部分兼容实现真实行为）
func TestProbeUsageMissingSetsReportedFalse(t *testing.T) {
	for _, protocol := range contract.AIProtocols {
		t.Run(protocol, func(t *testing.T) {
			var reply string
			switch protocol {
			case contract.AIProtocolOpenAI:
				reply = `{"choices":[{"message":{"content":"OK"}}]}`
			case contract.AIProtocolAnthropic:
				reply = `{"content":[{"type":"text","text":"OK"}]}`
			default:
				reply = `{"output_text":"OK"}`
			}
			record, _ := chatOnceFixture(t, protocol, reply)
			result, err := runChatOnce(t, record, conversation())
			if err != nil {
				t.Fatalf("应成功: %v", err)
			}
			t.Logf("%s: TokensReported=%v prompt=%d completion=%d", protocol, result.TokensReported, result.PromptTokens, result.CompletionTokens)
			if result.TokensReported {
				t.Fatalf("%s: 上游未返回 usage 时 TokensReported 必须为 false（否则 0 被当真实用量），实际 true", protocol)
			}
		})
	}
}
