package main

import (
	"os"

	"wr/cmd"
)

func main() {
	cmd.InitApp()
	os.Exit(cmd.Execute())
}
