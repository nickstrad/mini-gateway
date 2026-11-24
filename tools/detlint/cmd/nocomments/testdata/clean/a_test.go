package a

import "fmt"

func ExampleF() {
	fmt.Println(1)
	fmt.Println(2)
	// Output:
	// 1
	// 2
}

func Example_unordered() {
	fmt.Println(2)
	fmt.Println(1)
	// Unordered output:
	// 1
	// 2
}
