package test

import "testing"

// TestInitializeInstallsForkBackgroundRunners 锁定启动流水线移交 fork 独有的后台 runner：
// 漏掉移交时限额执行与本地排行聚合会在生产中静默停摆。
func TestInitializeInstallsForkBackgroundRunners(t *testing.T) {
	application := newInitializedApp(t, testAppConfig(t))
	if application.KeyPolicyEnforcement == nil {
		t.Fatal("Initialize did not install the key policy enforcement runner")
	}
	if application.LocalRanking == nil {
		t.Fatal("Initialize did not install the local ranking runner")
	}
}
