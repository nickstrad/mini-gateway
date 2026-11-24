package withtests_test

import (
	"sync"
	"testing"

	"example.com/fix/withtests"
)

func TestX(t *testing.T) {
	var once sync.Once
	once.Do(func() {})
	if withtests.Add(2, 2) != 4 {
		t.Fatal("bad")
	}
}
