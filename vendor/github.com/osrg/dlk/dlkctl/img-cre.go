package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/osrg/dlk/dlkctl/utils"
	"github.com/spf13/cobra"
)

type imgCreCfg struct {
	script *os.File
	params utils.Params
	pf     *PersistentFlags
}

// build image names
type buildImage struct {
	image    string // non-gpu
	imageGpu string // gpu
}

//NewCommandImageCreate generate create cmd
func NewCommandImageCreate() *cobra.Command {
	cmd := &cobra.Command{
		Use: "create <workload.py>",
		//if workload file is not specified, command error
		Args:  cobra.ExactArgs(1),
		Short: "create Docker image",
		Long:  `create Docker image`,
		Run:   imgCreateMain,
	}

	//set local flag
	utils.AddImageFlag(cmd)
	utils.AddBaseImageFlag(cmd)
	utils.AddGpuImageFlag(cmd)

	//add subcommand
	return cmd
}

//Main Proceduer of create command
func imgCreateMain(cmd *cobra.Command, args []string) {

	//parameter check, init parameters
	fmt.Println("*** CHECK PARAMS ***")
	ic := imgCreCfg{}
	err := ic.checkParams(cmd, args)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}

	cli := utils.NewDockerClient(ic.pf.docker, ic.pf.registry)

	exist := false
	if ic.params.Image != "" {
		//search the docker image on private registry
		exist, err = cli.IsImageExistOnRegistry(ic.params.Image, ic.pf.username)
		if err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		} else if exist {
			fmt.Println("Specified image has already existed on private registry")
			return
		}
	}

	ic.displayParams()

	// image names which are used for pushing images
	// to private registry
	var imgNames buildImage

	fmt.Println("*** CREATE Docker Image ***")
	imgNames, err = ic.createDockerImage(cli)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
	fmt.Printf("%-30s : %s\n", "REPOSITORY NAME for NON-GPU", imgNames.image)
	if ic.params.GpuImg {
		fmt.Printf("%-30s : %s\n", "REPOSITORY NAME for GPU", imgNames.imageGpu)
	}
	fmt.Println("Completed")

	//push images from local to private registry
	fmt.Println("*** Push Docker Image To Registry ***")
	// push non-gpu image
	err = cli.PushImage(imgNames.image)
	if err != nil {
		fmt.Println(err.Error())
		os.Exit(1)
	}
	// push gpu image
	if ic.params.GpuImg {
		err = cli.PushImage(imgNames.imageGpu)
		if err != nil {
			fmt.Println(err.Error())
			os.Exit(1)
		}
	}
	fmt.Println("Completed")
}

//checkParams check args and flag vailidity and return imgCreCfg struct
func (ic *imgCreCfg) checkParams(cmd *cobra.Command, args []string) error {
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

	var script *os.File

	// get time in order to use in name auto-generation process
	now := time.Now()

	// Open workload file
	script, err = os.Open(args[0])
	if err != nil {
		return err
	}

	// check image name
	// if image name is not specified, automatically generate it
	// <scriptname>-yy-mm-dd-hh-MM-ss
	if params.Image == "" {
		var s string

		sname := filepath.Base(script.Name())
		i := strings.LastIndex(sname, ".")
		if i != -1 {
			s = sname[0:i]
		} else {
			s = sname
		}
		params.Image = fmt.Sprintf("%s-%d-%d-%d-%d-%d-%d",
			s, now.Year(), now.Month(), now.Day(), now.Hour(), now.Minute(), now.Second())
	}

	ic.script = script
	ic.params = params
	ic.pf = pf

	return err
}

// createDockerImage build Docker images using docker REST API
// return: image names(buildImage)
func (ic *imgCreCfg) createDockerImage(cli *utils.DockerClient) (imgnm buildImage, err error) {

	// push image names for non-gpu and gpu
	var imageNames buildImage
	imageNames.image = fmt.Sprintf("%s/%s/%s", cli.RegistryInterface, ic.pf.username, ic.params.Image)
	if ic.params.GpuImg {
		imageNames.imageGpu = imageNames.image + ":latest-gpu"
	}

	// TO build using Docker API, api requires context file whitch is tar file containing dockerfile
	// generate dockerfile and compress it. this function return *File of generated tar

	// build non-gpu image
	gpuf := false
	cctx, err := ic.generateDockerContext(gpuf)
	if err != nil {
		return buildImage{}, err
	}

	ccfile, err := os.Open(cctx)
	if err != nil {
		return buildImage{}, err
	}

	err = cli.BuildNewImage(imageNames.image, ccfile)
	if err != nil {
		return buildImage{}, err
	}

	//build gpu image
	if ic.params.GpuImg {
		gpuf = true

		// file descriptor is moved back to top of
		// workload file
		ic.script.Seek(0, os.SEEK_SET)
		gctx, err := ic.generateDockerContext(gpuf)
		if err != nil {
			return buildImage{}, err
		}

		gcfile, err := os.Open(gctx)
		if err != nil {
			return buildImage{}, err
		}

		err = cli.BuildNewImage(imageNames.imageGpu, gcfile)
		if err != nil {
			return buildImage{}, err
		}
	}

	return imageNames, err
}

// generateDockerContext creates dockerfile and compresses files required by build REST api
func (ic *imgCreCfg) generateDockerContext(gpuf bool) (string, error) {

	// set temporary output dir, and generate it
	dir := tempDir + "/" + ic.params.Image
	if gpuf {
		dir += "-g"
	}
	err := os.MkdirAll(dir, 0755)
	if err != nil {
		return "", err
	}

	// create dockerfile
	// set base image
	// high priority: --baseImage flag
	var base string
	if ic.params.BaseImage != "" {
		base = ic.params.BaseImage

	} else if ic.params.GpuImg { // GpuImg is true,
		if gpuf { // for gpu, use tensorflow-with-gpu image
			base = dockerImageGpu
		} else { // for non-gpu, use tensorflow-non-gpu image
			base = dockerImage
		}
	} else { // use tensorflow-non-gpu image
		base = dockerImage
	}
	//create dockerfile
	df := []string{}
	df = append(df, "FROM "+base)
	df = append(df, "MAINTAINER dlkctl")
	df = append(df, "RUN mkdir /script")
	df = append(df, "ADD "+filepath.Base(ic.script.Name())+" /script/")
	df = append(df, "WORKDIR /script")

	file := dir + "/" + "Dockerfile"
	ofile, err := os.Create(file)
	if err != nil {
		return "", err
	}

	w := bufio.NewWriter(ofile)
	for _, str := range df {
		fmt.Fprintln(w, str)
	}
	w.Flush()

	//copy workload script to context dir
	dst, err := os.Create(dir + "/" + filepath.Base(ic.script.Name()))
	if err != nil {
		return "", err
	}
	_, err = io.Copy(dst, ic.script)
	defer dst.Close()
	//compress it
	path, err := makeTar(dir)

	return path, err
}

func (ic *imgCreCfg) displayParams() {
	fmt.Println("** exec parameters ************")
	if ic.script != nil {
		fmt.Printf("| %-30s : %s\n", "workload script", ic.script.Name())
	}
	fmt.Printf("| %-30s : %s\n", "docker daemon API endpoint", ic.pf.docker)
	fmt.Printf("| %-30s : %s\n", "docker registry endpoint", ic.pf.registry)
	if ic.params.Image != "" {
		fmt.Printf("| %-30s : %s\n", "docker image", ic.params.Image)
	}
	if ic.params.BaseImage != "" {
		fmt.Printf("| %-30s : %s\n", "docker base image", ic.params.BaseImage)
	}

	fmt.Printf("| %-30s : %t\n", "gpu image", ic.params.GpuImg)
}
