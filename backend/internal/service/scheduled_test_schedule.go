package service

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// A nil schedule preserves the existing five-field cron schedule. Periods
// repeat daily in Timezone, with intervals anchored to each period's start.
type ScheduledTestSchedule struct {
	Timezone string                        `json:"timezone"`
	Periods  []ScheduledTestSchedulePeriod `json:"periods"`
}

type ScheduledTestSchedulePeriod struct {
	Start           string `json:"start"`
	End             string `json:"end"`
	IntervalMinutes int    `json:"interval_minutes"`
}

type scheduledTestPeriod struct {
	start, duration, interval int
}

func scheduledTestClockMinute(value string, allowMidnightEnd bool) (int, error) {
	if allowMidnightEnd && value == "24:00" {
		return 1440, nil
	}
	if len(value) != 5 || value[2] != ':' {
		return 0, fmt.Errorf("time must use HH:mm")
	}
	for _, i := range []int{0, 1, 3, 4} {
		if value[i] < '0' || value[i] > '9' {
			return 0, fmt.Errorf("time must use HH:mm")
		}
	}
	hour, _ := strconv.Atoi(value[:2])
	minute, _ := strconv.Atoi(value[3:])
	if hour > 23 || minute > 59 {
		return 0, fmt.Errorf("time is outside 00:00..23:59")
	}
	return hour*60 + minute, nil
}

func parseScheduledTestSchedule(schedule *ScheduledTestSchedule) (*time.Location, []scheduledTestPeriod, error) {
	if schedule == nil {
		return nil, nil, fmt.Errorf("schedule is required")
	}
	if strings.TrimSpace(schedule.Timezone) == "" || schedule.Timezone == "Local" {
		return nil, nil, fmt.Errorf("schedule timezone must be an explicit IANA timezone, such as Asia/Shanghai")
	}
	loc, err := time.LoadLocation(schedule.Timezone)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid schedule timezone: %w", err)
	}
	if len(schedule.Periods) < 1 || len(schedule.Periods) > 24 {
		return nil, nil, fmt.Errorf("schedule requires 1..24 periods")
	}
	var occupied [1440]bool
	periods := make([]scheduledTestPeriod, 0, len(schedule.Periods))
	for i, period := range schedule.Periods {
		start, err := scheduledTestClockMinute(period.Start, false)
		if err != nil {
			return nil, nil, fmt.Errorf("schedule period %d start: %w", i+1, err)
		}
		end, err := scheduledTestClockMinute(period.End, true)
		if err != nil {
			return nil, nil, fmt.Errorf("schedule period %d end: %w", i+1, err)
		}
		if start == end {
			return nil, nil, fmt.Errorf("schedule period %d start and end must differ; use 00:00..24:00 for a full day", i+1)
		}
		duration := end - start
		if duration < 0 {
			duration += 1440
		}
		if period.IntervalMinutes < 1 || period.IntervalMinutes > duration {
			return nil, nil, fmt.Errorf("schedule period %d interval_minutes must be between 1 and its duration (%d)", i+1, duration)
		}
		for minute := 0; minute < duration; minute++ {
			index := (start + minute) % 1440
			if occupied[index] {
				return nil, nil, fmt.Errorf("schedule period %d overlaps another period", i+1)
			}
			occupied[index] = true
		}
		periods = append(periods, scheduledTestPeriod{start, duration, period.IntervalMinutes})
	}
	return loc, periods, nil
}

func scheduledTestScheduleAllows(schedule *ScheduledTestSchedule, now time.Time) (bool, error) {
	if schedule == nil {
		return true, nil
	}
	loc, periods, err := parseScheduledTestSchedule(schedule)
	if err != nil {
		return false, err
	}
	local := now.In(loc)
	minute := local.Hour()*60 + local.Minute()
	for _, period := range periods {
		if (minute-period.start+1440)%1440 < period.duration {
			return true, nil
		}
	}
	return false, nil
}

func computeScheduledTestNextRun(plan *ScheduledTestPlan, from time.Time) (time.Time, error) {
	if plan.Schedule == nil {
		return computeNextRun(plan.CronExpression, from)
	}
	loc, periods, err := parseScheduledTestSchedule(plan.Schedule)
	if err != nil {
		return time.Time{}, err
	}
	// Walk actual minutes, then match the local clock. This skips nonexistent
	// spring-forward times and handles repeated fall-back times like cron.
	// Three days also cover a skipped civil day after a timezone transition.
	limit := from.Add(72 * time.Hour)
	for next := from.Truncate(time.Minute).Add(time.Minute); next.Before(limit); next = next.Add(time.Minute) {
		local := next.In(loc)
		minute := local.Hour()*60 + local.Minute()
		for _, period := range periods {
			elapsed := (minute - period.start + 1440) % 1440
			if elapsed < period.duration && elapsed%period.interval == 0 {
				return next, nil
			}
		}
	}
	return time.Time{}, fmt.Errorf("schedule has no upcoming execution")
}
