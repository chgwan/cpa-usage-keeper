package auth

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"cpa-usage-keeper/internal/entities"
	"github.com/pquerna/otp"
	totplib "github.com/pquerna/otp/totp"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

const (
	totpSettingKey        = "auth.totp"
	totpPendingSettingKey = "auth.totp.pending"

	totpIssuer     = "CPA Usage Keeper"
	totpAccount    = "admin"
	totpPeriodSec  = 30
	totpDigitCount = 6
	totpSkewSteps  = 1

	totpPendingTTL = 10 * time.Minute
)

var (
	ErrTOTPNotEnrolled    = errors.New("totp: not enrolled")
	ErrTOTPNoPending      = errors.New("totp: no pending enrollment")
	ErrTOTPPendingExpired = errors.New("totp: pending enrollment expired")
)

// TOTPEnrollment 是已启用的管理员 TOTP 注册状态，LastStep 用于拒绝窗口内重放。
type TOTPEnrollment struct {
	Secret    string    `json:"secret"`
	EnabledAt time.Time `json:"enabled_at"`
	LastStep  int64     `json:"last_step"`
}

// TOTPPendingEnrollment 是尚未确认的注册，10 分钟内必须完成确认。
type TOTPPendingEnrollment struct {
	Secret    string    `json:"secret"`
	CreatedAt time.Time `json:"created_at"`
}

type TOTPManager struct {
	db  *gorm.DB
	now func() time.Time
}

func NewTOTPManager(db *gorm.DB) *TOTPManager {
	return NewTOTPManagerWithClock(db, time.Now)
}

func NewTOTPManagerWithClock(db *gorm.DB, now func() time.Time) *TOTPManager {
	if now == nil {
		now = time.Now
	}
	return &TOTPManager{db: db, now: now}
}

func (m *TOTPManager) Enrolled(ctx context.Context) bool {
	_, found := m.loadEnrollment(ctx)
	return found
}

func (m *TOTPManager) HasPending(ctx context.Context) bool {
	_, found := m.loadPending(ctx)
	return found
}

func (m *TOTPManager) CreatePending(ctx context.Context) (string, string, error) {
	if m == nil || m.db == nil {
		return "", "", fmt.Errorf("totp manager is not configured")
	}
	key, err := totplib.Generate(totplib.GenerateOpts{
		Issuer:      totpIssuer,
		AccountName: totpAccount,
		Period:      totpPeriodSec,
		Digits:      otp.DigitsSix,
		Algorithm:   otp.AlgorithmSHA1,
	})
	if err != nil {
		return "", "", fmt.Errorf("generate totp key: %w", err)
	}
	pending := TOTPPendingEnrollment{Secret: key.Secret(), CreatedAt: m.now()}
	if err := m.saveSetting(ctx, totpPendingSettingKey, &pending); err != nil {
		return "", "", err
	}
	return key.URL(), key.Secret(), nil
}

func (m *TOTPManager) ConfirmPending(ctx context.Context, code string) (bool, error) {
	pending, found := m.loadPending(ctx)
	if !found {
		return false, ErrTOTPNoPending
	}
	now := m.now()
	if now.Sub(pending.CreatedAt) > totpPendingTTL {
		if err := m.deleteSettings(ctx, []string{totpPendingSettingKey}); err != nil {
			return false, err
		}
		return false, ErrTOTPPendingExpired
	}
	step, ok := totpVerifyStep(pending.Secret, code, now)
	if !ok {
		return false, nil
	}
	enrollment := TOTPEnrollment{Secret: pending.Secret, EnabledAt: now, LastStep: step}
	if err := m.saveSetting(ctx, totpSettingKey, &enrollment); err != nil {
		return false, err
	}
	if err := m.deleteSettings(ctx, []string{totpPendingSettingKey}); err != nil {
		return false, err
	}
	return true, nil
}

func (m *TOTPManager) Verify(ctx context.Context, code string) (bool, error) {
	enrollment, found := m.loadEnrollment(ctx)
	if !found {
		return false, ErrTOTPNotEnrolled
	}
	now := m.now()
	step, ok := totpVerifyStep(enrollment.Secret, code, now)
	// 重放保护优先于 ±1 步的时钟容忍：不接受已使用过的时间步。
	if !ok || step <= enrollment.LastStep {
		return false, nil
	}
	enrollment.LastStep = step
	if err := m.saveSetting(ctx, totpSettingKey, &enrollment); err != nil {
		return false, err
	}
	return true, nil
}

func (m *TOTPManager) Disable(ctx context.Context) error {
	return m.ResetAll(ctx)
}

func (m *TOTPManager) ResetAll(ctx context.Context) error {
	if m == nil || m.db == nil {
		return fmt.Errorf("totp manager is not configured")
	}
	return m.deleteSettings(ctx, []string{totpSettingKey, totpPendingSettingKey})
}

// totpVerifyStep 在 ±1 步窗口内重新生成期望码并做常数时间比较，返回命中的最大时间步。
func totpVerifyStep(secret, code string, now time.Time) (int64, bool) {
	code = strings.TrimSpace(code)
	if code == "" {
		return -1, false
	}
	best := int64(-1)
	for offset := -totpSkewSteps; offset <= totpSkewSteps; offset++ {
		at := now.Add(time.Duration(offset*totpPeriodSec) * time.Second)
		expected, err := totplib.GenerateCodeCustom(secret, at, totplib.ValidateOpts{
			Period:    totpPeriodSec,
			Skew:      0,
			Digits:    otp.Digits(totpDigitCount),
			Algorithm: otp.AlgorithmSHA1,
		})
		if err != nil {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(expected), []byte(code)) == 1 {
			if step := at.Unix() / totpPeriodSec; step > best {
				best = step
			}
		}
	}
	return best, best >= 0
}

func (m *TOTPManager) loadEnrollment(ctx context.Context) (TOTPEnrollment, bool) {
	if m == nil || m.db == nil {
		return TOTPEnrollment{}, false
	}
	var setting entities.AppSetting
	err := m.db.WithContext(ctx).Where(&entities.AppSetting{SettingKey: totpSettingKey}).First(&setting).Error
	if err != nil || setting.Value == nil {
		return TOTPEnrollment{}, false
	}
	var enrollment TOTPEnrollment
	if err := json.Unmarshal([]byte(*setting.Value), &enrollment); err != nil || enrollment.Secret == "" {
		return TOTPEnrollment{}, false
	}
	return enrollment, true
}

func (m *TOTPManager) loadPending(ctx context.Context) (TOTPPendingEnrollment, bool) {
	if m == nil || m.db == nil {
		return TOTPPendingEnrollment{}, false
	}
	var setting entities.AppSetting
	err := m.db.WithContext(ctx).Where(&entities.AppSetting{SettingKey: totpPendingSettingKey}).First(&setting).Error
	if err != nil || setting.Value == nil {
		return TOTPPendingEnrollment{}, false
	}
	var pending TOTPPendingEnrollment
	if err := json.Unmarshal([]byte(*setting.Value), &pending); err != nil || pending.Secret == "" {
		return TOTPPendingEnrollment{}, false
	}
	return pending, true
}

func (m *TOTPManager) saveSetting(ctx context.Context, key string, payload any) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode totp setting %s: %w", key, err)
	}
	value := string(raw)
	now := m.now()
	setting := entities.AppSetting{
		SettingKey: key,
		Value:      &value,
		ValueType:  entities.AppSettingValueTypeJSON,
		CreatedAt:  now,
		UpdatedAt:  now,
	}
	if err := m.db.WithContext(ctx).Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "setting_key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value", "value_type", "updated_at"}),
	}).Create(&setting).Error; err != nil {
		return fmt.Errorf("save totp setting %s: %w", key, err)
	}
	return nil
}

func (m *TOTPManager) deleteSettings(ctx context.Context, keys []string) error {
	return m.db.WithContext(ctx).
		Where("setting_key IN ?", keys).
		Delete(&entities.AppSetting{}).Error
}
