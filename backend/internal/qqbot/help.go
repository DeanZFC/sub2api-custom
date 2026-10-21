package qqbot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

const (
	helpRowsPerPage   = 4 // Reserve the fifth row for navigation.
	helpPageSize      = helpRowsPerPage * 2
	helpSystemColumns = 5
	helpActionPrefix  = "qqhelp:v1:"
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
	action, label, command, usage, description string
	input                                      bool
}

type helpPage struct {
	title   string
	items   []helpItem
	columns int
}

func helpSystemItems() []helpItem {
	items := []helpItem{
		{action: "checkin", label: "每日签到", command: "/签到", description: "每日签到"},
		{action: "balance", label: "我的余额", command: "/我的余额", description: "查询账户余额"},
		{action: "rewards", label: "我的奖励", command: "/我的奖励", description: "查询各项累计奖励"},
		{action: "channels", label: "渠道状态", command: "/渠道状态", description: "查询整体及各分组的成功率、缓存率和首 Token"},
		{action: "lottery", label: "抽奖一次", command: "/抽奖", description: "消耗一次抽奖次数，参与抽奖"},
		{action: "lottery_count", label: "抽奖次数", command: "/抽奖次数", description: "查询剩余次数"},
		{action: "invite_rewards", label: "邀请奖励", command: "/邀请奖励", description: "查看邀请奖励规则、条件和今日进度"},
		{action: "invites", label: "我的邀请", command: "/我的邀请", description: "查询邀请状态和人数"},
		{action: "lottery_info", label: "抽奖活动", command: "/抽奖活动", description: "查看活动规则和奖品"},
		{action: "bind", label: "绑定账号", command: "/绑定", usage: "/绑定 邮箱或绑定码", description: "绑定账号", input: true},
		{action: "redeem", label: "奖励兑换", command: "/奖励兑换", usage: "/奖励兑换 兑换码", description: "兑换额度、抽奖次数或奖品", input: true},
		{action: "image", label: "生成图片", command: "/生图", usage: "/生图 图片描述", description: "生成图片", input: true},
		{action: "transfer", label: "余额转账", command: "/转账", usage: "/转账 金额 邮箱", description: "向其他账号转账余额", input: true},
	}
	items = append(items,
		helpItem{action: "prices", label: "模型价格", command: "/价格", usage: "/价格 模型名", description: "查询当前价格", input: true},
		helpItem{action: "today_usage", label: "今日用量", command: "/今日用量", description: "查询本人今日 API 用量与计费"},
		helpItem{action: "growth", label: "成长任务", command: "/成长任务", description: "查看连签与成长任务进度"},
		helpItem{action: "growth_claim", label: "领取任务", command: "/领取任务", usage: "/领取任务 任务编号", description: "领取成长任务抽奖次数", input: true},
	)
	return items
}

func helpPages(c Config) []helpPage {
	var customItems []helpItem
	for _, custom := range c.CustomCommands {
		if custom.Enabled {
			customItems = append(customItems, helpItem{action: "custom:" + custom.Command, label: custom.Command, command: custom.Command, description: "自定义回复"})
		}
	}
	var pages []helpPage
	// QQ supports five buttons per row. Keep the built-ins together at the start
	// while retaining a navigation row and handling future additional commands.
	items := helpSystemItems()
	pageSize := helpRowsPerPage * helpSystemColumns
	for start := 0; start < len(items); start += pageSize {
		pages = append(pages, helpPage{title: "系统指令", items: items[start:min(start+pageSize, len(items))], columns: helpSystemColumns})
	}
	for start := 0; start < len(customItems); start += helpPageSize {
		pages = append(pages, helpPage{title: "自定义指令", items: customItems[start:min(start+helpPageSize, len(customItems))], columns: 2})
	}
	return pages
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
	if raw, ok := strings.CutPrefix(action, "page:"); ok {
		page, err := strconv.Atoi(raw)
		if err != nil || page < 1 || page > len(helpPages(c)) || raw != strconv.Itoa(page) {
			return "", false
		}
		return "/帮助 " + raw, true
	}
	for _, page := range helpPages(c) {
		for _, item := range page.items {
			if !item.input && item.action == action {
				return item.command, true
			}
		}
	}
	return "", false
}

func (s *Service) sendHelp(ctx context.Context, c Config, j Job, arg string) error {
	page := 1
	if arg != "" {
		var err error
		page, err = strconv.Atoi(arg)
		if err != nil || page < 1 {
			return s.send(ctx, c, j, "用法：/帮助；翻页可发送 /帮助 页码。", nil, 1)
		}
	}
	menu := helpPages(c)
	pages := len(menu)
	if page > pages {
		return s.send(ctx, c, j, fmt.Sprintf("帮助共 %d 页，请发送 /帮助 1 至 /帮助 %d。", pages, pages), nil, 1)
	}
	current := menu[page-1]
	text := "# 📖 指令帮助 · " + current.title + "\n\n点击按钮填入指令，再点发送；需要参数的指令，请补全后发送。\n\n"
	keyboard := &messageKeyboard{}
	for i, item := range current.items {
		usage := item.command
		if item.usage != "" {
			usage = item.usage
		}
		text += "- **" + replyPlainLabel(usage) + "**：" + item.description + "\n"
		button := helpCommandButton(item.label, item.action, item.command)
		if item.input {
			button.Action.Data = item.command + " "
			button.RenderData.Style = 0
		}
		if i%current.columns == 0 {
			keyboard.Content.Rows = append(keyboard.Content.Rows, keyboardRow{})
		}
		row := &keyboard.Content.Rows[len(keyboard.Content.Rows)-1]
		row.Buttons = append(row.Buttons, button)
	}
	navigation := keyboardRow{}
	if page > 1 {
		navigation.Buttons = append(navigation.Buttons, helpCommandButton("上一页", fmt.Sprintf("page:%d", page-1), fmt.Sprintf("/帮助 %d", page-1)))
	}
	if page < pages {
		navigation.Buttons = append(navigation.Buttons, helpCommandButton("下一页", fmt.Sprintf("page:%d", page+1), fmt.Sprintf("/帮助 %d", page+1)))
	}
	if len(navigation.Buttons) > 0 {
		keyboard.Content.Rows = append(keyboard.Content.Rows, navigation)
	}
	text += fmt.Sprintf("\n第 %d/%d 页 · /帮助 页码 可翻页\n按钮不可用时，可直接发送上面的指令。", page, pages)
	return s.sendFormattedWithKeyboard(ctx, c, j, text, nil, 1, keyboard)
}
