// Copyright © 2017 NAME HERE <EMAIL ADDRESS>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"

	"github.com/osrg/dlk/dlkctl/utils"
	"github.com/spf13/cobra"
)

//NewCommandImageDelete generate cobra cmd "dlkctl image `delete`"
func NewCommandImageDelete() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "delete",
		Short:   "Delete dockerimage",
		Long:    `Delete docker image from registry `,
		Aliases: []string{"image-del"},
		Run:     deleteDockerImage,
	}

	//set local flag

	//add subcommand

	return cmd
}

//exec parameter
type ImageDeleteConfig struct {
	pf *PersistentFlags
}

//Main Proceduer of get learningTasks command
func deleteDockerImage(cmd *cobra.Command, args []string) {

	//parameter check
	fmt.Println("*** CHECK PARAMS ***")
	idc := ImageDeleteConfig{}
	err := idc.checkParams(cmd, args)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
	fmt.Println("completed")
	fmt.Println("*** delete image ***")

	c := utils.NewDockerClient(idc.pf.docker, idc.pf.registry)
	for _, str := range args {

		//get image digest for using in registry delete api
		image, tag := c.SeparateTagrepoIntoImageAndTag(str)
		if tag == "" {
			fmt.Println("tag is not specified,use 'latest' as tag")
			tag = "latest"
		}

		digest, err := c.GetImageDigest(image, tag)
		if err != nil {
			fmt.Println(err.Error())
			continue
		}

		fmt.Printf("Delete Image: %s :", str)
		err = c.DeleteImage(image, digest)
		if err != nil {
			fmt.Println(" NG")
			fmt.Println(err.Error())
		} else {
			fmt.Println(" OK")
		}

	}
}

//checkParams check and get exec parameter
func (idc *ImageDeleteConfig) checkParams(cmd *cobra.Command, args []string) error {
	var err error

	//check and get persistent flag volume
	var pf *PersistentFlags
	pf, err = CheckPersistentFlags()
	if err != nil {
		return err
	}

	// check passed argument
	r, _ := regexp.Compile("^[a-zA-Z0-9]")
	e := false
	for _, image := range args {

		if !r.MatchString(image) {
			fmt.Printf("unexpected argument: %s\n", image)
			e = true
		}
	}

	if e {
		return errors.New("unexpected arguments")
	}
	//set config values
	idc.pf = pf

	return err
}
