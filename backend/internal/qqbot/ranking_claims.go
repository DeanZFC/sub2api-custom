package qqbot

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// R-prefixed persistent IDs cannot shift when tasks are reordered, another
// reward is claimed, or a new daily settlement is added. IDs are not secrets;
// both listing and claiming are scoped to the current AppID and bound user.
func rankingClaimID(value string) (int64, bool) {
	if !strings.HasPrefix(value, "R") {
		return 0, false
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(value, "R"), 10, 64)
	return id, err == nil && id > 0 && value == "R"+strconv.FormatInt(id, 10)
}

func rankingClaimName(date, kind string, rank int) string {
	return fmt.Sprintf("%s %s 第 %d 名", date, rankingTitle(kind), rank)
}

func rankingGrowthTasks(ctx context.Context, tx *sql.Tx, appID string, uid int64, actions ...*[]keyboardButton) (string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,ranking_day::text,ranking_kind,rank,reward,claimed_at
 FROM qq_bot_ranking_claims WHERE app_id=$1 AND user_id=$2
 AND (claimed_at IS NULL OR id IN (SELECT id FROM qq_bot_ranking_claims
 WHERE app_id=$1 AND user_id=$2 AND claimed_at IS NOT NULL ORDER BY ranking_day DESC,id DESC LIMIT 10))
 ORDER BY (claimed_at IS NOT NULL),ranking_day DESC,id DESC`, appID, uid)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	text := "## 周期性成长任务\n\n每日排行奖励 · 北京时间次日 00:10 结算资格。发送 /今日排行榜 查看实时名次。"
	count := 0
	for rows.Next() {
		var id int64
		var date, kind string
		var rank int
		var raw []byte
		var claimed sql.NullTime
		if err = rows.Scan(&id, &date, &kind, &rank, &raw, &claimed); err != nil {
			return "", err
		}
		var reward GrowthReward
		if err = json.Unmarshal(raw, &reward); err != nil {
			return "", err
		}
		number := "R" + strconv.FormatInt(id, 10)
		status := "已领取"
		if !claimed.Valid {
			command := "/福利 领取 " + number
			status = "可领取 · " + command
			if len(actions) > 0 && actions[0] != nil {
				*actions[0] = append(*actions[0], helpCommandButton("领取 "+number, "welfare:ranking:"+number, command))
			}
		}
		text += fmt.Sprintf("\n\n**%s. %s**\n奖励：%s · %s", number, rankingClaimName(date, kind, rank), reward.description(), status)
		count++
	}
	if err = rows.Err(); err != nil {
		return "", err
	}
	if count == 0 {
		text += "\n\n暂无排行奖励记录；奖励开启且达到指定名次后，次日结算生成待领取任务。"
	}
	return text + "\n\n每天每榜分别领取，两个榜可同时获奖。待领取奖励长期保留，已领取仅展示最近 10 项；R 编号固定不变。领取后余额与次数到账，实物及自定义奖品由管理员发放。", nil
}

// growthTasks holds the config, binding and active-user locks. Granting and
// consuming the frozen entitlement share one transaction, including rollback.
func (s *Service) claimRankingReward(ctx context.Context, tx *sql.Tx, c Config, j Job, uid, id int64, at time.Time) (string, error) {
	var date, kind, key string
	var rank int
	var raw []byte
	var claimed sql.NullTime
	err := tx.QueryRowContext(ctx, `SELECT ranking_day::text,ranking_kind,rank,source_key,reward,claimed_at
 FROM qq_bot_ranking_claims WHERE id=$1 AND app_id=$2 AND user_id=$3 FOR UPDATE`, id, c.AppID, uid).Scan(&date, &kind, &rank, &key, &raw, &claimed)
	if errors.Is(err, sql.ErrNoRows) {
		return "任务不存在或不属于当前账号，请发送 /福利 查看自己的任务。", nil
	}
	if err != nil {
		return "", err
	}
	if claimed.Valid {
		return "该周期性成长任务奖励已领取。", nil
	}
	var reward GrowthReward
	if err = json.Unmarshal(raw, &reward); err != nil {
		return "", err
	}
	name := rankingClaimName(date, kind, rank)
	granted, err := grantActivityReward(ctx, tx, c, j, uid, key, "ranking", name, reward, at)
	if err != nil {
		return "", err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE qq_bot_ranking_claims SET claimed_at=$2 WHERE id=$1`, id, at); err != nil {
		return "", err
	}
	if err = tx.Commit(); err != nil {
		return "", err
	}
	if !granted {
		return "该周期性成长任务奖励已领取。", nil
	}
	if reward.Type == "balance" {
		s.invalidate(ctx, uid)
	}
	return fmt.Sprintf("✅ **周期性成长奖励已领取**\n\n%s\n奖励：%s\n%s", name, reward.description(), reward.deliveryNote()), nil
}
