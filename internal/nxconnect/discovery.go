package nxconnect

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Nx 6.1 publishes module information on 239.255.11.11:5008 every ten
// seconds. The legacy 5007 probe elicits a unicast reply on the sending socket.
// Source: nx_open/vms_6.1.2/.../discovery/{udp,deprecated}_multicast_finder.cpp.
// Discovery advertisements are hints only: HTTPS identity/certificate checks
// happen again before sending any credentials or bearer tokens.
func Discover(ctx context.Context) ([]Server, error) {
	ctx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil, errors.New("could not enumerate network interfaces; enter a server address manually")
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	servers := map[string]Server{}
	listeners := 0
	listen := func(conn *net.UDPConn) {
		listeners++
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer conn.Close()
			stop := context.AfterFunc(ctx, func() { conn.Close() })
			defer stop()
			buf := make([]byte, 65536)
			for {
				n, addr, err := conn.ReadFromUDP(buf)
				if err != nil {
					return
				}
				server, ok := discoveryServer(buf[:n], addr.IP)
				if !ok {
					continue
				}
				mu.Lock()
				if len(servers) < 256 {
					servers[server.Address] = server
				}
				mu.Unlock()
			}
		}()
	}
	group := net.IPv4(239, 255, 11, 11)
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagMulticast == 0 {
			continue
		}
		if conn, err := net.ListenMulticastUDP("udp4", &iface, &net.UDPAddr{IP: group, Port: 5008}); err == nil {
			listen(conn)
		}
		addresses, _ := iface.Addrs()
		for _, addr := range addresses {
			ip, _, err := net.ParseCIDR(addr.String())
			if err != nil || ip.To4() == nil {
				continue
			}
			conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: ip})
			if err != nil {
				continue
			}
			request := []byte(`{ magic: "7B938F06-ACF1-45f0-8303-98AA8057739A" }`)
			if _, err = conn.WriteToUDP(request, &net.UDPAddr{IP: group, Port: 5007}); err != nil {
				conn.Close()
				continue
			}
			listen(conn)
		}
	}
	if listeners == 0 {
		return nil, errors.New("network discovery is unavailable; enter the server address manually")
	}
	wg.Wait()
	list := make([]Server, 0, len(servers))
	for _, s := range servers {
		list = append(list, s)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].SystemName == list[j].SystemName {
			return list[i].Address < list[j].Address
		}
		return list[i].SystemName < list[j].SystemName
	})
	return list, nil
}
func discoveryServer(data []byte, ip net.IP) (Server, bool) {
	var s Server
	if len(data) > 16384 || json.Unmarshal(data, &s) != nil || s.Version == "" || s.Port < 1 || s.Port > 65535 || ip.IsUnspecified() || ip.IsMulticast() {
		return s, false
	}
	if s.Type != "Mediaserver" && s.Type != "Media Server" && s.Application != "Mediaserver" && s.Application != "Media Server" {
		return s, false
	}
	s.Address = "https://" + net.JoinHostPort(ip.String(), strconv.Itoa(s.Port))
	return s, true
}
