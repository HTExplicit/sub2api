package tickets

import (
	"encoding/json"
	"strconv"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func (o Outcome) MarshalJSON() ([]byte, error) {
	type wire Outcome
	message := map[string]string{
		"ticket_ready": "已取得有效292票据", "ticket_skipped": "已有有效票据，本次跳过", "ticket_stopped": "自动续期已停止",
		"ticket_disabled": "票据功能已关闭", "ticket_model_invalid": "模型不在票据配置范围内", "ticket_proxy_missing": "尚未配置采集代理",
		"ticket_busy": "账号和模型已有进行中的操作", "ticket_interrupted": "前次请求结果未确认，未重复发送", "ticket_not_due": "未到续期时间",
		"ticket_token": "无法取得账号访问令牌", "ticket_transport": "采集连接失败", "ticket_upstream": "上游拒绝采集请求",
		"ticket_length": "票据长度不符合292规则", "ticket_prefix": "票据格式不符合规则", "ticket_persist": "票据保存失败",
		"ticket_stale": "账号主体或操作状态已变化", "ticket_canceled": "操作已取消", "proxy_connection_failed": "代理连接或证书验证失败",
		"pinned_connection_failed": "固定证书验证失败", "invalid_proxy": "代理地址或协议不正确",
	}[o.Code]
	if message == "" {
		message = "操作未完成，请检查配置与账号状态"
	}
	facts := []extensionv1.DisplayFact{}
	if o.HTTPStatus > 0 {
		facts = append(facts, extensionv1.DisplayFact{Label: map[string]string{"zh": "上游状态", "en": "Upstream status"}, Value: strconv.Itoa(o.HTTPStatus)})
	}
	if o.ObservedLength > 0 {
		facts = append(facts, extensionv1.DisplayFact{Label: map[string]string{"zh": "票据长度", "en": "Ticket length"}, Value: strconv.Itoa(o.ObservedLength)})
	}
	if o.ExpiresAt != nil {
		facts = append(facts, extensionv1.DisplayFact{Label: map[string]string{"zh": "有效期至", "en": "Expires"}, Value: o.ExpiresAt.Format(time.RFC3339), Timestamp: true})
	}
	return json.Marshal(struct {
		wire
		Message string                    `json:"message"`
		Facts   []extensionv1.DisplayFact `json:"facts"`
	}{wire(o), message, facts})
}
