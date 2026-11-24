package pos

import (
	"errors"
	"fmt"
)

const layout = "node %-8p"

func A(x *int) string {
	return fmt.Sprintf("at %p", x)
}

func B(x *int) error {
	return fmt.Errorf(layout, x)
}

func C() error {
	return errors.New("%p is fine outside a format call")
}
