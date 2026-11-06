package main

import (
	"fmt"

	"chakram/internal/version"
)

func cmdVersion() {
	fmt.Printf("chakramctl %s\n", version.Version)
	fmt.Println("Chakram CometBFT+Engine API blockchain node operator CLI")
}
