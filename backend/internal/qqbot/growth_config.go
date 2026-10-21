package qqbot

import (
	"errors"
	"fmt"
	"math"
	"regexp"
	"strings"
	"unicode"

	"github.com/shopspring/decimal"
)

const maxGrowthTasks = 12

var growthTaskIDPattern = regexp.MustCompile("^[a-z][a-z0-9_-]{0,63}$")

type GrowthTask struct {
	ID      string  `json:"id"`
	Name    string  `json:"name"`
	Kind    string  `json:"kind"`
	Target  float64 `json:"target"`
	Chances int     `json:"chances"`
	Enabled bool    `json:"enabled"`
}

func (g GrowthConfig) tasks() []GrowthTask {
	if g.Tasks != nil {
		return g.Tasks
	}
	// Keep the original entitlement keys when upgrading existing settings.
	return []GrowthTask{
		{ID: "binding", Name: "完成账号绑定", Kind: "binding", Target: 1, Chances: g.BindingChances, Enabled: g.BindingChances > 0},
		{ID: "first_usage", Name: "首次成功 API 调用", Kind: "usage_count", Target: 1, Chances: g.FirstUsageChances, Enabled: g.FirstUsageChances > 0},
		{ID: "checkin_30", Name: "累计签到 30 天", Kind: "checkin_total", Target: 30, Chances: g.Checkin30Chances, Enabled: g.Checkin30Chances > 0},
	}
}

func (c CheckinConfig) streakDays() int {
	if c.StreakDays == 0 {
		return 7
	}
	return c.StreakDays
}

func validateGrowth(c CheckinConfig) error {
	if c.streakDays() < 1 || c.streakDays() > 3650 {
		return errors.New("连签奖励周期须为 1～3650 天")
	}
	for _, n := range []int{c.StreakChances, c.Growth.BindingChances, c.Growth.FirstUsageChances, c.Growth.Checkin30Chances} {
		if n < 0 || n > 1000 {
			return errors.New("连签与成长奖励次数须在 0 到 1000 之间；0 表示关闭")
		}
	}
	tasks := c.Growth.tasks()
	if len(tasks) > maxGrowthTasks {
		return fmt.Errorf("最多配置 %d 项成长任务", maxGrowthTasks)
	}
	ids := make(map[string]bool, len(tasks))
	for _, task := range tasks {
		if !growthTaskIDPattern.MatchString(task.ID) || ids[task.ID] {
			return errors.New("成长任务标识格式不正确或重复")
		}
		ids[task.ID] = true
		if strings.TrimSpace(task.Name) == "" || len([]rune(task.Name)) > 40 || strings.IndexFunc(task.Name, unicode.IsControl) >= 0 {
			return errors.New("成长任务名称须为 1～40 个字符且不能换行")
		}
		if task.Chances < 0 || task.Chances > 1000 {
			return errors.New("成长任务奖励次数须为 0～1000 的整数")
		}
		if math.IsNaN(task.Target) || math.IsInf(task.Target, 0) {
			return errors.New("成长任务目标不正确")
		}
		switch task.Kind {
		case "binding":
			if task.Target != 1 {
				return errors.New("绑定任务目标必须为 1")
			}
		case "usage_count", "checkin_total", "checkin_streak":
			limit := float64(3650)
			if task.Kind == "usage_count" {
				limit = 1000000
			}
			if task.Target < 1 || task.Target > limit || math.Trunc(task.Target) != task.Target {
				return errors.New("调用目标须为 1～1000000 的整数，签到目标须为 1～3650 的整数")
			}
		case "recharge_total":
			if task.Target < .01 || !validRechargeThreshold(task.Target) {
				return errors.New("累计有效充值目标须为 0.01～1000000000 USD，最多两位小数")
			}
		default:
			return errors.New("成长任务类型不支持")
		}
	}
	return nil
}

type growthProgress struct {
	total, streak, usage int
	recharge             decimal.Decimal
}

func (p growthProgress) task(task GrowthTask) (bool, string) {
	var current float64
	unit := "天"
	switch task.Kind {
	case "binding":
		return true, "已完成"
	case "usage_count":
		current, unit = float64(p.usage), "次"
	case "checkin_total":
		current = float64(p.total)
	case "checkin_streak":
		current = float64(p.streak)
	case "recharge_total":
		return p.recharge.GreaterThanOrEqual(decimal.NewFromFloat(task.Target)), fmt.Sprintf("USD %s/%.2f", p.recharge.Truncate(2).StringFixed(2), task.Target)
	default:
		return false, "任务类型不支持"
	}
	return current >= task.Target, fmt.Sprintf("%.0f/%.0f %s", current, task.Target, unit)
}
