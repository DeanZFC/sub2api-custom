package qqbot

import (
	"context"
	"strings"
)

const (
	helpColumns      = 2
	helpQuickActions = 8 // Keep the keyboard to four rows; all commands appear in the text.
	helpActionPrefix = "qqhelp:v3:"
)

type messageKeyboard struct {
	Content keyboardContent `json:"content"`
}
type keyboardContent struct {
	Rows []keyboardRow `json:"rows"`
}
type keyboardRow struct {
	Buttons []keyboardButton `json:"buttons"`
}
type keyboardButton struct {
	ID         string             `json:"id"`
	RenderData keyboardRenderData `json:"render_data"`
	Action     keyboardAction     `json:"action"`
}
type keyboardRenderData struct {
	Label        string `json:"label"`
	VisitedLabel string `json:"visited_label"`
	Style        int    `json:"style"`
}
type keyboardAction struct {
	Type            int                `json:"type"`
	Permission      keyboardPermission `json:"permission"`
	Data            string             `json:"data"`
	UnsupportedTips string             `json:"unsupport_tips"`
}
type keyboardPermission struct {
	Type int `json:"type"`
}

type helpItem struct {
	action, label, description, command, usage string
	input                                      bool
}

func helpSystemItems() []helpItem {
	return []helpItem{
		{action: "checkin", label: "每日签到", description: "每日签到", command: "/签到"},
		{action: "balance", label: "我的余额", description: "查询账户余额", command: "/我的余额"},
		{action: "today_usage", label: "今日用量", description: "查看今日用量", command: "/今日用量"},
		{action: "rewards", label: "我的奖励", description: "查看累计奖励", command: "/我的奖励"},
		{action: "lottery", label: "抽奖一次", description: "消耗一次抽奖次数", command: "/抽奖"},
		{action: "lottery_count", label: "抽奖次数", description: "查询剩余抽奖次数", command: "/抽奖次数"},
		{action: "lottery_info", label: "抽奖活动", description: "查看抽奖活动规则", command: "/抽奖活动"},
		{action: "growth", label: "成长任务", description: "查看成长进度和奖励", command: "/成长任务"},
		{action: "invite_rewards", label: "邀请奖励", description: "查看邀请奖励规则", command: "/邀请奖励"},
		{action: "invites", label: "我的邀请", description: "查看邀请状态", command: "/我的邀请"},
		{action: "rankings", label: "今日排行榜", description: "查看今日排行榜", command: "/今日排行榜"},
		{action: "channels", label: "渠道状态", description: "查看渠道状态", command: "/渠道状态"},
		{action: "image", label: "生成图片", description: "根据描述生成图片", command: "/生图", usage: "/生图 图片描述", input: true},
		{action: "prices", label: "模型价格", description: "查询模型价格", command: "/价格", usage: "/价格 模型名", input: true},
		{action: "bind", label: "绑定账号", description: "绑定网站账号", command: "/绑定", usage: "/绑定 邮箱或绑定码", input: true},
		{action: "transfer", label: "余额转账", description: "申请余额转账", command: "/转账", usage: "/转账 金额 邮箱", input: true},
	}
}

func helpItems(c Config) []helpItem {
	items := helpSystemItems()
	for _, custom := range c.CustomCommands {
		if custom.Enabled && !isBuiltinCommand(strings.TrimPrefix(custom.Command, "/")) && !isRetiredCommand(strings.TrimPrefix(custom.Command, "/")) {
			items = append(items, helpItem{action: "custom:" + custom.Command, label: custom.Command, description: "自定义回复", command: custom.Command})
		}
	}
	return items
}

func helpButtonID(action string) string { return "help_" + codeHash(action)[:16] }

func helpCommandButton(label, action, command string) keyboardButton {
	label = limitText(label, 9)
	return keyboardButton{
		ID:         helpButtonID(action),
		RenderData: keyboardRenderData{Label: label, VisitedLabel: label, Style: 1},
		// Group callbacks always show QQ's result toast. Command buttons avoid
		// that callback; group clients require the user to send the filled command.
		Action: keyboardAction{Type: 2, Permission: keyboardPermission{Type: 2}, Data: command, UnsupportedTips: "请升级 QQ，或手动发送对应指令"},
	}
}

// Only explicitly listed, parameter-free actions can execute from a callback.
// Resolve custom commands by name, so an old card cannot run a different item
// after the administrator reorders or disables commands.
func helpActionCommand(c Config, action string) (string, bool) {
	for _, item := range helpItems(c) {
		if !item.input && item.action == action {
			return item.command, true
		}
	}
	return "", false
}

func (s *Service) sendHelp(ctx context.Context, c Config, j Job, arg string) error {
	if arg != "" {
		return s.send(ctx, c, j, "用法：/帮助", nil, 1)
	}
	text := "📖 **指令帮助**"
	keyboard := &messageKeyboard{}
	customHeading := false
	for i, item := range helpItems(c) {
		if strings.HasPrefix(item.action, "custom:") {
			if !customHeading {
				text += "\n\n**自定义指令**"
				customHeading = true
			}
			text += "\n" + replyPlainLabel(item.command) + "：" + replyPlainLabel(item.description)
		} else {
			usage := item.command
			if item.usage != "" {
				usage = item.usage
			}
			text += "\n" + replyPlainLabel(usage) + "：" + replyPlainLabel(item.description)
		}
		if i < helpQuickActions {
			button := helpCommandButton(item.label, item.action, item.command)
			if i%helpColumns == 0 {
				keyboard.Content.Rows = append(keyboard.Content.Rows, keyboardRow{})
			}
			row := &keyboard.Content.Rows[len(keyboard.Content.Rows)-1]
			row.Buttons = append(row.Buttons, button)
		}
	}
	text += "\n\n按钮填入指令后发送；聊天请 @机器人 + 问题。"
	return s.sendFormattedWithKeyboard(ctx, c, j, text, nil, 1, keyboard)
}
