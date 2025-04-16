package utils

import (
	"log"
	"os"
	"path"
)

var PackageDirectory = path.Join(GetNoxDirectory(), "packages")

func GetNoxDirectory() string {
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatal(err)
	}
	return path.Join(home, ".nox")
}
