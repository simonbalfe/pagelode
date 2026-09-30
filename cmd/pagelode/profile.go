package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/simonbalfe/pagelode/internal/config"
	"github.com/simonbalfe/pagelode/internal/discovery"
	"github.com/simonbalfe/pagelode/internal/profile"
)

func profileCommand(configuration config.Config, args []string, stdout, stderr io.Writer) error {
	if len(args) != 3 || args[0] != "login" {
		return errors.New("usage: pagelode profile login <name> <URL>")
	}
	request, err := (discovery.Request{URL: args[2], Profile: args[1]}).Validate()
	if err != nil {
		return err
	}
	directory, err := profile.Directory(configuration.ProfilesDirectory, request.Profile, true)
	if err != nil {
		return err
	}
	command := exec.Command(configuration.PatchrightCommand, configuration.PatchrightWorker, "--login", request.URL)
	command.Env = append(os.Environ(), "PAGELODE_PATCHRIGHT_PROFILE="+directory, "PAGELODE_PATCHRIGHT_HEADLESS=false", "PAGELODE_PROFILE_SESSION=true")
	command.Stdout = stdout
	command.Stderr = stderr
	command.Stdin = os.Stdin
	fmt.Fprintln(stdout, "Sign in in the browser, then press Enter here to save the profile and close the browser.")
	if err := command.Run(); err != nil {
		return fmt.Errorf("profile login: %w", err)
	}
	fmt.Fprintf(stdout, "Profile %s saved. Use pagelode discover --profile %s %s\n", request.Profile, request.Profile, request.URL)
	return nil
}
