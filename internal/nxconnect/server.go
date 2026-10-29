package nxconnect

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

type Server struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	SystemName    string `json:"systemName"`
	Version       string `json:"version"`
	CloudSystemID string `json:"cloudSystemId"`
	CloudHost     string `json:"cloudHost"`
	Address       string `json:"address"`
	Port          int    `json:"port"`
	Type          string `json:"type"`
	Application   string `json:"application"`
}

func (c *Client) Probe(ctx context.Context, address string) (Server, error) {
	var raw json.RawMessage
	if err := c.json(ctx, "GET", address+"/api/moduleInformation", "", nil, "", "Identify server", &raw); err != nil {
		return Server{}, err
	}
	var wrapped struct {
		Reply json.RawMessage `json:"reply"`
	}
	json.Unmarshal(raw, &wrapped)
	if len(wrapped.Reply) > 0 {
		raw = wrapped.Reply
	}
	var server Server
	if len(raw) > 0 && raw[0] == '[' {
		var rows []Server
		if json.Unmarshal(raw, &rows) != nil || len(rows) != 1 {
			return server, errors.New("the address did not identify a single Nx server")
		}
		server = rows[0]
	} else if json.Unmarshal(raw, &server) != nil {
		return server, errors.New("the address did not return Nx server information")
	}
	if server.Type != "Mediaserver" && server.Type != "Media Server" && server.Application != "Mediaserver" && server.Application != "Media Server" {
		return Server{}, errors.New("this address is not an Nx Media Server")
	}
	if server.Version == "" {
		return Server{}, errors.New("the server did not report its Nx version")
	}
	server.CloudSystemID = strings.Trim(strings.ToLower(server.CloudSystemID), "{}")
	if server.CloudSystemID != "" && !ValidSiteID(server.CloudSystemID) {
		return Server{}, errors.New("the server reported an invalid cloud site ID")
	}
	server.Address = address
	return server, nil
}

type LocalSession struct {
	Token string
	Path  string
}

func (c *Client) LoginLocal(ctx context.Context, address, username, password string) (LocalSession, error) {
	if strings.TrimSpace(username) == "" || password == "" {
		return LocalSession{}, errors.New("enter a username and password")
	}
	for _, v := range []string{"v4", "v3", "v2", "v1"} {
		path := "/rest/" + v + "/login/sessions"
		var result struct {
			Token string `json:"token"`
		}
		err := c.json(ctx, "POST", address+path, "", map[string]any{"username": strings.TrimSpace(username), "password": password, "setCookie": false}, "", "Server sign-in", &result)
		var apiErr *APIError
		if errors.As(err, &apiErr) && (apiErr.Status == 404 || apiErr.Status == 405 || apiErr.Status == 501) {
			continue
		}
		if err != nil {
			return LocalSession{}, err
		}
		if !validToken(result.Token) {
			return LocalSession{}, errors.New("the server did not issue a valid login token")
		}
		return LocalSession{result.Token, path}, nil
	}
	return LocalSession{}, errors.New("this server does not support bearer-token sign-in")
}
func (c *Client) LogoutLocal(ctx context.Context, address string, s LocalSession) error {
	return c.json(ctx, "DELETE", address+s.Path+"/"+s.Token, s.Token, nil, "", "Sign out", nil)
}
func validToken(token string) bool {
	if token == "" || len(token) > 32768 {
		return false
	}
	for _, r := range token {
		if r < 33 || r > 126 || strings.ContainsRune("/?#%", r) {
			return false
		}
	}
	return true
}
func (s Server) CloudSupported() error {
	if !ValidSiteID(s.CloudSystemID) {
		return errors.New("this server is not linked to an Nx Cloud site; use a local account")
	}
	if s.CloudHost != "nxvms.com" && s.CloudHost != "https://nxvms.com" {
		return fmt.Errorf("this server uses a different cloud provider; this version supports nxvms.com accounts")
	}
	return nil
}
