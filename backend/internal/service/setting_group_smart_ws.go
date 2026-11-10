package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const groupSmartWSPrefix = "group_smart_ws:"
const groupSmartWSCacheTTL = time.Second
const groupSmartWSCacheLimit = 4096
const groupSmartWSMaxRevision int64 = 9007199254740991

var (
	ErrGroupSmartWSUnsupported = infraerrors.BadRequest("GROUP_SMART_WS_UNSUPPORTED", "智能 WS 优先仅支持 OpenAI 分组")
	ErrGroupSmartWSConflict    = infraerrors.New(409, "GROUP_SMART_WS_CONFLICT", "此分组的 WS 设置已更新，请重新加载后再保存")
	ErrGroupSmartWSUnavailable = infraerrors.New(503, "GROUP_SMART_WS_UNAVAILABLE", "暂时无法读取或保存 WS 设置，请稍后重试")
)

type GroupSmartWSPolicy struct {
	Enabled  bool  `json:"enabled"`
	Revision int64 `json:"revision"`
}

type groupSmartWSCacheEntry struct {
	enabled bool
	expires time.Time
}

// This optional capability keeps unrelated setting repository implementations compatible.
type GroupSmartWSCompareAndSwapper interface {
	CompareAndSwapGroupSmartWS(ctx context.Context, groupID int64, key string, previous *string, next string) (bool, error)
}

func groupSmartWSKey(groupID int64) string {
	return groupSmartWSPrefix + strconv.FormatInt(groupID, 10)
}

func decodeGroupSmartWS(raw string) (GroupSmartWSPolicy, error) {
	var policy GroupSmartWSPolicy
	dec := json.NewDecoder(strings.NewReader(raw))
	dec.DisallowUnknownFields()
	var wire struct {
		Enabled  *bool  `json:"enabled"`
		Revision *int64 `json:"revision"`
	}
	if err := dec.Decode(&wire); err != nil {
		return policy, err
	}
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return policy, errors.New("invalid WS setting document")
	}
	if wire.Enabled == nil || wire.Revision == nil || *wire.Revision < 1 || *wire.Revision > groupSmartWSMaxRevision {
		return policy, errors.New("invalid WS setting fields")
	}
	return GroupSmartWSPolicy{Enabled: *wire.Enabled, Revision: *wire.Revision}, nil
}

func (s *SettingService) validateGroupSmartWS(ctx context.Context, groupID int64) error {
	if groupID <= 0 {
		return ErrGroupNotFound
	}
	if s == nil || s.defaultSubGroupReader == nil || s.settingRepo == nil {
		return ErrGroupSmartWSUnavailable
	}
	group, err := s.defaultSubGroupReader.GetByID(ctx, groupID)
	if err != nil {
		return err
	}
	if group == nil || group.ID != groupID {
		return ErrGroupNotFound
	}
	if group.Platform != PlatformOpenAI {
		return ErrGroupSmartWSUnsupported
	}
	return nil
}

func (s *SettingService) readGroupSmartWS(ctx context.Context, groupID int64) (GroupSmartWSPolicy, *string, error) {
	raw, err := s.settingRepo.GetValue(ctx, groupSmartWSKey(groupID))
	if errors.Is(err, ErrSettingNotFound) {
		return GroupSmartWSPolicy{}, nil, nil
	}
	if err != nil {
		return GroupSmartWSPolicy{}, nil, ErrGroupSmartWSUnavailable.WithCause(err)
	}
	policy, err := decodeGroupSmartWS(raw)
	if err != nil {
		return GroupSmartWSPolicy{}, nil, ErrGroupSmartWSUnavailable.WithCause(err)
	}
	return policy, &raw, nil
}

func (s *SettingService) GetGroupSmartWS(ctx context.Context, groupID int64) (GroupSmartWSPolicy, error) {
	if err := s.validateGroupSmartWS(ctx, groupID); err != nil {
		return GroupSmartWSPolicy{}, err
	}
	policy, _, err := s.readGroupSmartWS(ctx, groupID)
	return policy, err
}

// UpdateGroupSmartWS does not create a row on reads. Revision zero means absent/off.
func (s *SettingService) UpdateGroupSmartWS(ctx context.Context, groupID int64, enabled bool, expectedRevision int64) (GroupSmartWSPolicy, error) {
	if err := s.validateGroupSmartWS(ctx, groupID); err != nil {
		return GroupSmartWSPolicy{}, err
	}
	if expectedRevision < 0 || expectedRevision >= groupSmartWSMaxRevision {
		return GroupSmartWSPolicy{}, ErrGroupSmartWSConflict
	}
	policy, previous, err := s.readGroupSmartWS(ctx, groupID)
	if err != nil {
		return GroupSmartWSPolicy{}, err
	}
	if policy.Revision != expectedRevision {
		return GroupSmartWSPolicy{}, ErrGroupSmartWSConflict
	}
	writer, ok := s.settingRepo.(GroupSmartWSCompareAndSwapper)
	if !ok {
		return GroupSmartWSPolicy{}, ErrGroupSmartWSUnavailable
	}
	next := GroupSmartWSPolicy{Enabled: enabled, Revision: policy.Revision + 1}
	raw, err := json.Marshal(next)
	if err != nil {
		return GroupSmartWSPolicy{}, fmt.Errorf("encode group WS policy: %w", err)
	}
	saved, err := writer.CompareAndSwapGroupSmartWS(ctx, groupID, groupSmartWSKey(groupID), previous, string(raw))
	// Invalidate in-flight reads as well as the local cache.
	s.groupSmartWSMu.Lock()
	delete(s.groupSmartWSCache, groupID)
	s.groupSmartWSGeneration++
	s.groupSmartWSMu.Unlock()
	if err != nil {
		return GroupSmartWSPolicy{}, err
	}
	if !saved {
		return GroupSmartWSPolicy{}, ErrGroupSmartWSConflict
	}
	return next, nil
}

// GroupSmartWSEnabled fails closed. Never reuse an expired enabled result on DB failure.
// Callers must pass the authenticated request group, never an account's group membership.
func (s *SettingService) GroupSmartWSEnabled(ctx context.Context, groupID int64) bool {
	if s == nil || s.settingRepo == nil || groupID <= 0 {
		return false
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if ctx.Err() != nil {
		return false
	}
	s.groupSmartWSMu.Lock()
	now := time.Now()
	if cached, ok := s.groupSmartWSCache[groupID]; ok && now.Before(cached.expires) {
		s.groupSmartWSMu.Unlock()
		return cached.enabled
	}
	epoch := s.groupSmartWSGeneration
	s.groupSmartWSMu.Unlock()
	dbCtx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	policy, _, err := s.readGroupSmartWS(dbCtx, groupID)
	enabled := err == nil && policy.Enabled
	s.groupSmartWSMu.Lock()
	defer s.groupSmartWSMu.Unlock()
	if epoch != s.groupSmartWSGeneration {
		return false
	}
	if s.groupSmartWSCache == nil || len(s.groupSmartWSCache) >= groupSmartWSCacheLimit {
		s.groupSmartWSCache = make(map[int64]groupSmartWSCacheEntry)
	}
	s.groupSmartWSCache[groupID] = groupSmartWSCacheEntry{enabled: enabled, expires: now.Add(groupSmartWSCacheTTL)}
	return enabled
}
