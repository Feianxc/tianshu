package view

import (
	"github.com/Feianxc/tianshu/server/internal/model"
	"github.com/Feianxc/tianshu/server/internal/rules"
)

// What an agent is told about a summary-only workspace in place of free text.

func genericFinding(rule string) string {
	if p, ok := rules.PlaybookFor(rule); ok {
		return p.Meaning
	}
	return "这个对象有一条待处理事项，细节按空间策略不提供。"
}

func genericReason(state string) string {
	switch state {
	case model.HealthOK:
		return "运行正常"
	case model.HealthDegraded:
		return "部分异常"
	case model.HealthDown:
		return "故障"
	case model.HealthUnknown:
		return "没有足够的证据"
	}
	return ""
}

func genericSignal(state string) string {
	switch state {
	case model.StateOK:
		return "正常"
	case model.StateWarn:
		return "需要注意"
	case model.StateFail:
		return "异常"
	}
	return "未知"
}
