package qqbot

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const welfareUsage = "用法：/福利；抽奖：/福利 抽奖；领取：/福利 领取 任务编号；兑换：/福利 兑换 兑换码。"

// Built-in replies use QQ's documented headings, emphasis, lists and quotes.
// Keep calculations in the existing account/reward services; this layer only
// assembles their output and command buttons.
func replySection(title, body string) string {
	first, rest, found := strings.Cut(body, "\n\n")
	if found && !strings.Contains(first, "\n") && strings.Contains(first, "**") {
		body = rest
	}
	return "## " + title + "\n\n" + strings.TrimSpace(body)
}

func commandReplyHeading(body string) string {
	first, rest, found := strings.Cut(body, "\n")
	if !strings.HasPrefix(first, "#") && strings.Count(first, "**") == 2 {
		first = "# " + strings.ReplaceAll(first, "**", "")
	}
	if found {
		return first + "\n" + rest
	}
	return first
}

func (s *Service) accountOverview(ctx context.Context, c Config, j Job, now time.Time) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	uid, err := boundUser(ctx, s.db, c.AppID, j.MemberID)
	if errors.Is(err, sql.ErrNoRows) {
		return bindFirst, nil
	}
	if err != nil {
		return "", err
	}
	// Preserve invitation settlement before displaying rewards and balance.
	rewards, err := s.myRewards(ctx, c, j)
	if err != nil || rewards == bindFirst {
		return rewards, err
	}
	var balance float64
	err = s.db.QueryRowContext(ctx, `SELECT u.balance FROM qq_bot_bindings b JOIN users u ON u.id=b.user_id WHERE b.app_id=$1 AND b.member_id=$2 AND u.id=$3 AND u.status='active' AND u.deleted_at IS NULL`, c.AppID, j.MemberID, uid).Scan(&balance)
	if errors.Is(err, sql.ErrNoRows) {
		return bindFirst, nil
	}
	if err != nil {
		return "", err
	}
	usage, err := s.todayUsage(ctx, c, j, now)
	if err != nil || usage == bindFirst {
		return usage, err
	}
	lottery, err := s.lotteryInfo(ctx, c, j, false)
	if err != nil || lottery == bindFirst {
		return lottery, err
	}
	return fmt.Sprintf("# 👤 我的账户\n\n**账户额度：USD %.2f**\n\n%s\n\n%s\n\n%s", balance, replySection("今日用量", usage), replySection("累计奖励", rewards), replySection("抽奖次数", lottery)), nil
}

func (s *Service) handleWelfare(ctx context.Context, c Config, j *Job, arg string) error {
	if arg == "" {
		return s.sendWelfare(ctx, c, *j, 1)
	}
	if page, err := strconv.Atoi(arg); err == nil && page > 0 && strconv.Itoa(page) == arg {
		return s.sendWelfare(ctx, c, *j, page)
	}
	action, value := command("/" + arg)
	var text string
	var err error
	switch action {
	case "抽奖":
		if value != "" {
			return s.send(ctx, c, *j, "用法：/福利 抽奖，每次消耗一次抽奖次数。", nil, 1)
		}
		text, err = s.drawLottery(ctx, c, *j)
		if err != nil {
			text = "抽奖处理异常，请发送 /我的 查询剩余次数。"
		}
	case "领取":
		index, parseErr := strconv.Atoi(value)
		_, rankingClaim := rankingClaimID(value)
		if !rankingClaim && (parseErr != nil || index < 1 || strconv.Itoa(index) != value) {
			return s.send(ctx, c, *j, "用法：/福利 领取 任务编号；发送 /福利 查看任务。", nil, 1)
		}
		text, err = s.growthTasks(ctx, c, *j, value)
		if err != nil {
			text = "成长任务处理失败，请稍后重试。"
		}
	case "兑换":
		// Codes have already been hashed and removed from the queued event.
		j.Kind = "reward_redemption"
		text, err = s.redeemReward(ctx, c, *j)
		if err != nil {
			text = "奖励兑换处理异常，请稍后重试或联系管理员。"
			j.Error = "奖励兑换处理异常"
		}
		text = commandReplyHeading(text)
		if _, saveErr := s.db.ExecContext(ctx, `UPDATE qq_bot_jobs SET kind=$2,result_text=$3 WHERE id=$1`, j.ID, j.Kind, text); saveErr != nil {
			return errors.New("兑换结果记录失败，请通过兑换管理核对奖励状态")
		}
		if sendErr := s.send(ctx, c, *j, text, nil, 1); sendErr != nil {
			return sendErr
		}
		if err != nil {
			return errors.New(j.Error)
		}
		return nil
	default:
		return s.send(ctx, c, *j, welfareUsage, nil, 1)
	}
	return s.send(ctx, c, *j, commandReplyHeading(text), nil, 1)
}

type welfarePage struct {
	text    string
	buttons []keyboardButton
}

// Pages never truncate content or exceed five rows of two buttons. Reserve
// two actions for lottery/redemption and two slots for page navigation.
func paginateWelfare(body string, claims []keyboardButton) []welfarePage {
	var pages []welfarePage
	current := welfarePage{}
	for _, paragraph := range strings.Split(body, "\n\n") {
		if strings.TrimSpace(paragraph) == "" {
			continue
		}
		var buttons []keyboardButton
		for _, claim := range claims {
			if strings.Contains(paragraph+"\n", claim.Action.Data+"\n") {
				buttons = append(buttons, claim)
			}
		}
		// Operator-defined rules can be long. Split on lines first, without
		// applying the truncation used for unbounded model-generated replies.
		for len([]rune(paragraph)) > 1300 {
			runes := []rune(paragraph)
			cut := 1300
			for i := cut - 1; i >= 650; i-- {
				if runes[i] == '\n' {
					cut = i + 1
					break
				}
			}
			if current.text != "" {
				pages = append(pages, current)
				current = welfarePage{}
			}
			pages = append(pages, welfarePage{text: string(runes[:cut])})
			paragraph = string(runes[cut:])
		}
		if current.text != "" && (len([]rune(current.text))+len([]rune(paragraph))+2 > 1300 || len(current.buttons)+len(buttons) > 6) {
			pages = append(pages, current)
			current = welfarePage{}
		}
		if current.text != "" {
			current.text += "\n\n"
		}
		current.text += paragraph
		current.buttons = append(current.buttons, buttons...)
	}
	if current.text != "" {
		pages = append(pages, current)
	}
	return pages
}

func (s *Service) sendWelfare(ctx context.Context, c Config, j Job, page int) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var sections []string
	var actions, claims []keyboardButton
	count, err := s.lotteryInfo(ctx, c, j, false)
	if err != nil {
		return s.send(ctx, c, j, "福利查询失败，请稍后重试。", nil, 1)
	}
	if count != "抽奖活动未开启。" {
		rules, rulesErr := s.lotteryInfo(ctx, c, j, true)
		if rulesErr != nil {
			return s.send(ctx, c, j, "抽奖信息查询失败，请稍后重试。", nil, 1)
		}
		sections = append(sections, replySection("抽奖福利", count), replySection("活动规则", rules))
		actions = append(actions, helpCommandButton("抽奖一次", "welfare:draw", "/福利 抽奖"))
	}
	if c.InviteReward > 0 && c.InviteDailyLimit > 0 {
		info, infoErr := s.inviteInfo(ctx, c, j)
		if infoErr != nil {
			return s.send(ctx, c, j, "邀请信息查询失败，请稍后重试。", nil, 1)
		}
		invites, invitesErr := s.myInvites(ctx, c, j)
		if invitesErr != nil {
			return s.send(ctx, c, j, "邀请信息查询失败，请稍后重试。", nil, 1)
		}
		sections = append(sections, replySection("邀请奖励", info), replySection("邀请进度", invites))
	}
	growth, err := s.growthTasks(ctx, c, j, "", &claims)
	if err != nil {
		return s.send(ctx, c, j, "成长任务查询失败，请稍后重试。", nil, 1)
	}
	sections = append(sections, replySection("成长任务", growth), "## 奖励兑换\n\n- 发送 /福利 兑换 兑换码。\n- 额度和抽奖次数自动到账；实物及其他奖品由管理员发放。")
	redeem := helpCommandButton("兑换奖励", "welfare:redeem", "/福利 兑换 ")
	redeem.RenderData.Style = 0
	actions = append(actions, redeem)
	pages := paginateWelfare(strings.Join(sections, "\n\n"), claims)
	if page < 1 || page > len(pages) {
		return s.send(ctx, c, j, fmt.Sprintf("福利中心共 %d 页，请发送 /福利 或 /福利 页码。", len(pages)), nil, 1)
	}
	current := pages[page-1]
	buttons := append(actions, current.buttons...)
	keyboard := &messageKeyboard{}
	for i, button := range buttons {
		if i%2 == 0 {
			keyboard.Content.Rows = append(keyboard.Content.Rows, keyboardRow{})
		}
		last := &keyboard.Content.Rows[len(keyboard.Content.Rows)-1]
		last.Buttons = append(last.Buttons, button)
	}
	nav := keyboardRow{}
	if page > 1 {
		nav.Buttons = append(nav.Buttons, helpCommandButton("上一页", fmt.Sprintf("welfare:page:%d", page-1), fmt.Sprintf("/福利 %d", page-1)))
	}
	if page < len(pages) {
		nav.Buttons = append(nav.Buttons, helpCommandButton("下一页", fmt.Sprintf("welfare:page:%d", page+1), fmt.Sprintf("/福利 %d", page+1)))
	}
	if len(nav.Buttons) > 0 {
		keyboard.Content.Rows = append(keyboard.Content.Rows, nav)
	}
	text := "# 🎁 福利中心\n\n" + current.text
	if len(pages) > 1 {
		text += fmt.Sprintf("\n\n第 %d/%d 页 · /福利 页码 查看更多", page, len(pages))
	}
	text += "\n\n> 点击按钮填入指令，再点发送。抽奖每次消耗一次次数。"
	return s.sendFormattedWithKeyboard(ctx, c, j, normalizeReplyLineBreaks(text), nil, 1, keyboard)
}
