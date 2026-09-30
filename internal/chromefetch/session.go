package chromefetch

import (
	"context"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
	"github.com/simonbalfe/pagelode/internal/page"
)

func browserSession(ctx context.Context) (page.Session, error) {
	var session page.Session
	err := chromedp.Run(ctx,
		chromedp.Evaluate("navigator.userAgent", &session.UserAgent),
		chromedp.ActionFunc(func(ctx context.Context) error {
			cookies, err := network.GetCookies().Do(ctx)
			if err != nil {
				return err
			}
			for i, cookie := range cookies {
				if i >= 500 {
					break
				}
				session.Cookies = append(session.Cookies, page.Cookie{Name: cookie.Name, Value: cookie.Value, Domain: cookie.Domain, Path: cookie.Path, Expires: cookie.Expires, Secure: cookie.Secure, HTTPOnly: cookie.HTTPOnly, SameSite: string(cookie.SameSite)})
			}
			return nil
		}),
	)
	return session, err
}
