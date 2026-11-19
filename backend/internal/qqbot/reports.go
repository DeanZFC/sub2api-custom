package qqbot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

var ErrReportFilter = errors.New("报表日期、筛选或分页参数不正确；日期范围最多 366 天")

type ReportFilter struct {
	StartDate  string `form:"start_date"`
	EndDate    string `form:"end_date"`
	GroupID    string `form:"group_id"`
	Kind       string `form:"kind"`
	RewardType string `form:"reward_type"`
	Search     string `form:"search"`
	Page       int    `form:"page"`
	PageSize   int    `form:"page_size"`
}

// Normalize supplies calendar defaults without depending on the server's zone.
func (f ReportFilter) Normalize(now time.Time) (ReportFilter, error) {
	zone := time.FixedZone("Asia/Shanghai", 8*3600)
	if f.EndDate == "" {
		f.EndDate = day(now)
	}
	end, err := time.ParseInLocation("2006-01-02", f.EndDate, zone)
	if err != nil {
		return f, ErrReportFilter
	}
	if f.StartDate == "" {
		f.StartDate = end.AddDate(0, 0, -6).Format("2006-01-02")
	}
	start, err := time.ParseInLocation("2006-01-02", f.StartDate, zone)
	if err != nil || start.After(end) || end.Sub(start) > 365*24*time.Hour || start.Year() < 1 || end.Year() > 9998 {
		return f, ErrReportFilter
	}
	f.GroupID, f.Search = strings.TrimSpace(f.GroupID), strings.TrimSpace(f.Search)
	if len(f.GroupID) > 254 || len(f.Search) > 254 || f.Page < 0 || f.Page > 1000000 || f.PageSize < 0 || f.PageSize > 100 {
		return f, ErrReportFilter
	}
	if f.Page == 0 {
		f.Page = 1
	}
	if f.PageSize == 0 {
		f.PageSize = 20
	}
	switch f.Kind {
	case "", "checkin", "invite", "lottery", "redemption", "growth", "streak", "ranking":
	default:
		return f, ErrReportFilter
	}
	switch f.RewardType {
	case "", "balance", "chances", "coupon", "physical", "other":
	default:
		return f, ErrReportFilter
	}
	return f, nil
}

type ReportMetrics struct {
	RewardCount       int64   `json:"reward_count"`
	UserCount         int64   `json:"user_count"`
	BalanceAmount     float64 `json:"balance_amount"`
	ChancesQuantity   int64   `json:"chances_quantity"`
	CouponQuantity    int64   `json:"coupon_quantity"`
	PendingQuantity   int64   `json:"pending_quantity"`
	FulfilledQuantity int64   `json:"fulfilled_quantity"`
}

type ReportDay struct {
	Date string `json:"date"`
	ReportMetrics
}

type ReportKind struct {
	Kind string `json:"kind"`
	ReportMetrics
}

type ReportUser struct {
	UserID   int64  `json:"user_id"`
	Email    string `json:"email"`
	Nickname string `json:"nickname"`
	ReportMetrics
}

type ReportRecord struct {
	ID          string     `json:"id"`
	Kind        string     `json:"kind"`
	RewardType  string     `json:"reward_type"`
	Amount      float64    `json:"amount"`
	Quantity    int64      `json:"quantity"`
	PrizeName   string     `json:"prize_name"`
	Status      string     `json:"status"`
	UserID      int64      `json:"user_id"`
	Email       string     `json:"email"`
	Nickname    string     `json:"nickname"`
	MemberID    string     `json:"member_id"`
	GroupID     string     `json:"group_id"`
	CreatedAt   time.Time  `json:"created_at"`
	FulfilledAt *time.Time `json:"fulfilled_at"`
}

type RewardReport struct {
	StartDate string         `json:"start_date"`
	EndDate   string         `json:"end_date"`
	Timezone  string         `json:"timezone"`
	Summary   ReportMetrics  `json:"summary"`
	Daily     []ReportDay    `json:"daily"`
	ByKind    []ReportKind   `json:"by_kind"`
	TopUsers  []ReportUser   `json:"top_users"`
	Items     []ReportRecord `json:"items"`
	Total     int64          `json:"total"`
	Page      int            `json:"page"`
	PageSize  int            `json:"page_size"`
}

const reportMetricSelect = `COUNT(*) AS reward_count,COUNT(DISTINCT user_id) AS user_count,
 COALESCE(SUM(amount),0) AS balance_amount,
 COALESCE(SUM(quantity) FILTER(WHERE reward_type='chances'),0) AS chances_quantity,
 COALESCE(SUM(quantity) FILTER(WHERE reward_type='coupon'),0) AS coupon_quantity,
 COALESCE(SUM(quantity) FILTER(WHERE reward_type IN ('physical','other') AND status='pending'),0) AS pending_quantity,
 COALESCE(SUM(quantity) FILTER(WHERE reward_type IN ('physical','other') AND status='fulfilled'),0) AS fulfilled_quantity`

// Balance awards have one authoritative ledger. Non-balance fulfillment rows
// and lottery snapshots add independent components, never another balance sum.
// Unscoped historical chance grants (also used by GPT Direct) cannot establish
// an application's ownership and deliberately do not enter an app report.
const rewardReportSQL = `WITH draw_records AS (
 SELECT d.id,d.user_id,d.member_id,d.group_id,d.prize_name,d.config_snapshot,r.created_at AS reward_at
 FROM qq_bot_rewards r JOIN qq_bot_lottery_draws d
 ON r.reward_key='lottery:'||d.id::text AND d.app_id=r.app_id AND d.user_id=r.user_id
 WHERE r.app_id=$1 AND r.kind='lottery' AND r.created_at>=$2 AND r.created_at<$3
 UNION ALL
 SELECT d.id,d.user_id,d.member_id,d.group_id,d.prize_name,d.config_snapshot,d.created_at
 FROM qq_bot_lottery_draws d WHERE d.app_id=$1 AND d.created_at>=$2 AND d.created_at<$3
 AND NOT EXISTS(SELECT 1 FROM qq_bot_rewards r WHERE r.reward_key='lottery:'||d.id::text
 AND r.app_id=d.app_id AND r.user_id=d.user_id AND r.kind='lottery')
), raw_rewards AS (
 SELECT 'balance:'||r.id::text AS id,r.kind,'balance'::text AS reward_type,r.amount,0::bigint AS quantity,
 ''::text AS prize_name,'fulfilled'::text AS status,r.user_id,r.member_id,r.group_id,r.created_at,r.created_at AS fulfilled_at
 FROM qq_bot_rewards r WHERE r.app_id=$1 AND r.amount>0 AND r.created_at>=$2 AND r.created_at<$3
 UNION ALL
 SELECT 'reward:'||r.id::text,CASE WHEN r.source_kind='code' THEN 'redemption' ELSE r.source_kind END,
 r.reward_type,0::numeric,r.quantity::bigint,COALESCE(NULLIF(r.prize_name,''),r.batch_name),r.status,
 r.user_id,r.member_id,r.group_id,r.redeemed_at,r.fulfilled_at
 FROM qq_bot_reward_codes r WHERE r.app_id=$1 AND r.reward_type<>'balance' AND r.quantity>0
 AND r.status IN ('pending','fulfilled') AND r.redeemed_at>=$2 AND r.redeemed_at<$3
 UNION ALL
 SELECT 'draw:'||d.id::text||':'||v.reward_type,'lottery',v.reward_type,0::numeric,v.quantity,d.prize_name,'fulfilled',
 d.user_id,d.member_id,d.group_id,d.reward_at,d.reward_at
 FROM draw_records d
 CROSS JOIN LATERAL (VALUES
 ('chances',COALESCE((d.config_snapshot->>'awarded_chances')::bigint,0)),
 ('coupon',COALESCE((d.config_snapshot->>'coupon_quantity')::bigint,0))) v(reward_type,quantity)
 WHERE v.quantity>0
), report AS MATERIALIZED (
 SELECT r.*,u.email,COALESCE(b.nickname,'') AS nickname,(r.created_at AT TIME ZONE 'Asia/Shanghai')::date::text AS date
 FROM raw_rewards r JOIN users u ON u.id=r.user_id
 LEFT JOIN qq_bot_bindings b ON b.app_id=$1 AND b.user_id=r.user_id AND b.member_id=r.member_id
 WHERE ($4='' OR r.group_id=$4) AND ($5='' OR r.kind=$5) AND ($6='' OR r.reward_type=$6)
 AND ($7='' OR r.user_id::text=$7 OR strpos(lower(u.email),lower($7))>0
 OR strpos(lower(r.member_id),lower($7))>0 OR strpos(lower(COALESCE(b.nickname,'')),lower($7))>0)
)
SELECT
 (SELECT row_to_json(m) FROM (SELECT ` + reportMetricSelect + ` FROM report) m),
 COALESCE((SELECT json_agg(m) FROM (SELECT date,` + reportMetricSelect + ` FROM report GROUP BY date ORDER BY date) m),'[]'),
 COALESCE((SELECT json_agg(m) FROM (SELECT kind,` + reportMetricSelect + ` FROM report GROUP BY kind ORDER BY balance_amount DESC,reward_count DESC,kind) m),'[]'),
 COALESCE((SELECT json_agg(m) FROM (SELECT user_id,MAX(email) AS email,MAX(nickname) AS nickname,` + reportMetricSelect + ` FROM report GROUP BY user_id ORDER BY balance_amount DESC,reward_count DESC,user_id LIMIT 10) m),'[]'),
 COALESCE((SELECT json_agg(m) FROM (SELECT id,kind,reward_type,amount,quantity,prize_name,status,user_id,email,nickname,member_id,group_id,created_at,fulfilled_at
 FROM report ORDER BY created_at DESC,id DESC LIMIT $8 OFFSET $9) m),'[]')`

func (s *Service) RewardReport(ctx context.Context, filter ReportFilter) (RewardReport, error) {
	f, err := filter.Normalize(time.Now())
	if err != nil {
		return RewardReport{}, err
	}
	out := RewardReport{StartDate: f.StartDate, EndDate: f.EndDate, Timezone: "Asia/Shanghai", Page: f.Page, PageSize: f.PageSize}
	zone := time.FixedZone("Asia/Shanghai", 8*3600)
	start, _ := time.ParseInLocation("2006-01-02", f.StartDate, zone)
	end, _ := time.ParseInLocation("2006-01-02", f.EndDate, zone)
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return out, err
	}
	defer func() { _ = tx.Rollback() }()
	cfg, err := s.loadConfig(ctx, tx)
	if err != nil {
		return out, err
	}
	var raw [5][]byte
	err = tx.QueryRowContext(ctx, rewardReportSQL, cfg.AppID, start, end.AddDate(0, 0, 1), f.GroupID, f.Kind, f.RewardType, f.Search, f.PageSize, (f.Page-1)*f.PageSize).Scan(&raw[0], &raw[1], &raw[2], &raw[3], &raw[4])
	if err != nil {
		return out, err
	}
	var sparseDays []ReportDay
	for i, target := range []any{&out.Summary, &sparseDays, &out.ByKind, &out.TopUsers, &out.Items} {
		if err = json.Unmarshal(raw[i], target); err != nil {
			return out, err
		}
	}
	days := make(map[string]ReportMetrics, len(sparseDays))
	for _, d := range sparseDays {
		days[d.Date] = d.ReportMetrics
	}
	out.Daily = make([]ReportDay, 0, int(end.Sub(start).Hours()/24)+1)
	for date := start; !date.After(end); date = date.AddDate(0, 0, 1) {
		key := date.Format("2006-01-02")
		out.Daily = append(out.Daily, ReportDay{Date: key, ReportMetrics: days[key]})
	}
	out.Total = out.Summary.RewardCount
	return out, tx.Commit()
}
