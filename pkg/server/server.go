package server

import (
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/network-tunnel/net-tunnel/pkg/config"
	"github.com/network-tunnel/net-tunnel/pkg/protocol"
)

type peer struct {
	conn         *websocket.Conn
	clientID     string
	clientName   string
	tunnelIP     string
	allowPossess bool
	localSubnet  string
	mu           sync.Mutex
}

type Server struct {
	cfg       *config.ServerConfig
	peers     map[string]*peer
	peersByIP map[string]*peer
	mu        sync.RWMutex
	ipPool    *ipAllocator
	upgrader  websocket.Upgrader
}

type ipAllocator struct {
	network *net.IPNet
	used    map[string]bool
	mu      sync.Mutex
}

func newIPAllocator(cidr string) (*ipAllocator, error) {
	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("parse cidr: %w", err)
	}
	return &ipAllocator{
		network: network,
		used:    make(map[string]bool),
	}, nil
}

func (a *ipAllocator) allocate() (net.IP, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	broadcast := broadcastIP(a.network)
	ip := a.network.IP.Mask(a.network.Mask)
	for ip = a.nextIP(ip); ip != nil; ip = a.nextIP(ip) {
		if !a.used[ip.String()] && !ip.Equal(a.network.IP) && !ip.Equal(broadcast) {
			a.used[ip.String()] = true
			return ip, nil
		}
	}
	return nil, fmt.Errorf("no available IP in pool")
}

func broadcastIP(n *net.IPNet) net.IP {
	broadcast := make(net.IP, len(n.IP))
	for i := range n.IP {
		broadcast[i] = n.IP[i] | ^n.Mask[i]
	}
	return broadcast
}

func (a *ipAllocator) release(ip net.IP) {
	a.mu.Lock()
	defer a.mu.Unlock()
	delete(a.used, ip.String())
}

func (a *ipAllocator) nextIP(ip net.IP) net.IP {
	next := make(net.IP, len(ip))
	copy(next, ip)
	for j := len(next) - 1; j >= 0; j-- {
		next[j]++
		if next[j] != 0 {
			break
		}
	}
	if !a.network.Contains(next) {
		return nil
	}
	return next
}

func NewServer(cfg *config.ServerConfig) (*Server, error) {
	tunnelNet := cfg.TunnelNet
	if tunnelNet == "" {
		tunnelNet = "10.0.0.0/24"
	}
	ipPool, err := newIPAllocator(tunnelNet)
	if err != nil {
		return nil, err
	}

	return &Server{
		cfg:       cfg,
		peers:     make(map[string]*peer),
		peersByIP: make(map[string]*peer),
		ipPool:    ipPool,
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool { return true },
		},
	}, nil
}

func (s *Server) Start() error {
	http.HandleFunc("/ws", s.handleWebSocket)
	http.HandleFunc("/health", s.handleHealth)

	go s.startTCPRelay()

	addr := s.cfg.Listen
	log.Printf("Relay server starting on %s", addr)
	return http.ListenAndServe(addr, nil)
}

func (s *Server) startTCPRelay() {
	host, _, err := net.SplitHostPort(s.cfg.Listen)
	if err != nil {
		host = "0.0.0.0"
	}
	relayAddr := fmt.Sprintf("%s:8081", host)

	listener, err := net.Listen("tcp", relayAddr)
	if err != nil {
		log.Printf("TCP relay listen error: %v", err)
		return
	}
	defer listener.Close()

	log.Printf("TCP relay listening on %s", relayAddr)

	pending := make(map[string]chan net.Conn)
	var mu sync.Mutex

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("TCP relay accept error: %v", err)
			continue
		}

		go func(conn net.Conn) {
			line, err := readLine(conn)
			if err != nil {
				log.Printf("TCP relay read error: %v", err)
				conn.Close()
				return
			}

			var sessionID string
			if _, err := fmt.Sscanf(line, "RELAY %s", &sessionID); err != nil {
				log.Printf("TCP relay handshake parse error: %v", err)
				conn.Close()
				return
			}

			if _, err := conn.Write([]byte("OK\n")); err != nil {
				conn.Close()
				return
			}

			mu.Lock()
			if ch, ok := pending[sessionID]; ok {
				delete(pending, sessionID)
				mu.Unlock()

				otherConn := <-ch
				go relayCopy(conn, otherConn)
			} else {
				ch := make(chan net.Conn)
				pending[sessionID] = ch
				mu.Unlock()

				select {
				case ch <- conn:
					return
				case <-time.After(30 * time.Second):
					mu.Lock()
					delete(pending, sessionID)
					mu.Unlock()
					conn.Close()
				}
			}
		}(conn)
	}
}

func readLine(conn net.Conn) (string, error) {
	var b strings.Builder
	tmp := make([]byte, 1)
	for {
		_, err := conn.Read(tmp)
		if err != nil {
			return "", err
		}
		if tmp[0] == '\n' {
			return b.String(), nil
		}
		b.WriteByte(tmp[0])
	}
}

func relayCopy(a, b net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		io.Copy(a, b)
		b.Close()
		wg.Done()
	}()
	go func() {
		io.Copy(b, a)
		a.Close()
		wg.Done()
	}()
	wg.Wait()
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.mu.RLock()
	count := len(s.peers)
	s.mu.RUnlock()
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]interface{}{
		"status":  "ok",
		"clients": count,
	})
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		log.Printf("upgrade error: %v", err)
		return
	}

	p := &peer{
		conn: conn,
	}

	defer func() {
		s.unregisterPeer(p)
		conn.Close()
	}()

	for {
		_, data, err := conn.ReadMessage()
		if err != nil {
			log.Printf("read error from %s: %v", p.clientName, err)
			break
		}

		var msg protocol.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Printf("parse error: %v", err)
			continue
		}

		s.handleMessage(p, &msg)
	}
}

func (s *Server) handleMessage(p *peer, msg *protocol.Message) {
	switch msg.Type {
	case protocol.MsgTypeRegister:
		s.handleRegister(p, msg)
	case protocol.MsgTypeListPeers:
		s.handleListPeers(p)
	case protocol.MsgTypeConnectRequest:
		s.handleConnectRequest(p, msg)
	case protocol.MsgTypePossessToggle:
		s.handlePossessToggle(p, msg)
	case protocol.MsgTypeSignalOffer, protocol.MsgTypeSignalAnswer, protocol.MsgTypeSignalICE:
		s.handleSignal(p, msg)
	case protocol.MsgTypeHeartbeat:
		s.sendMessage(p, &protocol.Message{Type: protocol.MsgTypeHeartbeat})
	case protocol.MsgTypeDisconnect:
		log.Printf("Peer %s disconnecting", p.clientName)
		s.unregisterPeer(p)
	default:
		s.sendError(p, 400, "unknown message type")
	}
}

func (s *Server) registerPeer(p *peer, clientID string, tunnelIP string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.peers[clientID] = p
	s.peersByIP[tunnelIP] = p
}

func (s *Server) unregisterPeer(p *peer) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if p.clientID != "" {
		delete(s.peers, p.clientID)
	}
	if p.tunnelIP != "" {
		delete(s.peersByIP, p.tunnelIP)
		s.ipPool.release(net.ParseIP(p.tunnelIP))
	}
	log.Printf("Peer disconnected: %s (%s)", p.clientName, p.tunnelIP)
}

func (s *Server) handleRegister(p *peer, msg *protocol.Message) {
	var payload protocol.RegisterPayload
	if err := marshalPayload(msg.Payload, &payload); err != nil {
		s.sendError(p, 400, "invalid payload")
		return
	}

	if s.cfg.AuthToken != "" {
		if subtle.ConstantTimeCompare([]byte(payload.AuthToken), []byte(s.cfg.AuthToken)) != 1 {
			s.sendError(p, 401, "invalid auth token")
			return
		}
	}

	tunnelIP, err := s.ipPool.allocate()
	if err != nil {
		s.sendError(p, 500, "no available IP")
		return
	}

	clientID := fmt.Sprintf("%s-%d", payload.ClientName, time.Now().UnixNano())
	p.clientID = clientID
	p.clientName = payload.ClientName
	p.tunnelIP = tunnelIP.String()
	p.allowPossess = payload.AllowPossess
	p.localSubnet = payload.LocalSubnet

	s.registerPeer(p, clientID, p.tunnelIP)

	tunnelNet := s.cfg.TunnelNet
	if tunnelNet == "" {
		tunnelNet = "10.0.0.0/24"
	}
	resp := protocol.RegisterRespPayload{
		ClientID:  clientID,
		TunnelIP:  p.tunnelIP,
		TunnelNet: tunnelNet,
	}

	s.sendMessage(p, &protocol.Message{
		Type:    protocol.MsgTypeRegisterResp,
		Payload: resp,
	})

	log.Printf("Peer registered: %s -> %s (possess=%v, subnet=%s)",
		p.clientName, p.tunnelIP, p.allowPossess, p.localSubnet)
}

func (s *Server) handleListPeers(p *peer) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var peers []protocol.PeerInfo
	for _, peer := range s.peers {
		if peer.clientID == p.clientID {
			continue
		}
		peers = append(peers, protocol.PeerInfo{
			ClientID:     peer.clientID,
			ClientName:   peer.clientName,
			TunnelIP:     peer.tunnelIP,
			AllowPossess: peer.allowPossess,
			LocalSubnet:  peer.localSubnet,
			Connected:    true,
		})
	}

	if peers == nil {
		peers = []protocol.PeerInfo{}
	}

	s.sendMessage(p, &protocol.Message{
		Type:    protocol.MsgTypePeerList,
		Payload: peers,
	})
}

func (s *Server) handleConnectRequest(p *peer, msg *protocol.Message) {
	var payload protocol.ConnectRequestPayload
	if err := marshalPayload(msg.Payload, &payload); err != nil {
		s.sendError(p, 400, "invalid payload")
		return
	}

	s.mu.RLock()
	target, exists := s.peers[payload.TargetClientID]
	if !exists {
		for _, peer := range s.peers {
			if peer.clientName == payload.TargetClientID {
				target = peer
				exists = true
				break
			}
		}
	}
	s.mu.RUnlock()

	if !exists {
		s.sendError(p, 404, "target peer not found")
		return
	}

	if target.clientID == p.clientID {
		s.sendError(p, 400, "cannot connect to yourself")
		return
	}

	if !target.allowPossess {
		s.sendMessage(p, &protocol.Message{
			Type: protocol.MsgTypeConnectResp,
			Payload: protocol.ConnectRespPayload{
				Success: false,
				Message: "target does not allow possession",
			},
		})
		return
	}

	offer := protocol.SignalPayload{
		TargetClientID: target.clientID,
		FromClientID:   p.clientID,
		FromTunnelIP:   p.tunnelIP,
		Data:           fmt.Sprintf("connect_request from %s (%s)", p.clientName, p.tunnelIP),
	}

	s.sendMessage(target, &protocol.Message{
		Type:    protocol.MsgTypeSignalOffer,
		Payload: offer,
	})

	s.sendMessage(p, &protocol.Message{
		Type: protocol.MsgTypeConnectResp,
		Payload: protocol.ConnectRespPayload{
			Success:     true,
			TargetID:    target.clientID,
			TargetIP:    target.tunnelIP,
			TargetName:  target.clientName,
			LocalSubnet: target.localSubnet,
			Message:     "connect request sent",
		},
	})
}

func (s *Server) handlePossessToggle(p *peer, msg *protocol.Message) {
	var payload protocol.PossessTogglePayload
	if err := marshalPayload(msg.Payload, &payload); err != nil {
		s.sendError(p, 400, "invalid payload")
		return
	}

	p.mu.Lock()
	p.allowPossess = payload.AllowPossess
	p.mu.Unlock()

	log.Printf("Possess toggle: %s -> %v", p.clientName, payload.AllowPossess)

	s.broadcastPossessUpdate(p.clientID, payload.AllowPossess)
}

func (s *Server) broadcastPossessUpdate(clientID string, allowPossess bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	update := protocol.PossessUpdatePayload{
		ClientID:     clientID,
		AllowPossess: allowPossess,
	}

	for _, peer := range s.peers {
		if peer.clientID != clientID {
			s.sendMessage(peer, &protocol.Message{
				Type:    protocol.MsgTypePossessUpdate,
				Payload: update,
			})
		}
	}
}

func (s *Server) handleSignal(p *peer, msg *protocol.Message) {
	var payload protocol.SignalPayload
	if err := marshalPayload(msg.Payload, &payload); err != nil {
		s.sendError(p, 400, "invalid payload")
		return
	}

	s.mu.RLock()
	target, exists := s.peers[payload.TargetClientID]
	s.mu.RUnlock()

	if !exists {
		s.sendError(p, 404, "target peer not found for signal")
		return
	}

	payload.FromClientID = p.clientID
	s.sendMessage(target, &protocol.Message{
		Type:    msg.Type,
		Payload: payload,
	})
}

func (s *Server) sendMessage(p *peer, msg *protocol.Message) {
	p.mu.Lock()
	defer p.mu.Unlock()

	data, err := json.Marshal(msg)
	if err != nil {
		log.Printf("marshal error: %v", err)
		return
	}

	if err := p.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		log.Printf("write error to %s: %v", p.clientName, err)
	}
}

func (s *Server) sendError(p *peer, code int, message string) {
	s.sendMessage(p, &protocol.Message{
		Type: protocol.MsgTypeError,
		Payload: protocol.ErrorPayload{
			Code:    code,
			Message: message,
		},
	})
}

func marshalPayload(from, to interface{}) error {
	data, err := json.Marshal(from)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, to)
}
