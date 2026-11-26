package service

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/Reddetk/SayMoDev/progress-service/internal/port/out"
)

func TestTariffEffect(t *testing.T) {
	cases := []struct {
		event         string
		grant, revoke bool
		clear         bool
	}{
		{"TariffPlanActivated", true, true, false},
		{"TariffPlanChanged", true, true, false},
		{"TariffPlanDeActivated", false, true, true},
		{"SubscriptionActivated", true, true, false},
		{"SubscriptionChanged", true, true, false},
		{"SubscriptionRenewed", true, true, false},
		{"SubscriptionSuspended", false, true, false},
		{"SubscriptionExpired", false, true, false},
		{"SubscriptionResumed", true, true, false},
		{"SubscriptionDeActivated", false, false, false},
		{"SomethingElse", false, false, false},
	}
	for _, c := range cases {
		g, r, cl, _ := tariffEffect(c.event)
		assert.Equal(t, c.grant, g, "grant for %s", c.event)
		assert.Equal(t, c.revoke, r, "revoke for %s", c.event)
		assert.Equal(t, c.clear, cl, "clear for %s", c.event)
	}
}

func TestPickCurrentProgressRanksActiveFirst(t *testing.T) {
	mk := func(status string) out.ProgressRecord {
		return out.ProgressRecord{Status: status, ProgramID: uuid.New()}
	}
	assert.Equal(t, "active", pickCurrentProgress([]out.ProgressRecord{mk("paused"), mk("completed"), mk("active")}).Status)
	assert.Equal(t, "completed", pickCurrentProgress([]out.ProgressRecord{mk("paused"), mk("completed")}).Status)
	assert.Equal(t, "paused", pickCurrentProgress([]out.ProgressRecord{mk("paused")}).Status)
	assert.Nil(t, pickCurrentProgress(nil))
}
