package client

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/network-tunnel/net-tunnel/client/config"
	"github.com/network-tunnel/net-tunnel/client/protocol"
	"github.com/network-tunnel/net-tunnel/client/tunnel"
)

type peerConnection struct {
	mu           sync.Mutex
	peerID       string
	peerName     string
	peerIP       string
	allowPossess bool
	localSubnet  string
	conn         net.Conn
	cancel       chan struct{}
}

type Client struct {
	cfg        *config.Config
	wsConn     *websocket.Conn
	clientID   string
	tunnelIP   string
	tunIface   *tunnel.Interface
	peers      map[string]*peerConnection
	mu         sync.RWMutex
	relayAddr  string
	localIface string
	stopped    bool
}

func New(cfg *config.Config) *Client {
	return &Client{
		cfg:       cfg,
		peers:     make(map[string]*peerConnection),
		relayAddr: cfg.ServerAddr,
	}
}

func (c *Client) Start() error {
	if err := c.connectWebSocket(); err != nil {
		return fmt.Errorf("connect: %w", err)
	}

	if err := c.register(); err != nil {
		return fmt.Errorf("register: %w", err)
	}

	tun, err := tunnel.CreateTUN(c.tunnelIP, c.cfg.TunnelNet, 1500)
	if err != nil {
		return fmt.Errorf("create TUN: %w", err)
	}
	c.tunIface = tun
	log.Printf("TUN interface %s created with IP %s", tun.Name(), c.tunnelIP)

	_, ipnet, _ := net.ParseCIDR(c.cfg.TunnelNet)
	if ipnet != nil {
		ones, _ := ipnet.Mask.Size()
		log.Printf("Tunnel network: %s/%d, your IP: %s", ipnet.IP.String(), ones, c.tunnelIP)
	}

	if c.cfg.LocalSubnet != "" {
		log.Printf("Registering local subnet: %s", c.cfg.LocalSubnet)
		if err := tunnel.EnableIPForward(); err != nil {
			log.Printf("Warning: enable IP forward: %v", err)
		}
		if iface, err := tunnel.FindInterfaceForSubnet(c.cfg.LocalSubnet); err == nil {
			c.localIface = iface
			log.Printf("Found local interface %s for subnet %s", iface, c.cfg.LocalSubnet)
			if err := tun.AddNAT(iface); err != nil {
				log.Printf("Warning: add NAT: %v", err)
			}
		} else {
			log.Printf("Warning: cannot find interface for subnet %s: %v", c.cfg.LocalSubnet, err)
		}
	}

	go c.tunForwarder()

	go c.heartbeat()

	c.messageLoop()

	c.cleanup()

	return nil
}

func (c *Client) cleanup() {
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return
	}
	c.stopped = true
	c.mu.Unlock()

	if c.tunIface != nil {
		c.mu.RLock()
		peers := make([]*peerConnection, 0, len(c.peers))
		for _, pc := range c.peers {
			peers = append(peers, pc)
		}
		c.mu.RUnlock()

		for _, pc := range peers {
			if pc.localSubnet != "" {
				c.tunIface.RemoveRoute(pc.localSubnet)
			}
			pc.mu.Lock()
			if pc.conn != nil {
				pc.conn.Close()
			}
			pc.mu.Unlock()
		}
		if c.localIface != "" {
			c.tunIface.RemoveNAT(c.localIface)
		}
		c.tunIface.Close()
	}
}

func (c *Client) Stop() {
	if c.wsConn != nil {
		c.wsConn.Close()
	}
}

func (c *Client) connectWebSocket() error {
	u := fmt.Sprintf("ws://%s/ws", c.cfg.ServerAddr)
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		return fmt.Errorf("dial websocket: %w", err)
	}
	c.wsConn = conn
	log.Printf("Connected to relay server at %s", c.cfg.ServerAddr)
	return nil
}

func (c *Client) register() error {
	payload := protocol.RegisterPayload{
		ClientName:   c.cfg.ClientName,
		AuthToken:    c.cfg.AuthToken,
		AllowPossess: c.cfg.AllowPossess,
		TunnelNet:    c.cfg.TunnelNet,
		LocalSubnet:  c.cfg.LocalSubnet,
	}

	msg := protocol.Message{
		Type:    protocol.MsgTypeRegister,
		Payload: payload,
	}

	if err := c.sendMessage(&msg); err != nil {
		return fmt.Errorf("send register: %w", err)
	}

	_, data, err := c.wsConn.ReadMessage()
	if err != nil {
		return fmt.Errorf("read register resp: %w", err)
	}

	var resp protocol.Message
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("parse register resp: %w", err)
	}

	if resp.Type == protocol.MsgTypeError {
		var errPayload protocol.ErrorPayload
		if err := marshalPayload(resp.Payload, &errPayload); err == nil {
			return fmt.Errorf("register error: %s", errPayload.Message)
		}
		return fmt.Errorf("register error")
	}

	if resp.Type != protocol.MsgTypeRegisterResp {
		return fmt.Errorf("unexpected response: %s", resp.Type)
	}

	var regResp protocol.RegisterRespPayload
	if err := marshalPayload(resp.Payload, &regResp); err != nil {
		return fmt.Errorf("parse reg resp: %w", err)
	}

	c.clientID = regResp.ClientID
	c.tunnelIP = regResp.TunnelIP
	log.Printf("Registered as %s with tunnel IP %s", c.clientID, c.tunnelIP)
	return nil
}

func (c *Client) messageLoop() {
	defer c.wsConn.Close()

	for {
		_, data, err := c.wsConn.ReadMessage()
		if err != nil {
			log.Printf("Read error: %v", err)
			break
		}

		var msg protocol.Message
		if err := json.Unmarshal(data, &msg); err != nil {
			log.Printf("Parse error: %v", err)
			continue
		}

		c.handleMessage(&msg)
	}
}

func (c *Client) handleMessage(msg *protocol.Message) {
	switch msg.Type {
	case protocol.MsgTypePeerList:
		c.handlePeerList(msg)
	case protocol.MsgTypeConnectResp:
		c.handleConnectResp(msg)
	case protocol.MsgTypeSignalOffer:
		c.handleSignalOffer(msg)
	case protocol.MsgTypeSignalAnswer:
	case protocol.MsgTypeSignalICE:
	case protocol.MsgTypePossessUpdate:
		c.handlePossessUpdate(msg)
	case protocol.MsgTypeError:
		c.handleError(msg)
	case protocol.MsgTypeHeartbeat:
	case protocol.MsgTypeRegisterResp:
	default:
		log.Printf("Unknown message type: %s", msg.Type)
	}
}

func (c *Client) handlePeerList(msg *protocol.Message) {
	var peers []protocol.PeerInfo
	if err := marshalPayload(msg.Payload, &peers); err != nil {
		log.Printf("Parse peer list error: %v", err)
		return
	}

	log.Printf("Available peers (%d):", len(peers))
	for _, p := range peers {
		possessStr := "allow"
		if !p.AllowPossess {
			possessStr = "deny"
		}
		subnetInfo := ""
		if p.LocalSubnet != "" {
			subnetInfo = fmt.Sprintf(", subnet=%s", p.LocalSubnet)
		}
		log.Printf("  %s (%s) IP=%s [possess=%s]%s",
			p.ClientName, p.ClientID[:8], p.TunnelIP, possessStr, subnetInfo)
	}
}

func (c *Client) handleConnectResp(msg *protocol.Message) {
	var resp protocol.ConnectRespPayload
	if err := marshalPayload(msg.Payload, &resp); err != nil {
		log.Printf("Parse connect resp error: %v", err)
		return
	}

	if resp.Success {
		log.Printf("Connect request sent to %s (%s)", resp.TargetName, resp.TargetIP)
		c.establishDataConnection(resp.TargetID, resp.TargetIP, resp.TargetName, resp.LocalSubnet)
	} else {
		log.Printf("Connect failed: %s", resp.Message)
	}
}

func (c *Client) handleSignalOffer(msg *protocol.Message) {
	var signal protocol.SignalPayload
	if err := marshalPayload(msg.Payload, &signal); err != nil {
		log.Printf("Parse signal error: %v", err)
		return
	}

	log.Printf("Connection request from %s", signal.FromClientID[:8])

	if !c.cfg.AllowPossess {
		log.Printf("Rejecting connection (possession denied)")
		return
	}

	c.establishDataConnection(signal.FromClientID, "", "", "")
}

func (c *Client) handlePossessUpdate(msg *protocol.Message) {
	var update protocol.PossessUpdatePayload
	if err := marshalPayload(msg.Payload, &update); err != nil {
		return
	}

	c.mu.RLock()
	peer, exists := c.peers[update.ClientID]
	c.mu.RUnlock()

	if exists {
		peer.mu.Lock()
		peer.allowPossess = update.AllowPossess
		peer.mu.Unlock()

		if !update.AllowPossess {
			log.Printf("Peer %s revoked possession, disconnecting", update.ClientID[:8])
			go c.disconnectPeer(update.ClientID)
		}
	}
}

func (c *Client) handleError(msg *protocol.Message) {
	var errPayload protocol.ErrorPayload
	if err := marshalPayload(msg.Payload, &errPayload); err == nil {
		log.Printf("Server error [%d]: %s", errPayload.Code, errPayload.Message)
	}
}

func (c *Client) establishDataConnection(peerID, peerIP, peerName, localSubnet string) {
	c.mu.Lock()
	if _, exists := c.peers[peerID]; exists {
		c.mu.Unlock()
		log.Printf("Already connected to %s", peerID[:8])
		return
	}

	pc := &peerConnection{
		peerID:      peerID,
		peerName:    peerName,
		peerIP:      peerIP,
		localSubnet: localSubnet,
		cancel:      make(chan struct{}),
	}
	c.peers[peerID] = pc
	c.mu.Unlock()

	go c.connectToPeer(pc)
}

func (c *Client) connectToPeer(pc *peerConnection) {
	relayHost, _, err := net.SplitHostPort(c.relayAddr)
	if err != nil {
		log.Printf("Invalid relay addr: %v", err)
		c.removePeer(pc.peerID)
		return
	}

	relayAddr := fmt.Sprintf("%s:8081", relayHost)
	sessionID := fmt.Sprintf("%s-%s-%d", c.clientID, pc.peerID, time.Now().UnixNano())

	log.Printf("Connecting to peer %s via relay at %s", pc.peerID[:8], relayAddr)

	conn, err := net.DialTimeout("tcp", relayAddr, 10*time.Second)
	if err != nil {
		log.Printf("Relay connect error: %v", err)
		c.removePeer(pc.peerID)
		return
	}

	handshake := fmt.Sprintf("RELAY %s\n", sessionID)
	if _, err := conn.Write([]byte(handshake)); err != nil {
		log.Printf("Relay handshake error: %v", err)
		conn.Close()
		c.removePeer(pc.peerID)
		return
	}

	response := make([]byte, 3)
	if _, err := io.ReadFull(conn, response); err != nil || string(response) != "OK\n" {
		log.Printf("Relay handshake response error: %v", err)
		conn.Close()
		c.removePeer(pc.peerID)
		return
	}

	pc.conn = conn
	log.Printf("Relay connection established with %s", pc.peerID[:8])

	if pc.localSubnet != "" && c.tunIface != nil {
		go func() {
			if err := c.tunIface.AddRoute(pc.localSubnet); err != nil {
				log.Printf("Add route for %s: %v", pc.localSubnet, err)
			} else {
				log.Printf("Route added: %s -> %s", pc.localSubnet, c.tunIface.Name())
			}
		}()
	}

	go c.peerReader(pc)
}

func (c *Client) peerReader(pc *peerConnection) {
	defer func() {
		c.removePeer(pc.peerID)
	}()

	for {
		var length uint16
		if err := binary.Read(pc.conn, binary.BigEndian, &length); err != nil {
			select {
			case <-pc.cancel:
				return
			default:
				log.Printf("Peer read error from %s: %v", pc.peerID[:8], err)
				return
			}
		}

		data := make([]byte, length)
		if _, err := io.ReadFull(pc.conn, data); err != nil {
			log.Printf("Peer data read error from %s: %v", pc.peerID[:8], err)
			return
		}

		if err := c.tunIface.WritePacket(data); err != nil {
			log.Printf("TUN write error: %v", err)
		}
	}
}

func (c *Client) tunForwarder() {
	for {
		packet, err := c.tunIface.ReadPacket()
		if err != nil {
			continue
		}

		if len(packet) < 20 {
			continue
		}

		version := packet[0] >> 4
		if version != 4 {
			continue
		}

		destIP := net.IP(packet[16:20])
		if destIP.IsMulticast() || destIP.Equal(net.IPv4(255, 255, 255, 255)) || destIP.IsLoopback() || destIP[0] == 127 {
			continue
		}

		peer := c.findPeerForDest(destIP)
		if peer != nil {
			c.sendToPeer(peer, packet)
		}
	}
}

func (c *Client) findPeerForDest(destIP net.IP) *peerConnection {
	c.mu.RLock()
	defer c.mu.RUnlock()

	for _, pc := range c.peers {
		if pc.peerIP != "" {
			peerIP := net.ParseIP(pc.peerIP)
			if peerIP != nil && destIP.Equal(peerIP) {
				return pc
			}
		}

		if pc.localSubnet != "" {
			_, subnet, err := net.ParseCIDR(pc.localSubnet)
			if err == nil && subnet.Contains(destIP) {
				return pc
			}
		}
	}
	return nil
}

func (c *Client) sendToPeer(pc *peerConnection, data []byte) error {
	if len(data) > 65535 {
		return fmt.Errorf("packet too large: %d", len(data))
	}
	pc.mu.Lock()
	defer pc.mu.Unlock()

	if pc.conn == nil {
		return fmt.Errorf("peer not connected")
	}

	length := uint16(len(data))
	if err := binary.Write(pc.conn, binary.BigEndian, length); err != nil {
		return err
	}
	_, err := pc.conn.Write(data)
	return err
}

func (c *Client) disconnectPeer(peerID string) {
	c.mu.RLock()
	pc, exists := c.peers[peerID]
	c.mu.RUnlock()

	if exists {
		close(pc.cancel)
		pc.mu.Lock()
		if pc.conn != nil {
			pc.conn.Close()
		}
		pc.mu.Unlock()
		c.removePeer(peerID)
	}
}

func (c *Client) removePeer(peerID string) {
	c.mu.Lock()

	if pc, exists := c.peers[peerID]; exists {
		if pc.localSubnet != "" && c.tunIface != nil {
			if err := c.tunIface.RemoveRoute(pc.localSubnet); err != nil {
				log.Printf("Remove route %s: %v", pc.localSubnet, err)
			}
		}

		pc.mu.Lock()
		if pc.conn != nil {
			pc.conn.Close()
		}
		pc.mu.Unlock()
		delete(c.peers, peerID)
		log.Printf("Disconnected from %s", peerID[:8])
	}
	c.mu.Unlock()
}

func (c *Client) heartbeat() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		msg := protocol.Message{Type: protocol.MsgTypeHeartbeat}
		if err := c.sendMessage(&msg); err != nil {
			log.Printf("Heartbeat error: %v", err)
		}
	}
}

func (c *Client) ListPeers() error {
	msg := protocol.Message{Type: protocol.MsgTypeListPeers}
	return c.sendMessage(&msg)
}

func (c *Client) ConnectTo(targetClientID string) error {
	msg := protocol.Message{
		Type: protocol.MsgTypeConnectRequest,
		Payload: protocol.ConnectRequestPayload{
			TargetClientID: targetClientID,
		},
	}
	return c.sendMessage(&msg)
}

func (c *Client) TogglePossess(allow bool) error {
	c.cfg.AllowPossess = allow
	msg := protocol.Message{
		Type: protocol.MsgTypePossessToggle,
		Payload: protocol.PossessTogglePayload{
			AllowPossess: allow,
		},
	}
	return c.sendMessage(&msg)
}

func (c *Client) AllowPossess() bool {
	return c.cfg.AllowPossess
}

func (c *Client) TunnelIP() string {
	return c.tunnelIP
}

func (c *Client) ClientID() string {
	return c.clientID
}

func (c *Client) Peers() map[string]*peerConnection {
	c.mu.RLock()
	defer c.mu.RUnlock()
	peers := make(map[string]*peerConnection, len(c.peers))
	for k, v := range c.peers {
		peers[k] = v
	}
	return peers
}

func (p *peerConnection) PeerID() string      { return p.peerID }
func (p *peerConnection) PeerName() string    { return p.peerName }
func (p *peerConnection) PeerIP() string      { return p.peerIP }
func (p *peerConnection) AllowPossess() bool  { return p.allowPossess }
func (p *peerConnection) LocalSubnet() string { return p.localSubnet }

func (c *Client) sendMessage(msg *protocol.Message) error {
	data, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	return c.wsConn.WriteMessage(websocket.TextMessage, data)
}

func marshalPayload(from, to interface{}) error {
	data, err := json.Marshal(from)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, to)
}
