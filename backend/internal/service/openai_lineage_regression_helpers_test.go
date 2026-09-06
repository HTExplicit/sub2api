//go:build unit

package service

import "strings"

// markOpenAIWSInvalidEncryptedContentLineage 把本次被上游拒绝的密文摘要写入
// 会话 lineage。digests 须在剥离前收集。
func (s *OpenAIGatewayService) markOpenAIWSInvalidEncryptedContentLineage(groupID int64, sessionHash string, digests []string) {
	if s == nil || len(digests) == 0 || strings.TrimSpace(sessionHash) == "" {
		return
	}
	stateStore := s.getOpenAIWSStateStore()
	if stateStore == nil {
		return
	}
	stateStore.MarkSessionInvalidEncryptedContent(groupID, sessionHash, digests, s.openAIWSSessionStickyTTL())
}

// sessionInvalidEncryptedContentDigests 返回会话已知失效密文摘要；全局无记录
// 时（常态）零成本返回 nil。
func (s *OpenAIGatewayService) sessionInvalidEncryptedContentDigests(groupID int64, sessionHash string) map[string]struct{} {
	if s == nil || strings.TrimSpace(sessionHash) == "" {
		return nil
	}
	stateStore := s.getOpenAIWSStateStore()
	if stateStore == nil || !stateStore.HasAnySessionInvalidEncryptedContent() {
		return nil
	}
	return stateStore.GetSessionInvalidEncryptedContentDigests(groupID, sessionHash)
}
