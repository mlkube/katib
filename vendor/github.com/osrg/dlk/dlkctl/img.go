package main

import (
	"github.com/spf13/cobra"
)

//NewCommandImage generate image cmd
func NewCommandImage() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "image",
		Short: "Manage Docker images",
		Long:  `Create/Display/Delete Docker images`,
	}

	//set local flag

	//add subcommand
	//create
	cmd.AddCommand(NewCommandImageCreate())
	cmd.AddCommand(NewCommandImageDelete())

	//list
	cmd.AddCommand(NewCommandImageList())

	return cmd
}
