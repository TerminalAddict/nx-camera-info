package main

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"nx-camera-info/internal/nxconnect"
)

type ConnectionRequest struct {
	Action   string `json:"action"`
	Address  string `json:"address"`
	Auth     string `json:"auth"`
	Username string `json:"username"`
	Password string `json:"password"`
	SiteID   string `json:"siteId"`
}
type ConnectionState struct {
	PromptID    int                    `json:"promptId"`
	Busy        bool                   `json:"busy"`
	Stage       string                 `json:"stage"`
	Message     string                 `json:"message"`
	Error       string                 `json:"error"`
	Warning     string                 `json:"warning"`
	Received    int64                  `json:"received"`
	Total       int64                  `json:"total"`
	Certificate *nxconnect.Certificate `json:"certificate,omitempty"`
	Servers     []nxconnect.Server     `json:"servers"`
	Sites       []nxconnect.Site       `json:"sites"`
}

type connectionManager struct {
	mu        sync.Mutex
	state     ConnectionState
	client    *nxconnect.Client
	session   *cameraSession
	cancel    context.CancelFunc
	answer    chan string
	done      chan struct{}
	cloud     *nxconnect.Cloud
	idleTimer *time.Timer
	discover  func(context.Context) ([]nxconnect.Server, error)
}

func newConnectionManager(session *cameraSession) *connectionManager {
	return &connectionManager{client: nxconnect.New(), session: session, discover: nxconnect.Discover, state: ConnectionState{Stage: "idle"}}
}
func (m *connectionManager) State() ConnectionState {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.state
	s.Servers = append([]nxconnect.Server{}, s.Servers...)
	s.Sites = append([]nxconnect.Site{}, s.Sites...)
	return s
}
func (m *connectionManager) progress(stage string, received, total int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state.Stage, m.state.Received, m.state.Total = stage, received, total
	labels := map[string]string{"discovering": "Searching nearby networks for Nx servers…", "probing": "Checking server identity…", "authenticating": "Signing in…", "sites": "Choose a cloud site to open.", "downloading": "Downloading a site database backup into memory…", "validating": "Checking and opening the database…", "signing-out": "Signing out…"}
	m.state.Message = labels[stage]
}
func (m *connectionManager) ask(ctx context.Context, stage, message string, cert *nxconnect.Certificate) (string, error) {
	m.mu.Lock()
	ch := make(chan string, 1)
	m.answer = ch
	m.state.Stage, m.state.Message, m.state.Certificate = stage, message, cert
	m.state.PromptID++
	m.mu.Unlock()
	defer func() { m.mu.Lock(); m.answer = nil; m.state.Certificate = nil; m.mu.Unlock() }()
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case answer := <-ch:
		return answer, nil
	}
}
func (m *connectionManager) Answer(answer string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.answer == nil {
		return errors.New("there is no connection prompt to answer")
	}
	select {
	case m.answer <- answer:
		return nil
	default:
		return errors.New("the previous answer is being processed")
	}
}
func closeCloud(cloud *nxconnect.Cloud) string {
	if cloud == nil {
		return ""
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := cloud.Close(ctx); err != nil {
		return err.Error()
	}
	return ""
}
func (m *connectionManager) Cancel() {
	m.mu.Lock()
	if m.idleTimer != nil {
		m.idleTimer.Stop()
		m.idleTimer = nil
	}
	if m.cancel != nil {
		m.cancel()
	}
	cloud := m.cloud
	m.cloud = nil
	if !m.state.Busy {
		m.state = ConnectionState{Stage: "idle"}
	}
	m.mu.Unlock()
	if cloud != nil {
		go closeCloud(cloud)
	}
}
func (m *connectionManager) Shutdown() {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	if m.idleTimer != nil {
		m.idleTimer.Stop()
	}
	cloud, done := m.cloud, m.done
	m.cloud = nil
	m.mu.Unlock()
	closeCloud(cloud)
	if done != nil {
		select {
		case <-done:
		case <-time.After(9 * time.Second):
		}
	}
	m.client.Close()
}
func (m *connectionManager) Start(req ConnectionRequest, finish func()) error {
	if req.Action != "discover" && req.Action != "cloud" && req.Action != "open" {
		return errors.New("unknown connection action")
	}
	m.mu.Lock()
	if m.state.Busy {
		m.mu.Unlock()
		return errors.New("cancel or finish the current connection first")
	}
	if m.idleTimer != nil {
		m.idleTimer.Stop()
		m.idleTimer = nil
	}
	cloud := m.cloud
	m.cloud = nil
	sites := append([]nxconnect.Site{}, m.state.Sites...)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	m.cancel = cancel
	m.done = make(chan struct{})
	done := m.done
	m.state = ConnectionState{Busy: true, Stage: "starting", Message: "Starting connection…", Sites: sites}
	m.mu.Unlock()
	go func() {
		defer finish()
		defer close(done)
		defer cancel()
		keepCloud := false
		var resultErr error
		defer func() {
			req.Password = ""
			warning := ""
			if !keepCloud || ctx.Err() != nil {
				warning = closeCloud(cloud)
				cloud = nil
			}
			m.mu.Lock()
			cancelled := ctx.Err() != nil
			m.cancel = nil
			m.state.Busy = false
			if warning != "" {
				m.state.Warning = warning
			}
			if cancelled {
				m.state.Stage = "cancelled"
				m.state.Message = "Connection cancelled or timed out."
				m.state.Sites = nil
			} else if resultErr != nil {
				m.state.Stage = "error"
				m.state.Error = resultErr.Error()
				m.state.Message = ""
				m.state.Sites = nil
			} else if keepCloud {
				m.cloud = cloud
				m.idleTimer = time.AfterFunc(5*time.Minute, func() {
					m.mu.Lock()
					if m.cloud != cloud {
						m.mu.Unlock()
						return
					}
					m.cloud = nil
					m.state.Sites = nil
					m.state.Stage = "error"
					m.state.Error = "Cloud sign-in expired while browsing; sign in again."
					m.mu.Unlock()
					closeCloud(cloud)
				})
			}
			m.mu.Unlock()
			if cancelled && cloud != nil {
				closeCloud(cloud)
			}
		}()
		if req.Action != "open" || req.Address != "" {
			closeCloud(cloud)
			cloud = nil
		}
		if req.Action == "discover" {
			m.progress("discovering", 0, 0)
			servers, err := m.discover(ctx)
			resultErr = err
			m.mu.Lock()
			m.state.Servers = servers
			m.state.Stage = "discovered"
			m.state.Message = fmt.Sprintf("Found %d servers. You can also enter an address manually.", len(servers))
			m.mu.Unlock()
			return
		}
		challenge := func(ctx context.Context, message string) (string, error) { return m.ask(ctx, "mfa", message, nil) }
		if req.Action == "cloud" {
			m.progress("authenticating", 0, 0)
			cloud = nxconnect.NewCloud(m.client, req.Username, req.Password, challenge)
			if resultErr = cloud.Login(ctx); resultErr != nil {
				return
			}
			list, err := cloud.Sites(ctx)
			resultErr = err
			if err != nil {
				return
			}
			m.mu.Lock()
			m.state.Sites = list
			m.state.Stage = "sites"
			m.state.Message = fmt.Sprintf("%d sites available. Select a site to request a backup.", len(list))
			m.mu.Unlock()
			keepCloud = true
			return
		}
		var data []byte
		var name string
		source := DatabaseSource{Kind: "server", Auth: req.Auth, Address: req.Address, SiteID: req.SiteID}
		if req.Address == "" {
			if cloud == nil {
				resultErr = errors.New("sign in to Nx Cloud first")
				return
			}
			for _, site := range sites {
				if site.ID == req.SiteID {
					name = site.Name
					break
				}
			}
			if name == "" {
				resultErr = errors.New("select a site from the signed-in account")
				return
			}
			source.Auth, source.Route = "cloud", "Cloud relay"
			m.progress("authenticating", 0, 0)
			data, resultErr = cloud.Download(ctx, req.SiteID, "", nil, m.progress)
		} else {
			address, err := nxconnect.NormalizeAddress(req.Address)
			if err != nil {
				resultErr = err
				return
			}
			source.Address = address
			local := m.client.Local(address, "")
			defer func() { local.Close() }()
			m.progress("probing", 0, 0)
			server, err := local.Probe(ctx, address)
			var cert *nxconnect.CertificateError
			if errors.As(err, &cert) {
				answer, askErr := m.ask(ctx, "certificate", "Verify this server certificate before sending credentials.", &cert.Certificate)
				if askErr != nil {
					resultErr = askErr
					return
				}
				if answer != "trust" {
					resultErr = errors.New("server certificate was not trusted")
					return
				}
				local.Close()
				local = m.client.Local(address, cert.Certificate.Fingerprint)
				m.progress("probing", 0, 0)
				server, err = local.Probe(ctx, address)
			}
			if err != nil {
				resultErr = err
				return
			}
			if req.SiteID != "" && req.SiteID != server.CloudSystemID {
				resultErr = errors.New("the server now belongs to a different cloud site; select it again")
				return
			}
			name = server.SystemName
			if name == "" {
				name = server.Name
			}
			if name == "" {
				name = "Nx site"
			}
			source.SiteID, source.Route = server.CloudSystemID, "Direct server"
			m.progress("authenticating", 0, 0)
			switch req.Auth {
			case "cloud":
				if resultErr = server.CloudSupported(); resultErr != nil {
					return
				}
				cloud = nxconnect.NewCloud(m.client, req.Username, req.Password, challenge)
				data, resultErr = cloud.Download(ctx, server.CloudSystemID, address, local, m.progress)
				// Offer a relay retry on a transport failure only. Permission and
				// format errors never trigger alternative authentication attempts.
				var networkErr *nxconnect.NetworkError
				if errors.As(resultErr, &networkErr) && ctx.Err() == nil {
					answer, err := m.ask(ctx, "relay", "The direct connection failed. Retry this site through Nx Cloud?", nil)
					if err != nil {
						resultErr = err
						return
					}
					if answer == "relay" {
						source.Route = "Cloud relay"
						data, resultErr = cloud.Download(ctx, server.CloudSystemID, "", nil, m.progress)
					}
				}
			case "local":
				login, err := local.LoginLocal(ctx, address, req.Username, req.Password)
				if err != nil {
					resultErr = err
					return
				}
				data, resultErr = local.Download(ctx, address, login.Token, "", m.progress)
				logoutCtx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
				if err := local.LogoutLocal(logoutCtx, address, login); err != nil {
					m.mu.Lock()
					m.state.Warning = "Server sign-out could not be confirmed; the token was discarded locally."
					m.mu.Unlock()
				}
				cancel()
			default:
				resultErr = errors.New("choose a local or Nx Cloud account")
			}
		}
		if resultErr != nil {
			clear(data)
			return
		}
		m.progress("validating", int64(len(data)), int64(len(data)))
		var report Report
		report, resultErr = readMemoryReport(ctx, data)
		if resultErr != nil {
			clear(data)
			return
		}
		m.progress("signing-out", 0, 0)
		warning := closeCloud(cloud)
		cloud = nil
		m.mu.Lock()
		resultErr = m.session.installBackup(ctx, data, name+".db", source, report)
		if resultErr != nil {
			clear(data)
			m.mu.Unlock()
			return
		}
		m.state.Stage = "ready"
		m.state.Message = "Database opened from a temporary backup."
		if warning != "" {
			m.state.Warning = warning
		}
		m.state.Sites = nil
		m.mu.Unlock()
	}()
	return nil
}
