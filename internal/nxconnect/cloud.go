package nxconnect

import (
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"sort"
	"strings"
)

type Challenge func(context.Context, string) (string, error)
type cloudToken struct {
	access, refresh string
	manager         *cloudToken
}
type Cloud struct {
	client          *Client
	email, password string
	tokens          []*cloudToken
	primary         *cloudToken
	challenge       Challenge
}
type Site struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Shared  bool   `json:"shared"`
	MFA     bool   `json:"system2faEnabled"`
}

func NewCloud(c *Client, email, password string, challenge Challenge) *Cloud {
	return &Cloud{client: c, email: strings.TrimSpace(email), password: password, challenge: challenge}
}

func (c *Cloud) issue(ctx context.Context, siteID string) (*cloudToken, error) {
	scope := CloudURL + " cloudSystemId=*"
	stage := "Cloud sign-in"
	if siteID != "" {
		if !ValidSiteID(siteID) {
			return nil, errors.New("invalid cloud site ID")
		}
		// A specific site ID must never be combined with a cloud URL scope.
		scope = "cloudSystemId=" + siteID
		stage = "Site sign-in"
	}
	res, err := c.client.request(ctx, "POST", CloudURL+"/cdb/oauth2/token", "", map[string]string{
		"grant_type": "password", "response_type": "token", "client_id": "3rdParty", "username": c.email, "password": c.password, "scope": scope,
	}, "")
	if err != nil {
		return nil, err
	}
	data, err := readLimited(res, jsonLimit)
	if err != nil {
		return nil, err
	}
	defer clear(data)
	var result struct {
		Access  string `json:"access_token"`
		Refresh string `json:"refresh_token"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, errors.New(stage + ": unexpected response from Nx Cloud")
	}
	apiErr := responseError(res.StatusCode, data, stage)
	if !validToken(result.Access) {
		if apiErr.Code != "" || res.StatusCode >= 400 {
			return nil, apiErr
		}
		return nil, errors.New(stage + ": no valid token was issued")
	}
	if result.Refresh != "" && !validToken(result.Refresh) {
		return nil, errors.New(stage + ": invalid refresh token")
	}
	token := &cloudToken{access: result.Access, refresh: result.Refresh}
	if siteID != "" {
		token.manager = c.primary
	}
	c.tokens = append(c.tokens, token)
	if apiErr.MFA() {
		if err := c.confirm(ctx, token); err != nil {
			return nil, err
		}
	} else if res.StatusCode >= 400 || apiErr.Code != "" {
		return nil, apiErr
	}
	return token, nil
}
func (c *Cloud) Login(ctx context.Context) error {
	if c.email == "" || c.password == "" {
		return errors.New("enter your Nx Cloud email and password")
	}
	token, err := c.issue(ctx, "")
	if err != nil {
		return err
	}
	c.primary = token
	return nil
}
func (c *Cloud) confirm(ctx context.Context, token *cloudToken) error {
	if c.challenge == nil {
		return errors.New("this account or site requires an authenticator code")
	}
	message := "Enter the six-digit code from your authenticator."
	for attempt := 0; attempt < 3; attempt++ {
		code, err := c.challenge(ctx, message)
		if err != nil {
			return err
		}
		code = strings.TrimSpace(code)
		valid := len(code) == 6
		for _, r := range code {
			if r < '0' || r > '9' {
				valid = false
			}
		}
		if !valid {
			message = "Enter a six-digit authenticator code."
			continue
		}
		authorizer := token
		if token.manager != nil {
			authorizer = token.manager
		}
		address := CloudURL + "/cdb/account/self/2fa/totp/key/" + code + "?" + url.Values{"token": {token.access}}.Encode()
		err = c.client.json(ctx, "GET", address, authorizer.access, nil, "", "Verify authenticator code", nil)
		if err == nil {
			return nil
		}
		var apiErr *APIError
		if !errors.As(err, &apiErr) || !(apiErr.Status == 400 || apiErr.Status == 401 || apiErr.Status == 403) {
			return err
		}
		message = "That code was not accepted. Enter a current authenticator code."
	}
	return errors.New("authenticator verification failed; sign in again")
}
func (c *Cloud) Sites(ctx context.Context) ([]Site, error) {
	if c.primary == nil {
		return nil, errors.New("sign in to Nx Cloud first")
	}
	sites := map[string]Site{}
	for i, query := range []string{"", "?directlyShared=true", "?directlyShared=false"} {
		var rows []Site
		for attempt := 0; attempt < 2; attempt++ {
			err := c.client.json(ctx, "GET", CloudURL+"/cdb/systems"+query, c.primary.access, nil, "", "List cloud sites", &rows)
			var apiErr *APIError
			if errors.As(err, &apiErr) && apiErr.MFA() && attempt == 0 {
				if err = c.confirm(ctx, c.primary); err != nil {
					return nil, err
				}
				continue
			}
			if err != nil {
				return nil, err
			}
			break
		}
		for _, site := range rows {
			if !ValidSiteID(site.ID) {
				return nil, errors.New("Nx returned an invalid site ID; the site list is incomplete")
			}
			if _, exists := sites[site.ID]; exists {
				continue
			}
			if site.Name == "" {
				site.Name = site.ID
			}
			site.Shared = i > 0
			sites[site.ID] = site
		}
	}
	list := make([]Site, 0, len(sites))
	for _, site := range sites {
		list = append(list, site)
	}
	sort.Slice(list, func(i, j int) bool { return strings.ToLower(list[i].Name) < strings.ToLower(list[j].Name) })
	return list, nil
}
func (c *Cloud) Download(ctx context.Context, siteID, address string, local *Client, progress Progress) ([]byte, error) {
	if c.primary == nil {
		if err := c.Login(ctx); err != nil {
			return nil, err
		}
	}
	client, relayID := local, ""
	if address == "" {
		client = c.client
		relayID = siteID
		address = relayURL(siteID)
	}
	if client == nil {
		return nil, errors.New("no verified server connection")
	}
	token, err := c.issue(ctx, siteID)
	if err != nil {
		return nil, err
	}
	for attempt := 0; attempt < 2; attempt++ {
		data, err := client.Download(ctx, address, token.access, relayID, progress)
		var apiErr *APIError
		if errors.As(err, &apiErr) && attempt == 0 {
			if apiErr.MFA() {
				if err = c.confirm(ctx, token); err != nil {
					return nil, err
				}
				continue
			}
			if apiErr.Fresh() {
				token, err = c.issue(ctx, siteID)
				if err != nil {
					return nil, err
				}
				continue
			}
		}
		return data, err
	}
	return nil, errors.New("site authentication could not be completed")
}

// Call with a short, independent context even after cancellation. Children
// require their cloud manager for revocation; refresh tokens are revoked first.
func (c *Cloud) Close(ctx context.Context) error {
	var failed bool
	for i := len(c.tokens) - 1; i >= 0; i-- {
		t := c.tokens[i]
		authorizer := t
		if t.manager != nil {
			authorizer = t.manager
		}
		for _, secret := range []string{t.refresh, t.access} {
			if secret == "" {
				continue
			}
			err := c.client.json(ctx, "DELETE", CloudURL+"/cdb/oauth2/token/"+url.PathEscape(secret), authorizer.access, nil, "", "Cloud sign-out", nil)
			var apiErr *APIError
			if err != nil && !(errors.As(err, &apiErr) && apiErr.Status == 404) {
				failed = true
			}
		}
		t.refresh, t.access = "", ""
	}
	c.password, c.email = "", ""
	c.tokens = nil
	c.primary = nil
	if failed {
		return errors.New("sign-out could not be confirmed; tokens were discarded locally")
	}
	return nil
}
