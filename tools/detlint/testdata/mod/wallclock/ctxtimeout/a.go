package ctxtimeout

import (
	"context"
	"time"
)

func F(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, time.Second)
}
