package main

import (
	"github.com/spf13/cobra"

	deprecations "github.com/redpanda-data/common-go/rp-controller-gen/deprecation-tests"
	"github.com/redpanda-data/common-go/rp-controller-gen/status"
)

func main() {
	root := cobra.Command{
		Use:  "rp-controller-gen",
		Long: "rp-controller-gen is the hub module for hosting various file generation tasks within Redpanda's controllers.",
	}

	root.AddCommand(
		status.Cmd(),
		deprecations.Cmd(),
	)

	if err := root.Execute(); err != nil {
		panic(err)
	}
}
