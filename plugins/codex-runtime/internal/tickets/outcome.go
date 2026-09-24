package tickets

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	extensionv1 "github.com/Wei-Shaw/sub2api/pkg/extensionapi/v1"
)

func (o Outcome) MarshalJSON() ([]byte, error) {
	type wire Outcome
	message := map[string]string{
		"routing_verified": "业务出口响应完整、模型声明一致；本次未验证质量", "routing_candidate": "已取得候选路由材料，尚未通过业务出口验证",
		"routing_model_mismatch": "上游模型声明不匹配，未发布路由资格", "routing_incomplete": "响应未完整结束或缺少模型声明，未发布路由资格",
		"routing_capacity": "上游容量不足或流内限流，未完成路由验证", "routing_policy": "上游策略检查阻止请求，未完成路由验证",
		"routing_cancelled": "客户端取消，本次响应未完成验证", "routing_legacy_retired": "旧 STATE 材料已丢弃，尚未获得路由资格",
		"routing_cookie_missing": "响应未给出可用路由 Cookie", "routing_cookie_expired": "Cookie 已到期，需要重新验证",
		"routing_transport": "采集或业务出口验证连接失败", "routing_stale": "账号主体、出口、指纹或 Cookie 代次已变化",
		"routing_cookie_deleted": "上游已撤销路由 Cookie", "routing_budget_spent": "本次验证的固定调用预算已使用",
		"routing_upstream": "上游拒绝路由验证请求", "routing_connection_unknown": "无法绑定经过验证的实际连接",
		"ticket_ready": "历史 STATE 采集记录，不代表当前路由资格", "ticket_skipped": "已有路由验证记录，本次跳过；本次未验证质量", "ticket_stopped": "路由自动续期已停止",
		"ticket_disabled": "路由采集与验证已关闭", "ticket_model_invalid": "模型不在路由配置范围内", "ticket_proxy_missing": "尚未配置采集代理",
		"ticket_missing": "该模型尚无有效路由资格",
		"ticket_busy":    "账号和模型已有进行中的操作", "ticket_interrupted": "前次请求结果未确认，未重复发送", "ticket_not_due": "未到续期时间",
		"ticket_token": "无法取得账号访问令牌", "ticket_transport": "采集连接失败", "ticket_upstream": "上游拒绝采集请求",
		"ticket_length": "历史 STATE 长度检查失败（该规则已停用）", "ticket_prefix": "历史 STATE 格式检查失败（该规则已停用）", "ticket_persist": "路由验证记录保存失败",
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
		facts = append(facts, extensionv1.DisplayFact{Label: map[string]string{"zh": "STATE 长度（仅观测）", "en": "STATE length (observation only)"}, Value: strconv.Itoa(o.ObservedLength)})
	}
	if observation := o.Observation; observation != nil {
		material, completed, matched := "未证实有效", "未证实完整", "未知"
		if o.Code == "routing_candidate" {
			material = "候选材料，待业务出口验证"
		} else if o.Code == "routing_verified" && o.Success {
			material = "验证时有效"
		}
		if observation.Completed {
			completed = "完整结束"
		}
		if observation.ResponseModel != "" {
			matched = "不一致"
			if observation.ModelMatched {
				matched = "一致"
			}
			facts = append(facts, extensionv1.DisplayFact{Label: map[string]string{"zh": "上游模型声明", "en": "Upstream model declaration"}, Value: observation.ResponseModel})
		}
		facts = append(facts,
			extensionv1.DisplayFact{Label: map[string]string{"zh": "路由材料", "en": "Routing material"}, Value: material},
			extensionv1.DisplayFact{Label: map[string]string{"zh": "完整响应", "en": "Response completion"}, Value: completed},
			extensionv1.DisplayFact{Label: map[string]string{"zh": "模型声明匹配", "en": "Model declaration match"}, Value: matched})
	}
	if strings.HasPrefix(o.Code, "routing_") || o.Code == "ticket_skipped" || o.Code == "ticket_ready" {
		facts = append(facts, extensionv1.DisplayFact{Label: map[string]string{"zh": "质量验证", "en": "Quality validation"}, Value: "本次未验证 / Not tested by this operation"})
	}
	if o.ExpiresAt != nil {
		facts = append(facts, extensionv1.DisplayFact{Label: map[string]string{"zh": "路由记录有效期至", "en": "Routing record expires"}, Value: o.ExpiresAt.Format(time.RFC3339), Timestamp: true})
	}
	return json.Marshal(struct {
		wire
		Message string                    `json:"message"`
		Facts   []extensionv1.DisplayFact `json:"facts"`
	}{wire(o), message, facts})
}
