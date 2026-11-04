package main

import (
	"errors"
	"flag"
	"fmt"
	"github.com/tc-hib/winres"
	"github.com/tc-hib/winres/version"
	"os"
	"quillweave/internal/platform"
	"strconv"
	"strings"
)

// generate writes Windows metadata and distribution notices for temporary build use.
func generate(output, arch, productVersion string) error {
	target, ok := map[string]winres.Arch{"amd64": winres.ArchAMD64, "arm64": winres.ArchARM64}[arch]
	if !ok {
		return errors.New("Windows supports amd64 or arm64")
	}
	var numbers [4]uint16
	parts := strings.Split(productVersion, ".")
	if len(parts) != 3 {
		return errors.New("The version must contain three numeric components")
	}
	for i, part := range parts {
		number, err := strconv.ParseUint(part, 10, 16)
		if err != nil {
			return err
		}
		numbers[i] = uint16(number)
	}
	metadata := version.Info{FileVersion: numbers, ProductVersion: numbers}
	for key, value := range map[string]string{version.ProductName: "Quillweave", version.FileDescription: "Quillweave", version.ProductVersion: productVersion, version.FileVersion: productVersion, version.CompanyName: "Quillweave", version.OriginalFilename: "Quillweave.exe"} {
		if err := metadata.Set(version.LangDefault, key, value); err != nil {
			return err
		}
	}
	resources := winres.ResourceSet{}
	resources.SetVersionInfo(metadata)
	resources.SetManifest(winres.AppManifest{Identity: winres.AssemblyIdentity{Name: platform.ApplicationID, Version: numbers}, ExecutionLevel: winres.AsInvoker, DPIAwareness: winres.DPIPerMonitorV2, UseCommonControlsV6: true, LongPathAware: true})
	for _, name := range []string{"LICENSE", "THIRD_PARTY_NOTICES.txt"} {
		content, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		if err := resources.Set(winres.RT_RCDATA, winres.Name(name), winres.LCIDNeutral, content); err != nil {
			return err
		}
	}
	file, err := os.Create(output)
	if err != nil {
		return err
	}
	writeErr := resources.WriteObject(file, target)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	return closeErr
}

// main generates resources from validated build arguments.
func main() {
	output := flag.String("out", "", "Output resource path")
	arch := flag.String("arch", "amd64", "Target architecture")
	productVersion := flag.String("version", "0.3.0", "Application version")
	flag.Parse()
	if *output == "" {
		fmt.Fprintln(os.Stderr, "Missing resource output path")
		os.Exit(1)
	}
	if err := generate(*output, *arch, *productVersion); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
