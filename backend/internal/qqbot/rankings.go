package qqbot

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

func beijingDayStart(at time.Time) time.Time {
	local := at.In(time.FixedZone("Asia/Shanghai", 8*3600))
	return time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location())
}

type rankingEntry struct {
	Kind   string
	Rank   int
	UserID int64
}

// Compute both rankings in a single database snapshot. No identifying or usage
// values leave this query: only internal account keys and ordinal positions.
func dailyRankingEntries(ctx context.Context, tx *sql.Tx, start, end time.Time) ([]rankingEntry, error) {
	rows, err := tx.QueryContext(ctx, `WITH daily AS (
 SELECT l.user_id,
   SUM(COALESCE(l.input_tokens,0)::numeric+COALESCE(l.output_tokens,0)::numeric+COALESCE(l.cache_creation_tokens,0)::numeric+COALESCE(l.cache_read_tokens,0)::numeric) AS tokens,
   SUM(COALESCE(l.actual_cost,0)) AS amount,
   MAX(l.created_at) FILTER(WHERE COALESCE(l.input_tokens,0)::numeric+COALESCE(l.output_tokens,0)::numeric+COALESCE(l.cache_creation_tokens,0)::numeric+COALESCE(l.cache_read_tokens,0)::numeric > 0) AS token_at,
   MAX(l.created_at) FILTER(WHERE l.actual_cost > 0) AS amount_at
 FROM usage_logs l JOIN users u ON u.id=l.user_id
 WHERE u.status='active' AND u.deleted_at IS NULL AND l.created_at >= $1 AND l.created_at < $2
 GROUP BY l.user_id
), tokens AS (
 SELECT user_id,ROW_NUMBER() OVER(ORDER BY tokens DESC,token_at ASC,user_id ASC) AS position FROM daily WHERE tokens>0
), amount AS (
 SELECT user_id,ROW_NUMBER() OVER(ORDER BY amount DESC,amount_at ASC,user_id ASC) AS position FROM daily WHERE amount>0
)
SELECT 'tokens',position,user_id FROM tokens WHERE position <= $3
UNION ALL SELECT 'amount',position,user_id FROM amount WHERE position <= $3
ORDER BY 1,2`, start, end, rankingTopN)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var entries []rankingEntry
	for rows.Next() {
		var entry rankingEntry
		if err = rows.Scan(&entry.Kind, &entry.Rank, &entry.UserID); err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}

func rankingAlias(c Config, date string, uid int64) string {
	mac := hmac.New(sha256.New, []byte(c.AppSecret))
	_, _ = fmt.Fprintf(mac, "qq-ranking-alias:%s:%s:%d", c.AppID, date, uid)
	return "用户 ****" + strings.ToUpper(hex.EncodeToString(mac.Sum(nil)[:4]))
}

func rankingTitle(kind string) string {
	if kind == "tokens" {
		return "Token 消耗榜"
	}
	return "消费金额榜"
}

func (s *Service) todayRankings(ctx context.Context, c Config, now time.Time) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	cfg, err := loadRankingConfig(ctx, s.db, c.AppID)
	if err != nil {
		return "", err
	}
	if !cfg.Enabled {
		return "今日排行榜暂未开启。", nil
	}
	if c.AppSecret == "" {
		return "", errors.New("ranking alias secret unavailable")
	}
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	start := beijingDayStart(now)
	entries, err := dailyRankingEntries(ctx, tx, start, now)
	if err != nil {
		return "", err
	}
	date := start.Format("2006-01-02")
	text := fmt.Sprintf("🏆 **今日排行榜**\n\n%s（北京时间）· 全站有效账号 · 各取前 10 名", date)
	for _, kind := range []string{"tokens", "amount"} {
		text += "\n\n**" + rankingTitle(kind) + "**"
		found := false
		for _, entry := range entries {
			if entry.Kind != kind {
				continue
			}
			found = true
			text += fmt.Sprintf("\n第 %d 名 · %s", entry.Rank, rankingAlias(c, date, entry.UserID))
		}
		if !found {
			text += "\n暂无上榜账号。"
		}
	}
	return text + "\n\n仅展示匿名代号和名次，不公开账号信息及具体消耗。当前为实时榜，次日北京时间 00:10 结算已开启的奖励资格；绑定后发送 /福利 查看，通过 /福利 领取 任务编号 领取。", nil
}

func (s *Service) startRankingMonitor(ctx context.Context) {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		scan := func() {
			q, cancel := context.WithTimeout(ctx, 45*time.Second)
			err := s.settleDueRankings(q, time.Now())
			cancel()
			if err != nil && ctx.Err() == nil {
				slog.Warn("qqbot daily ranking settlement failed", "error", err)
			}
		}
		scan()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				scan()
			}
		}
	}()
}

func (s *Service) settleDueRankings(ctx context.Context, now time.Time) error {
	c, err := s.Config(ctx)
	if err != nil {
		return err
	}
	if !c.Enabled || c.AppID == "" {
		return nil
	}
	// Allow ten minutes for asynchronous usage records to finish arriving.
	cutoff := beijingDayStart(now.Add(-10*time.Minute)).AddDate(0, 0, -1)
	rows, err := s.db.QueryContext(ctx, `SELECT d::date::text FROM generate_series(
 (SELECT MIN(effective_day)::timestamp FROM qq_bot_ranking_config_versions WHERE app_id=$1),$2::date::timestamp,INTERVAL '1 day') d
 WHERE NOT EXISTS(SELECT 1 FROM qq_bot_ranking_settlements s WHERE s.app_id=$1 AND s.ranking_day=d::date)
 ORDER BY d LIMIT 7`, c.AppID, cutoff.Format("2006-01-02"))
	if err != nil {
		return err
	}
	var dates []string
	for rows.Next() {
		var date string
		if err = rows.Scan(&date); err != nil {
			_ = rows.Close()
			return err
		}
		dates = append(dates, date)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, date := range dates {
		if err = s.settleRankingDay(ctx, c.AppID, date, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Service) settleRankingDay(ctx context.Context, appID, date string, now time.Time) error {
	start, err := time.ParseInLocation("2006-01-02", date, time.FixedZone("Asia/Shanghai", 8*3600))
	if err != nil {
		return err
	}
	if now.Before(start.AddDate(0, 0, 1).Add(10 * time.Minute)) {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	c, err := s.lockConfig(ctx, tx)
	if err != nil {
		return err
	}
	if !c.Enabled || c.AppID != appID {
		return nil
	}
	// Keep binding metadata stable and use the same lock order as other grants.
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,272))`, appID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,288))`, appID); err != nil {
		return err
	}
	cfg, err := rankingRulesForDay(ctx, tx, appID, date)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `INSERT INTO qq_bot_ranking_settlements(app_id,ranking_day,rules,settled_at) VALUES($1,$2,$3::jsonb,$4) ON CONFLICT DO NOTHING`, appID, date, string(raw), now)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil || count == 0 {
		return err
	}
	if !cfg.RewardsEnabled {
		return tx.Commit()
	}
	entries, err := dailyRankingEntries(ctx, tx, start, start.AddDate(0, 0, 1))
	if err != nil {
		return err
	}
	prizeFor := func(entry rankingEntry) GrowthReward {
		prizes := cfg.TokenRewards
		if entry.Kind == "amount" {
			prizes = cfg.AmountRewards
		}
		for _, prize := range prizes {
			if prize.Rank == entry.Rank {
				return prize.Reward
			}
		}
		return GrowthReward{}
	}
	// Lock accounts in a fixed order even if the two boards have different winners.
	sort.SliceStable(entries, func(i, j int) bool { return entries[i].UserID < entries[j].UserID })
	grants := 0
	for _, entry := range entries {
		reward := prizeFor(entry)
		if !reward.enabled() {
			continue
		}
		var uid int64
		err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 AND status='active' AND deleted_at IS NULL FOR UPDATE`, entry.UserID).Scan(&uid)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return err
		}
		key := fmt.Sprintf("qq-ranking:%s:%s:%s:%d", appID, date, entry.Kind, uid)
		// Preserve rewards paid by the former automatic settlement implementation.
		claimed, claimErr := activityRewardClaimed(ctx, tx, key)
		if claimErr != nil {
			return claimErr
		}
		if claimed {
			continue
		}
		if err = reward.validate(); err != nil {
			return err
		}
		rewardJSON, marshalErr := json.Marshal(reward)
		if marshalErr != nil {
			return marshalErr
		}
		result, err = tx.ExecContext(ctx, `INSERT INTO qq_bot_ranking_claims(app_id,ranking_day,ranking_kind,rank,user_id,source_key,reward,created_at)
 VALUES($1,$2,$3,$4,$5,$6,$7::jsonb,$8) ON CONFLICT DO NOTHING`, appID, date, entry.Kind, entry.Rank, uid, key, string(rewardJSON), now)
		if err != nil {
			return err
		}
		count, err = result.RowsAffected()
		if err != nil {
			return err
		}
		grants += int(count)
	}
	if _, err = tx.ExecContext(ctx, `UPDATE qq_bot_ranking_settlements SET reward_count=$3 WHERE app_id=$1 AND ranking_day=$2`, appID, date, grants); err != nil {
		return err
	}
	return tx.Commit()
}
