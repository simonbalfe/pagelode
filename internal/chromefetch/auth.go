package chromefetch

import (
	"context"
	"github.com/chromedp/cdproto/fetch"
	"github.com/chromedp/chromedp"
)

func (e *Engine) proxyActions(tab context.Context) (chromedp.Tasks, <-chan error) {
	var authErrors chan error
	var actions chromedp.Tasks
	if e.proxy != nil && e.proxy.User != nil {
		username := e.proxy.User.Username()
		password, _ := e.proxy.User.Password()
		authErrors = make(chan error, 1)
		reportAuthError := func(err error) {
			if err != nil {
				select {
				case authErrors <- err:
				default:
				}
			}
		}
		chromedp.ListenTarget(tab, func(event any) {
			switch event := event.(type) {
			case *fetch.EventRequestPaused:
				go func() { reportAuthError(chromedp.Run(tab, fetch.ContinueRequest(event.RequestID))) }()
			case *fetch.EventAuthRequired:
				response := &fetch.AuthChallengeResponse{Response: fetch.AuthChallengeResponseResponseCancelAuth}
				if event.AuthChallenge != nil && event.AuthChallenge.Source == fetch.AuthChallengeSourceProxy {
					response = &fetch.AuthChallengeResponse{Response: fetch.AuthChallengeResponseResponseProvideCredentials, Username: username, Password: password}
				}
				go func() { reportAuthError(chromedp.Run(tab, fetch.ContinueWithAuth(event.RequestID, response))) }()
			}
		})
		actions = append(actions, fetch.Enable().WithHandleAuthRequests(true))
	}
	return actions, authErrors
}
