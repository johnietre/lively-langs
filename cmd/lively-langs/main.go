package main

import (
	"log"

	"github.com/johnietre/lively-langs/server"
	"github.com/spf13/cobra"
)

func main() {
	log.SetFlags(0)

	cmd := cobra.Command{
		Use:                   "lively-langs",
		DisableFlagsInUseLine: true,
	}
	cmd.AddCommand(server.MakeCmd())
	if err := cmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
