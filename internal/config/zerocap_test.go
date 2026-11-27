package config_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/nickstrad/mini-gateway/internal/config"
	"github.com/nickstrad/mini-gateway/internal/proxy"
)

func parseWith(t *testing.T, key string, v any) (*config.File, error) {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(minimal), &m); err != nil {
		t.Fatal(err)
	}
	set(m, key, v)
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	return config.Parse(b)
}

// TestZeroCapMeansTheDefaultAndANegativeCapIsRefused proves narrowed claim 13:
// explicit zero takes the documented default while any negative cap is refused naming its key.
func TestZeroCapMeansTheDefaultAndANegativeCapIsRefused(t *testing.T) {
	def := proxy.Config{}.WithDefaults()
	for key, effective := range map[string]struct {
		got  func(*config.File) int
		want int
	}{
		"pool.connections":    {func(f *config.File) int { return f.Pool.Connections }, def.Connections},
		"queue.capacity":      {func(f *config.File) int { return f.Queue.Capacity }, def.QueueCap},
		"limits.max_clients":  {func(f *config.File) int { return f.Limits.MaxClients }, def.MaxClients},
		"limits.max_startup":  {func(f *config.File) int { return f.Limits.MaxStartup }, def.MaxStartup},
		"limits.reply_buffer": {func(f *config.File) int { return f.Limits.ReplyBuffer }, def.ReplyBufferCap},
	} {
		f, err := parseWith(t, key, 0)
		if err != nil {
			t.Fatalf("%s: 0: %v", key, err)
		}
		if got := effective.got(f); got != effective.want {
			t.Errorf("%s: 0 gave %d, want the default %d", key, got, effective.want)
		}
		if _, err := parseWith(t, key, -1); err == nil || !strings.Contains(err.Error(), key) {
			t.Errorf("%s: -1 gave %v, want an error naming the key", key, err)
		}
	}
}
