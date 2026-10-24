// Command hello is the small main package binsize's tests build for real,
// for each platform, instead of all of pleiades.
package main

import "fmt"

// main prints a line, so the binary links fmt like a real command.
func main() {
	fmt.Println("hello")
}
