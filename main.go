package main

import (
	"fmt"
	"github.com/coding-ia/packer-plugin-ebstpm/internal/post-processor/ebstpm"
	"github.com/hashicorp/packer-plugin-sdk/plugin"
	"github.com/hashicorp/packer-plugin-sdk/version"
	"os"
)

func main() {
	pps := plugin.NewSet()
	pps.RegisterPostProcessor("create", new(ebstpm.PostProcessor))
	pps.SetVersion(version.SDKVersion)
	err := pps.Run()
	if err != nil {
		fmt.Fprintln(os.Stderr, err.Error())
		os.Exit(1)
	}
}
