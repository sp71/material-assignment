package memfs

import "fmt"

// ExampleFS reproduces the walkthrough from the assignment prompt, end to end.
// It doubles as godoc documentation and as a verified integration test (the
// // Output: block below is checked by `go test`).
func ExampleFS() {
	fs := New()

	must(fs.Mkdir("school"))
	must(fs.Cd("school"))
	fmt.Println(fs.Pwd()) // /school

	must(fs.Mkdir("homework"))
	must(fs.Cd("homework"))
	must(fs.Mkdir("math"))
	must(fs.Mkdir("lunch"))
	must(fs.Mkdir("history"))
	must(fs.Mkdir("spanish"))
	must(fs.RemoveDir("lunch"))
	fmt.Println(fs.Ls())  // [history math spanish]
	fmt.Println(fs.Pwd()) // /school/homework

	must(fs.Cd(".."))
	must(fs.Mkdir("cheatsheet"))
	fmt.Println(fs.Ls()) // [cheatsheet homework]
	must(fs.RemoveDir("cheatsheet"))

	must(fs.Cd(".."))
	fmt.Println(fs.Pwd()) // /

	// Output:
	// /school
	// [history math spanish]
	// /school/homework
	// [cheatsheet homework]
	// /
}

// must panics on error. It exists only so the runnable example reads cleanly;
// examples take no *testing.T, so testify's require is unavailable here.
func must(err error) {
	if err != nil {
		panic(err)
	}
}
