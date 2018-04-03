package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/osrg/dlk/dlkctl/utils"
	"github.com/spf13/cobra"
)

type imgListCfg struct {
	params utils.Params
	pf     *PersistentFlags
	cli    *utils.DockerClient
}

//NewCommandImageList generate list cmd
func NewCommandImageList() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Args:  cobra.NoArgs,
		Short: "Display Docker image on registry",
		Long:  `Display Docker image list on private registry`,
		Run:   imgListMain,
	}

	//set local flag
	utils.AddImageFlag(cmd)

	//add subcommand
	return cmd
}

//Main Proceduer of create command
func imgListMain(cmd *cobra.Command, args []string) {

	//parameter check, init parameters
	fmt.Println("*** CHECK PARAMS ***")
	lc := imgListCfg{}
	err := lc.checkParams(cmd, args)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
	fmt.Println("completed")

	// display exec paramenters
	fmt.Println("*** exec parameters ***")
	lc.displayParams()

	// create docker client
	lc.cli = utils.NewDockerClient(lc.pf.docker, lc.pf.registry)

	// if image name is specified
	if lc.params.Image != "" {
		fmt.Println("*** image ***")

		// check image exist on private registry
		exist := false
		exist, err = lc.cli.IsImageExistOnRegistry(lc.params.Image, lc.pf.username)
		if err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
		if !exist {
			fmt.Println("Specified image does not exist on private registry")
			return
		}

		// get tags corresponding to image from private registry
		var tags *utils.Tags
		tags, err = lc.cli.SearchOnRegistry(lc.params.Image, lc.pf.username)
		if err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
		// display image and its tags
		displayImageTagsOnRegistry(lc.params.Image, tags)

		return
	}

	// get all images on private registry
	var imgs []utils.Repositories
	imgs, err = lc.cli.GetImageListOnRegistry()
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	// display all user's images
	fmt.Println("*** image ***")
	lc.displayImagesOnRegistry(imgs)
}

//checkParams check args and flag vailidity and return imgCreCfg struct
func (lc *imgListCfg) checkParams(cmd *cobra.Command, args []string) error {
	var err error

	//check and get persistent flag
	var pf *PersistentFlags
	pf, err = CheckPersistentFlags()
	if err != nil {
		return err
	}

	//check Flags using common parameter checker
	var params utils.Params
	params, err = utils.CheckFlags(cmd)
	if err != nil {
		return err
	}

	lc.params = params
	lc.pf = pf

	return err
}

// display parameters
func (lc *imgListCfg) displayParams() {
	fmt.Printf("| %-30s : %s\n", "docker daemon API endpoint", lc.pf.docker)
	fmt.Printf("| %-30s : %s\n", "docker registry endpoint", lc.pf.registry)
	fmt.Printf("| %-30s : %s\n", "username", lc.pf.username)
	if lc.params.Image != "" {
		fmt.Printf("| %-30s : %s\n", "docker image", lc.params.Image)
	}
}

// display image and tags on registry
func displayImageTagsOnRegistry(inm string, tgs *utils.Tags) {

	// in case of no tag, display image only
	if len(tgs.TagNames) == 0 {
		fmt.Println(inm)
		return
	}

	// display "image:tag"
	for _, t := range tgs.TagNames {
		fmt.Printf("%s:%s\n", inm, t)
	}
}

// display all user's images on registry
func (lc *imgListCfg) displayImagesOnRegistry(ims []utils.Repositories) {

	// get "username/"
	u := lc.pf.username + "/"

	for _, r := range ims {
		for _, i := range r.ImgNames {

			// display image excluding top of "username/"
			p := strings.Index(i, u)
			if p == -1 { // not display image whose top is not "username/"
				continue
			}
			fmt.Println(i[len(u):])
		}
	}
}
