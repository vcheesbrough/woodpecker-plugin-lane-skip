package main

import (
	"fmt"
	"os"

	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/plugin"
	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/settings"
	"github.com/vcheesbrough/woodpecker-plugin-lane-skip/internal/version"
)

func main() {
	s, err := settings.FromEnv()
	if err == nil {
		if s.Mode != settings.ModeFiles {
			fmt.Printf("lane-skip %s: mode %s\n", version.ReleaseTag, s.Mode)
		}
		err = plugin.Run(s, os.Stdout)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "lane-skip: ERROR: %v\n", err)
		os.Exit(1)
	}
}
