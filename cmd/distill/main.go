package main

import (
	"os"

	"github.com/msmedes/distill/internal/distill"
)

func main() {
	os.Exit(distill.Main(os.Args))
}
