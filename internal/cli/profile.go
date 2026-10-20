package cli

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"github.com/simonbalfe/pagelode/pkg/browser"
	"github.com/simonbalfe/pagelode/pkg/discovery"
	"io"
	"os"
	"time"
)

const profileRequestTimeout = 2 * time.Minute

func profileCommand(configuration Config, args []string, stdout, stderr io.Writer) error {
	if len(args) != 3 || args[0] != "login" {
		return errors.New("usage: pagelode profile login <name> <URL>")
	}
	request, err := (discovery.Request{URL: args[2], Profile: args[1]}).Validate()
	if err != nil {
		return err
	}
	identities, err := browser.NewIdentities(configuration.ProxyURL, configuration.BrowserIdentities, nil)
	if err != nil {
		return fmt.Errorf("configure browser identities: %w", err)
	}
	client, err := browser.New(configuration.BrowserURLs, configuration.BrowserToken, configuration.BrowserSettle, configuration.BrowserNodeSlots, identities)
	if err != nil {
		return fmt.Errorf("configure browser service: %w", err)
	}
	headless := false
	createContext, cancelCreate := context.WithTimeout(context.Background(), profileRequestTimeout)
	session, err := client.CreateSession(createContext, browser.SessionRequest{Profile: request.Profile, CreateProfile: true, Headless: &headless, StartURL: request.URL, TTL: time.Hour})
	cancelCreate()
	if err != nil {
		return fmt.Errorf("profile login: %w", err)
	}
	fmt.Fprintf(stdout, "Browser session %s is open. CDP: %s\n", session.ID, session.CDPURL)
	fmt.Fprintln(stdout, "Sign in in the browser window on the browser service host, then press Enter here to save the profile and close the browser.")
	_, readErr := bufio.NewReader(os.Stdin).ReadString('\n')
	closeContext, cancelClose := context.WithTimeout(context.Background(), profileRequestTimeout)
	defer cancelClose()
	if err := client.CloseSession(closeContext, session.ID); err != nil {
		return fmt.Errorf("profile login: save profile: %w", err)
	}
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return fmt.Errorf("profile login: read confirmation: %w", readErr)
	}
	fmt.Fprintf(stdout, "Profile %s saved. Use pagelode map endpoints --profile %s %s\n", request.Profile, request.Profile, request.URL)
	return nil
}
