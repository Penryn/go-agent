package policy

import (
	"testing"
	"time"

	"github.com/phlin/go-agent/internal/config"
	policydomain "github.com/phlin/go-agent/internal/domain/policy"
)

func TestQuietHourActive(t *testing.T) {
	svc := New(config.Config{DefaultPolicy: policydomain.GroupPolicy{QuietHours: []string{"23:00-06:00"}}})
	at := func(clock string) time.Time {
		parsed, err := time.ParseInLocation("2006-01-02 15:04", "2026-09-01 "+clock, time.Local)
		if err != nil {
			t.Fatal(err)
		}
		return parsed
	}
	cases := map[string]bool{
		"02:00": true,  // 跨午夜中段
		"23:30": true,  // 起始段
		"05:59": true,  // 结束段边界
		"06:01": false, // 出界一分钟
		"14:00": false, // 白天
	}
	for clock, want := range cases {
		if got := svc.QuietHourActive(at(clock), svc.defaultPolicy); got != want {
			t.Fatalf("QuietHourActive(%s) = %v, want %v", clock, got, want)
		}
	}
}
