package withtests

import (
	"testing"
	"time"
)

func TestAdd(t *testing.T) {
	_ = time.Now()
	if Add(1, 2) != 3 {
		t.Fatal("bad")
	}
}
