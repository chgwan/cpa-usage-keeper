package test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"cpa-usage-keeper/internal/auth"
	"cpa-usage-keeper/internal/entities"
	"github.com/pquerna/otp/totp"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func openTOTPDatabase(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "totp.db")), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite database: %v", err)
	}
	if err := db.AutoMigrate(&entities.AppSetting{}); err != nil {
		t.Fatalf("migrate app settings: %v", err)
	}
	t.Cleanup(func() {
		if sqlDB, err := db.DB(); err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func fixedTOTPClock(at time.Time) func() time.Time {
	return func() time.Time { return at }
}

func mustEnrolled(t *testing.T, manager *auth.TOTPManager, ctx context.Context) bool {
	t.Helper()
	enrolled, err := manager.Enrolled(ctx)
	if err != nil {
		t.Fatalf("read enrollment state: %v", err)
	}
	return enrolled
}

func TestTOTPManagerCreateConfirmVerify(t *testing.T) {
	db := openTOTPDatabase(t)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	manager := auth.NewTOTPManagerWithClock(db, fixedTOTPClock(now))
	ctx := context.Background()

	if mustEnrolled(t, manager, ctx) {
		t.Fatal("expected no enrollment initially")
	}
	uri, secret, err := manager.CreatePending(ctx)
	if err != nil {
		t.Fatalf("create pending: %v", err)
	}
	if uri == "" || secret == "" {
		t.Fatalf("expected otpauth uri and secret, got %q %q", uri, secret)
	}
	if !manager.HasPending(ctx) {
		t.Fatal("expected pending enrollment after setup")
	}
	if mustEnrolled(t, manager, ctx) {
		t.Fatal("pending must not count as enrolled")
	}

	code, err := totp.GenerateCode(secret, now)
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	confirmed, err := manager.ConfirmPending(ctx, code)
	if err != nil || !confirmed {
		t.Fatalf("confirm pending: confirmed=%v err=%v", confirmed, err)
	}
	if !mustEnrolled(t, manager, ctx) {
		t.Fatal("expected enrollment after confirm")
	}
	if manager.HasPending(ctx) {
		t.Fatal("expected pending cleared after confirm")
	}

	// Same code again is a replay and must fail.
	replayed, err := manager.Verify(ctx, code)
	if err != nil || replayed {
		t.Fatalf("replayed code accepted: replayed=%v err=%v", replayed, err)
	}
	nextCode, err := totp.GenerateCode(secret, now.Add(30*time.Second))
	if err != nil {
		t.Fatalf("generate next code: %v", err)
	}
	valid, err := manager.Verify(ctx, nextCode)
	if err != nil || !valid {
		t.Fatalf("next-step code rejected: valid=%v err=%v", valid, err)
	}
}

func TestTOTPManagerConfirmRejectsWrongCode(t *testing.T) {
	db := openTOTPDatabase(t)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	manager := auth.NewTOTPManagerWithClock(db, fixedTOTPClock(now))
	ctx := context.Background()

	_, secret, err := manager.CreatePending(ctx)
	if err != nil {
		t.Fatalf("create pending: %v", err)
	}
	rightCode, err := totp.GenerateCode(secret, now)
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	wrongCode := "000000"
	if wrongCode == rightCode {
		wrongCode = "111111"
	}
	confirmed, err := manager.ConfirmPending(ctx, wrongCode)
	if err != nil || confirmed {
		t.Fatalf("wrong code confirmed: confirmed=%v err=%v", confirmed, err)
	}
	if !manager.HasPending(ctx) {
		t.Fatal("wrong code must keep the pending enrollment")
	}
}

func TestTOTPManagerPendingExpiresAfterTenMinutes(t *testing.T) {
	db := openTOTPDatabase(t)
	start := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()

	setupManager := auth.NewTOTPManagerWithClock(db, fixedTOTPClock(start))
	_, secret, err := setupManager.CreatePending(ctx)
	if err != nil {
		t.Fatalf("create pending: %v", err)
	}
	later := start.Add(11 * time.Minute)
	code, err := totp.GenerateCode(secret, later)
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	expiredManager := auth.NewTOTPManagerWithClock(db, fixedTOTPClock(later))
	confirmed, err := expiredManager.ConfirmPending(ctx, code)
	if confirmed || !errors.Is(err, auth.ErrTOTPPendingExpired) {
		t.Fatalf("expected expired error, got confirmed=%v err=%v", confirmed, err)
	}
}

func TestTOTPManagerConfirmWithoutPending(t *testing.T) {
	db := openTOTPDatabase(t)
	manager := auth.NewTOTPManagerWithClock(db, fixedTOTPClock(time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)))
	confirmed, err := manager.ConfirmPending(context.Background(), "123456")
	if confirmed || !errors.Is(err, auth.ErrTOTPNoPending) {
		t.Fatalf("expected no-pending error, got confirmed=%v err=%v", confirmed, err)
	}
}

func TestTOTPManagerVerifyWithoutEnrollment(t *testing.T) {
	db := openTOTPDatabase(t)
	manager := auth.NewTOTPManager(db)
	valid, err := manager.Verify(context.Background(), "123456")
	if valid || !errors.Is(err, auth.ErrTOTPNotEnrolled) {
		t.Fatalf("expected not-enrolled error, got valid=%v err=%v", valid, err)
	}
}

func TestTOTPManagerEnrolledReportsStorageFailure(t *testing.T) {
	db := openTOTPDatabase(t)
	manager := auth.NewTOTPManager(db)
	if err := db.Migrator().DropTable(&entities.AppSetting{}); err != nil {
		t.Fatalf("drop app settings: %v", err)
	}

	enrolled, err := manager.Enrolled(context.Background())
	if err == nil {
		t.Fatal("expected an unreadable enrollment store to return an error instead of 'not enrolled'")
	}
	if enrolled {
		t.Fatal("expected enrolled to be false alongside the error")
	}
}

func TestTOTPManagerRejectsUnreadableEnrollmentInsteadOfSkippingTOTP(t *testing.T) {
	db := openTOTPDatabase(t)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	manager := auth.NewTOTPManagerWithClock(db, fixedTOTPClock(now))
	ctx := context.Background()

	_, secret, err := manager.CreatePending(ctx)
	if err != nil {
		t.Fatalf("create pending: %v", err)
	}
	code, err := totp.GenerateCode(secret, now)
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	if confirmed, err := manager.ConfirmPending(ctx, code); err != nil || !confirmed {
		t.Fatalf("confirm pending: confirmed=%v err=%v", confirmed, err)
	}
	if err := db.Model(&entities.AppSetting{}).Where("setting_key = ?", "auth.totp").Update("value", "{broken").Error; err != nil {
		t.Fatalf("corrupt enrollment: %v", err)
	}

	enrolled, err := manager.Enrolled(ctx)
	if err == nil || enrolled {
		t.Fatalf("expected corrupted enrollment to error, got enrolled=%v err=%v", enrolled, err)
	}
	nextCode, err := totp.GenerateCode(secret, now.Add(30*time.Second))
	if err != nil {
		t.Fatalf("generate next code: %v", err)
	}
	valid, err := manager.Verify(ctx, nextCode)
	if valid {
		t.Fatal("expected verification against a corrupted enrollment to fail")
	}
	if err == nil || errors.Is(err, auth.ErrTOTPNotEnrolled) {
		t.Fatalf("corrupted enrollment must not be reported as 'not enrolled', got %v", err)
	}
}

func TestTOTPManagerDisableAndResetAllClearState(t *testing.T) {
	db := openTOTPDatabase(t)
	now := time.Date(2026, 8, 27, 12, 0, 0, 0, time.UTC)
	manager := auth.NewTOTPManagerWithClock(db, fixedTOTPClock(now))
	ctx := context.Background()

	_, secret, err := manager.CreatePending(ctx)
	if err != nil {
		t.Fatalf("create pending: %v", err)
	}
	code, err := totp.GenerateCode(secret, now)
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	if confirmed, err := manager.ConfirmPending(ctx, code); err != nil || !confirmed {
		t.Fatalf("confirm pending: confirmed=%v err=%v", confirmed, err)
	}
	if err := manager.Disable(ctx); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if mustEnrolled(t, manager, ctx) || manager.HasPending(ctx) {
		t.Fatal("expected all state cleared after disable")
	}

	// Re-enroll, then ResetAll must clear it too.
	_, secret, err = manager.CreatePending(ctx)
	if err != nil {
		t.Fatalf("create pending: %v", err)
	}
	code, err = totp.GenerateCode(secret, now)
	if err != nil {
		t.Fatalf("generate code: %v", err)
	}
	if confirmed, err := manager.ConfirmPending(ctx, code); err != nil || !confirmed {
		t.Fatalf("confirm pending: confirmed=%v err=%v", confirmed, err)
	}
	if err := manager.ResetAll(ctx); err != nil {
		t.Fatalf("reset all: %v", err)
	}
	if mustEnrolled(t, manager, ctx) || manager.HasPending(ctx) {
		t.Fatal("expected all state cleared after reset")
	}
}
