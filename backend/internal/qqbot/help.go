package qqbot

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

const (
	helpRowsPerMessage = 5 // QQ limits each keyboard to five rows.
	helpColumns        = 2
	helpPageSize       = 8 // Four command rows plus one navigation row.
	helpActionPrefix   = "qqhelp:v2:"
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
	title string
	items []helpItem
}

func helpSystemItems() []helpItem {
	return []helpItem{
		{action: "checkin", label: "每日签到", command: "/签到", description: "每日签到"},
		{action: "account", label: "我的账户", command: "/我的", description: "余额、今日用量、累计奖励与抽奖次数"},
		{action: "welfare", label: "福利中心", command: "/福利", description: "抽奖、邀请、成长任务与兑换"},
		{action: "channels", label: "渠道状态", command: "/渠道状态", description: "查询整体及各分组的成功率、缓存率和首 Token"},
		{action: "image", label: "生成图片", command: "/生图", usage: "/生图 图片描述", description: "描述你想生成的图片", input: true},
		{action: "prices", label: "模型价格", command: "/价格", usage: "/价格 模型名", description: "查询当前价格", input: true},
		{action: "bind", label: "绑定账号", command: "/绑定", usage: "/绑定 邮箱或绑定码", description: "绑定账号", input: true},
		{action: "transfer", label: "余额转账", command: "/转账", usage: "/转账 金额 邮箱", description: "向其他账号转账余额", input: true},
	}
}

func helpPages(c Config) []helpPage {
	pages := []helpPage{{title: "功能菜单", items: helpSystemItems()}}
	// Preserve the separate ranking feature and administrator-defined replies
	// without adding them to the eight primary actions.
	items := []helpItem{{action: "rankings", label: "今日排行榜", command: "/今日排行榜", description: "匿名查看 Token 与消费金额前 10 名"}}
	for _, custom := range c.CustomCommands {
		if custom.Enabled && !isBuiltinCommand(strings.TrimPrefix(custom.Command, "/")) && !isRetiredCommand(strings.TrimPrefix(custom.Command, "/")) {
			items = append(items, helpItem{action: "custom:" + custom.Command, label: custom.Command, command: custom.Command, description: "自定义回复"})
		}
	}
	for start := 0; start < len(items); start += helpPageSize {
		end := min(start+helpPageSize, len(items))
		pages = append(pages, helpPage{title: "更多服务", items: items[start:end]})
	}
	return pages
}

// Keep the eight main menu buttons compact while making every built-in and
// enabled custom command discoverable from the help home.
func helpCommandOverview(menu []helpPage) []helpItem {
	commands := []helpItem{{command: "/帮助", description: "查看全部指令；/帮助 页码 查看更多按钮"}}
	for _, page := range menu {
		for _, item := range page.items {
			commands = append(commands, item)
			if item.command == "/福利" {
				commands = append(commands,
					helpItem{command: "/福利 抽奖", description: "消耗一次抽奖次数，参与抽奖"},
					helpItem{command: "/福利 领取 任务编号", description: "领取已完成的成长任务奖励"},
					helpItem{command: "/福利 兑换 兑换码", description: "兑换额度、抽奖次数或奖品"},
				)
			}
		}
	}
	return commands
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
	navigation := keyboardRow{}
	if page > 1 {
		navigation.Buttons = append(navigation.Buttons, helpCommandButton("上一页", fmt.Sprintf("page:%d", page-1), fmt.Sprintf("/帮助 %d", page-1)))
	}
	if page < pages {
		label := "下一页"
		if page == 1 {
			label = "更多服务"
		}
		navigation.Buttons = append(navigation.Buttons, helpCommandButton(label, fmt.Sprintf("page:%d", page+1), fmt.Sprintf("/帮助 %d", page+1)))
	}
	// Keep each page within QQ's five-row keyboard limit.
	// Spread its rows across consecutive cards and put navigation on the last.
	commandRows := (len(current.items) + helpColumns - 1) / helpColumns
	totalRows := commandRows
	if len(navigation.Buttons) > 0 {
		totalRows++
	}
	cards := (totalRows + helpRowsPerMessage - 1) / helpRowsPerMessage
	itemsPerCard := ((commandRows + cards - 1) / cards) * helpColumns
	for start, seq := 0, 1; start < len(current.items); start, seq = start+itemsPerCard, seq+1 {
		end := min(start+itemsPerCard, len(current.items))
		text := "# 📖 指令帮助 · " + current.title + "\n\n"
		listed := current.items[start:end]
		if page == 1 {
			text += "## 指令一览\n\n"
			listed = helpCommandOverview(menu)
		}
		for _, item := range listed {
			usage := item.command
			if item.usage != "" {
				usage = item.usage
			}
			text += "- **" + replyPlainLabel(usage) + "**：" + item.description + "\n"
		}
		if page == 1 {
			text += "\n## 功能菜单\n"
		}
		keyboard := &messageKeyboard{}
		for i, item := range current.items[start:end] {
			button := helpCommandButton(item.label, item.action, item.command)
			if item.input {
				button.Action.Data = item.command + " "
				button.RenderData.Style = 0
			}
			if i%helpColumns == 0 {
				keyboard.Content.Rows = append(keyboard.Content.Rows, keyboardRow{})
			}
			row := &keyboard.Content.Rows[len(keyboard.Content.Rows)-1]
			row.Buttons = append(row.Buttons, button)
		}
		if end == len(current.items) && len(navigation.Buttons) > 0 {
			keyboard.Content.Rows = append(keyboard.Content.Rows, navigation)
		}
		text += fmt.Sprintf("\n第 %d/%d 页", page, pages)
		if cards > 1 {
			text += fmt.Sprintf(" · 本页第 %d/%d 段", seq, cards)
		}
		if pages > 1 {
			text += " · /帮助 页码 可翻页"
		}
		text += "\n按钮不可用时，可直接发送上面的指令。"
		text += "\n\n> 点击按钮填入指令，再点发送；需要参数时请先补全。"
		if page == 1 {
			text += "\n> 聊天：@机器人 + 问题。"
		}
		if err := s.sendFormattedWithKeyboard(ctx, c, j, text, nil, seq, keyboard); err != nil {
			return err
		}
	}
	return nil
}
